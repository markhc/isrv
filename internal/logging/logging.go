package logging

import (
	"context"
	"errors"
	"fmt"
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
)

// instrumentationName matches telemetry.InstrumentationName. Duplicated here
// to avoid importing the telemetry package from logging (which would create
// an import cycle once telemetry starts using the logger).
const instrumentationName = "github.com/markhc/isrv"

type RequestLoggerOptions struct {
	LogLevel     zapcore.Level
	RecoverPanic bool
	SkipFunc     func(req *http.Request, respStatus int) bool
}

var (
	logger     *zap.Logger
	baseCore   zapcore.Core // file + console cores, reused when attaching the OTel bridge
	logFile    *os.File     // open log file handle, retained for clean shutdown
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

	// Error and above go to stderr, so we need splitting
	highPriority := zap.LevelEnablerFunc(func(lvl zapcore.Level) bool {
		return lvl >= zapcore.ErrorLevel
	})
	lowPriority := zap.LevelEnablerFunc(func(lvl zapcore.Level) bool {
		return lvl < zapcore.ErrorLevel && lvl >= config.Logging.Level
	})

	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "ts"
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderConfig.EncodeLevel = customLevelEncoder
	encoderConfig.ConsoleSeparator = " | "
	encoderConfig.LevelKey = "level"

	encoder := zapcore.NewConsoleEncoder(encoderConfig)

	consoleDebugging := zapcore.Lock(os.Stdout)
	consoleErrors := zapcore.Lock(os.Stderr)

	cores := []zapcore.Core{
		zapcore.NewCore(encoder, consoleErrors, highPriority),
		zapcore.NewCore(encoder, consoleDebugging, lowPriority),
	}

	if config.Logging.LogToFile {
		if dir := filepath.Dir(config.Logging.Path); dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
				panic(err)
			}
		}

		// Append to file it if exists, create it if it doesn't
		file, err := os.OpenFile(config.Logging.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			panic(err)
		}

		logFile = file
		cores = append([]zapcore.Core{
			zapcore.NewCore(encoder, zapcore.AddSync(file), zapcore.DebugLevel),
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

// Shutdown flushes any buffered log records and closes the log file if one
// was opened. It is safe to call even if Initialize was never called.
func Shutdown() error {
	if logger != nil {
		// Sync errors on stdout/stderr are common and harmless (EBADF, EINVAL
		// on devices that do not support fsync), so they are intentionally
		// ignored here. Real I/O failures on the log file would have surfaced
		// at write time.
		_ = logger.Sync()
	}

	if logFile != nil {
		if err := logFile.Close(); err != nil {
			return fmt.Errorf("close log file: %w", err)
		}
		logFile = nil
	}

	return nil
}

func customLevelEncoder(l zapcore.Level, enc zapcore.PrimitiveArrayEncoder) {
	enc.AppendString(fmt.Sprintf("%-5s", l.CapitalString()))
}

// RequestLogger returns a middleware that logs HTTP requests and responses using the global zap.Logger instance.
// based on chi-httplog but simplified and customized for this application.
func RequestLogger(options *RequestLoggerOptions) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			zapFields := make([]zap.Field, 0)

			// Early skip if the SkipFunc returns true
			if options.SkipFunc != nil && options.SkipFunc(r, 0) {
				next.ServeHTTP(w, r)

				return
			}

			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			start := time.Now()
			ctx := r.Context()

			defer func() {
				zapFields = attemptRecover(ww, r, zapFields, options.RecoverPanic)

				duration := time.Since(start)
				statusCode := ww.Status()
				if statusCode == 0 {
					statusCode = 200
				}

				// Skip logging if the request is filtered by the Skip function.
				if options.SkipFunc != nil && options.SkipFunc(r, statusCode) {
					return
				}

				lvl := getLogLevel(statusCode)
				if lvl < options.LogLevel {
					return
				}

				zapFields = append(zapFields,
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
				)

				msg := fmt.Sprintf("%s %s => HTTP %v (%v)", r.Method, r.URL, statusCode, duration)
				Ctx(ctx).Log(lvl, msg, zapFields...)
			}()

			// Now call the next handler in the chain, all the logic is handled in the deferred function above
			next.ServeHTTP(ww, r)
		})
	}
}

func attemptRecover(ww http.ResponseWriter, r *http.Request, fields []zap.Field, recoverPanic bool) []zap.Field {
	if rec := recover(); rec != nil {
		if recoverPanic && r.Header.Get("Connection") != "Upgrade" {
			ww.WriteHeader(http.StatusInternalServerError)
		}

		// Re-panic if it's a client abort or we're not recovering panics
		//
		//nolint:errorlint
		if rec == http.ErrAbortHandler || !recoverPanic {
			defer panic(rec)
		}

		fields = append(fields, zap.String("panic", fmt.Sprintf("%v", rec)))
	}

	return fields
}

func getLogLevel(statusCode int) zapcore.Level {
	switch {
	case statusCode >= 500:
		return zapcore.ErrorLevel
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

// InitializeNop sets the global logger to a no-op logger. Intended for use in tests.
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
// Deprecated: prefer returning errors up to main and letting the deferred
// shutdown code run. Calling Fatal here skips deferred telemetry flush and
// log-file close. Retained only for genuinely unrecoverable startup paths.
func LogFatal(message string, fields ...zap.Field) {
	logger.Fatal(message, fields...)
}

// Ctx returns a *zap.Logger annotated with the OpenTelemetry trace and span
// IDs (when the context carries a valid span context) and the chi request
// ID (when present). Use it inside request handlers, middleware, and any
// other code path that has a context, so log records can be correlated with
// distributed traces in the observability backend.
//
//	logging.Ctx(r.Context()).Info("upload requested", logging.String("filename", name))
//
// When the context is nil or carries neither a span nor a request id, the
// global logger is returned unchanged.
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

// MaybeIP returns a zap field carrying the client IP address only if IP
// logging is enabled in the configuration. When disabled, it returns
// zap.Skip() so the field is omitted from the log record entirely.
//
// Use this everywhere a remote client address is about to be logged so
// the LogIps configuration flag is honored consistently.
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
