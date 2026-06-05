package logging

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/markhc/isrv/internal/configuration"
	"go.opentelemetry.io/contrib/bridges/otelzap"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

// instrumentationName mirrors telemetry.InstrumentationName. It is duplicated
// here to avoid an import cycle once telemetry starts using the logger.
const instrumentationName = "github.com/markhc/isrv"

// RequestLoggerOptions configures the per-request logger middleware.
type RequestLoggerOptions struct {
	LogLevel zapcore.Level
	SkipFunc func(req *http.Request, respStatus int) bool
}

var (
	logger     *zap.Logger
	baseCore   zapcore.Core // file + console cores, reused when attaching the OTel bridge
	logSink    io.Closer    // rotating file sink (lumberjack), retained for clean shutdown
	initOnce   sync.Once
	bridgeOnce sync.Once
)

// Initialize sets up the global logger, writing to both the configured log
// file and the console (stdout/stderr split by level).
//
// It is safe to call multiple times; subsequent calls are no-ops. To enable
// forwarding of log records to the OpenTelemetry log pipeline, call
// AttachOTelBridge after the global OTel logger provider has been registered.
func Initialize() {
	initOnce.Do(initializeLocked)
}

func initializeLocked() {
	config := configuration.Get()

	// Error and above go to stderr; everything else goes to stdout.
	highPriority := zap.LevelEnablerFunc(func(lvl zapcore.Level) bool {
		return lvl >= zapcore.ErrorLevel
	})
	lowPriority := zap.LevelEnablerFunc(func(lvl zapcore.Level) bool {
		return lvl < zapcore.ErrorLevel && lvl >= config.Logging.Level
	})

	consoleEncoderConfig := zap.NewProductionEncoderConfig()
	consoleEncoderConfig.TimeKey = "ts"
	consoleEncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	consoleEncoderConfig.EncodeLevel = customLevelEncoder
	consoleEncoderConfig.ConsoleSeparator = " | "
	consoleEncoderConfig.LevelKey = "level"

	consoleEncoder := zapcore.NewConsoleEncoder(consoleEncoderConfig)

	consoleDebugging := zapcore.Lock(os.Stdout)
	consoleErrors := zapcore.Lock(os.Stderr)

	cores := []zapcore.Core{
		zapcore.NewCore(consoleEncoder, consoleErrors, highPriority),
		zapcore.NewCore(consoleEncoder, consoleDebugging, lowPriority),
	}

	if config.Logging.LogToFile {
		if dir := filepath.Dir(config.Logging.Path); dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
				panic(err)
			}
		}

		// Rotate the file sink so long-running servers do not fill the disk.
		// Zero values fall back to lumberjack defaults (100 MiB / unlimited
		// backups / no expiration / no compression).
		rotator := &lumberjack.Logger{
			Filename:   config.Logging.Path,
			MaxSize:    config.Logging.MaxSizeMB,
			MaxBackups: config.Logging.MaxBackups,
			MaxAge:     config.Logging.MaxAgeDays,
			Compress:   config.Logging.Compress,
		}
		logSink = rotator

		// The file sink uses JSON encoding so records are machine-parseable
		// by aggregators (Loki, Elasticsearch, etc.); the console sinks stay
		// human-readable.
		fileEncoderConfig := zap.NewProductionEncoderConfig()
		fileEncoderConfig.TimeKey = "ts"
		fileEncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
		fileEncoderConfig.LevelKey = "level"
		fileEncoderConfig.EncodeLevel = zapcore.LowercaseLevelEncoder
		fileEncoder := zapcore.NewJSONEncoder(fileEncoderConfig)

		cores = append([]zapcore.Core{
			zapcore.NewCore(fileEncoder, zapcore.AddSync(rotator), zapcore.DebugLevel),
		}, cores...)
	}

	baseCore = zapcore.NewTee(cores...)
	logger = zap.New(baseCore)
}

// AttachOTelBridge rewraps the global logger to additionally forward records
// to the OpenTelemetry log pipeline via the otelzap bridge.
//
// Must be called after the global OTel logger provider has been registered.
// The underlying file/console cores opened by Initialize are reused (no new
// file descriptors are opened), and subsequent calls are no-ops.
func AttachOTelBridge() {
	bridgeOnce.Do(func() {
		if baseCore == nil {
			return
		}
		logger = zap.New(zapcore.NewTee(baseCore, otelzap.NewCore(instrumentationName)))
	})
}

// Shutdown flushes buffered log records and closes the rotating log file if
// one was opened. It is safe to call even if Initialize was never invoked.
func Shutdown() error {
	if logger != nil {
		// Sync errors on stdout/stderr (EBADF, EINVAL on devices that do not
		// support fsync) are common and harmless. Real I/O failures on the
		// log file would have surfaced at write time.
		_ = logger.Sync()
	}

	if logSink != nil {
		if err := logSink.Close(); err != nil {
			return fmt.Errorf("close log sink: %w", err)
		}
		logSink = nil
	}

	return nil
}

func customLevelEncoder(l zapcore.Level, enc zapcore.PrimitiveArrayEncoder) {
	enc.AppendString(fmt.Sprintf("%-5s", l.CapitalString()))
}

// RequestLogger returns a middleware that logs each HTTP request/response
// pair through the global zap logger. It is loosely based on chi-httplog,
// simplified for this application.
//
// Panic recovery is intentionally NOT handled here; install a dedicated
// recoverer middleware upstream so panics are recorded on the active
// OpenTelemetry span before this middleware logs the request/response.
func RequestLogger(options *RequestLoggerOptions) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if options.SkipFunc != nil && options.SkipFunc(r, 0) {
				next.ServeHTTP(w, r)

				return
			}

			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			start := time.Now()
			ctx := r.Context()

			defer func() {
				duration := time.Since(start)
				statusCode := ww.Status()
				if statusCode == 0 {
					statusCode = 200
				}

				if options.SkipFunc != nil && options.SkipFunc(r, statusCode) {
					return
				}

				lvl := getLogLevel(statusCode)
				if lvl < options.LogLevel {
					return
				}

				zapFields := []zap.Field{
					zap.String("method", r.Method),
					zap.String("path", r.URL.Path),
					MaybeIP("remote_addr", r.RemoteAddr),
					zap.String("host", r.Host),
					zap.String("scheme", scheme(r)),
					zap.String("proto", r.Proto),
					zap.Int64("length", r.ContentLength),
					zap.String("user_agent", r.UserAgent()),
					zap.Int("status", statusCode),
					zap.Duration("duration", duration),
					zap.Int("response_bytes", ww.BytesWritten()),
				}

				msg := fmt.Sprintf("%s %s => HTTP %v (%v)", r.Method, r.URL, statusCode, duration)
				Ctx(ctx).Log(lvl, msg, zapFields...)
			}()

			next.ServeHTTP(ww, r)
		})
	}
}

func getLogLevel(statusCode int) zapcore.Level {
	switch {
	case statusCode >= 500:
		return zapcore.ErrorLevel
	case statusCode == 404, statusCode == 405:
		// 404/405 are typically scanner/probe noise. Keep them recorded but
		// only surface them when the operator sets the file/console sink to
		// debug.
		return zapcore.DebugLevel
	case statusCode == 429:
		return zapcore.InfoLevel
	case statusCode >= 400:
		return zapcore.WarnLevel
	default:
		return zapcore.InfoLevel
	}
}

func scheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}

	return "http"
}

// GetLogger returns the global zap.Logger instance.
func GetLogger() *zap.Logger {
	return logger
}

// InitializeNop replaces the global logger with a no-op logger. Intended for
// use in tests.
func InitializeNop() {
	logger = zap.NewNop()
}

// LogDebug logs a message at debug level.
func LogDebug(message string, fields ...zap.Field) {
	logger.Debug(message, fields...)
}

// LogInfo logs a message at info level.
func LogInfo(message string, fields ...zap.Field) {
	logger.Info(message, fields...)
}

// LogWarn logs a message at warn level.
func LogWarn(message string, fields ...zap.Field) {
	logger.Warn(message, fields...)
}

// LogError logs a message at error level.
func LogError(message string, fields ...zap.Field) {
	logger.Error(message, fields...)
}

// LogFatal logs a message at fatal level and then exits the application.
//
// Deprecated: prefer returning errors up to main and letting deferred
// shutdown code run. LogFatal skips deferred telemetry flush and log-file
// close. Retained only for genuinely unrecoverable startup paths.
func LogFatal(message string, fields ...zap.Field) {
	logger.Fatal(message, fields...)
}

// Ctx returns a *zap.Logger annotated with the OpenTelemetry trace and span
// IDs (when ctx carries a valid span context) and the chi request ID (when
// present). Use it inside request handlers, middleware, and any other code
// path that has a context, so log records can be correlated with distributed
// traces in the observability backend.
//
//	logging.Ctx(r.Context()).Info("upload requested", logging.String("filename", name))
//
// When ctx is nil or carries neither a span nor a request ID, the global
// logger is returned unchanged.
func Ctx(ctx context.Context) *zap.Logger {
	if logger == nil {
		return logger
	}
	if ctx == nil {
		return logger
	}

	// Pre-size for the common case: trace_id + span_id + request_id.
	fields := make([]zap.Field, 0, 3)

	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		fields = append(fields,
			zap.String("trace_id", sc.TraceID().String()),
			zap.String("span_id", sc.SpanID().String()),
		)
	}

	if reqID := middleware.GetReqID(ctx); reqID != "" {
		fields = append(fields, zap.String("request_id", reqID))
	}

	if len(fields) == 0 {
		return logger
	}

	return logger.With(fields...)
}

// DebugCtx logs a message at debug level with trace/request correlation
// fields extracted from ctx.
func DebugCtx(ctx context.Context, message string, fields ...zap.Field) {
	Ctx(ctx).Debug(message, fields...)
}

// InfoCtx logs a message at info level with trace/request correlation
// fields extracted from ctx.
func InfoCtx(ctx context.Context, message string, fields ...zap.Field) {
	Ctx(ctx).Info(message, fields...)
}

// WarnCtx logs a message at warn level with trace/request correlation
// fields extracted from ctx.
func WarnCtx(ctx context.Context, message string, fields ...zap.Field) {
	Ctx(ctx).Warn(message, fields...)
}

// ErrorCtx logs a message at error level with trace/request correlation
// fields extracted from ctx.
func ErrorCtx(ctx context.Context, message string, fields ...zap.Field) {
	Ctx(ctx).Error(message, fields...)
}

// String creates a zap string field.
func String(key, value string) zap.Field {
	return zap.String(key, value)
}

// MaybeIP returns a zap field carrying the client IP address only when IP
// logging is enabled in the configuration. When disabled it returns
// zap.Skip() so the field is omitted from the record entirely.
//
// Use this wherever a remote client address is about to be logged so the
// LogIps configuration flag is honored consistently.
func MaybeIP(key, ipAddress string) zap.Field {
	if !configuration.Get().Logging.LogIps {
		return zap.Skip()
	}

	return zap.String(key, ipAddress)
}

// Int creates a zap int field.
func Int(key string, value int) zap.Field {
	return zap.Int(key, value)
}

func Int64(key string, value int64) zap.Field {
	return zap.Int64(key, value)
}

func Float32(key string, value float32) zap.Field {
	return zap.Float32(key, value)
}

func Float64(key string, value float64) zap.Field {
	return zap.Float64(key, value)
}

func Error(err error) zap.Field {
	return zap.Error(err)
}

func Time(key string, value time.Time, format string) zap.Field {
	return zap.String(key, value.Format(format))
}

func TimeRFC3339(key string, value time.Time) zap.Field {
	return zap.String(key, value.Format(time.RFC3339))
}

func Any(key string, value any) zap.Field {
	return zap.Any(key, value)
}
