package logging

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/requestid"
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
	SkipFunc func(c fiber.Ctx, respStatus int) bool
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
	consoleEncoderConfig.EncodeLevel = zapcore.LowercaseLevelEncoder
	consoleEncoderConfig.LevelKey = "level"

	consoleEncoder := zapcore.NewJSONEncoder(consoleEncoderConfig)

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

// RequestLogger returns a Fiber middleware that logs each HTTP request/response
// pair through the global zap logger. It is loosely based on chi-httplog,
// simplified for this application.
func RequestLogger(options *RequestLoggerOptions) fiber.Handler {
	return func(c fiber.Ctx) error {
		if options.SkipFunc != nil && options.SkipFunc(c, 0) {
			return c.Next()
		}

		// Run the request chain and capture the response status code and duration.
		start := time.Now()
		err := c.Next()
		duration := time.Since(start)
		statusCode := c.Response().StatusCode()

		if options.SkipFunc != nil && options.SkipFunc(c, statusCode) {
			return err
		}

		lvl := getLogLevel(statusCode)
		if lvl < options.LogLevel {
			return err
		}

		// In anonymize mode only maintenance-relevant records survive: successful
		// access-log lines (info/debug) are dropped, leaving warnings and errors.
		if anonymizeEnabled() && lvl < zapcore.WarnLevel {
			return err
		}

		// The path carries the file ID, so it is redacted from the message in
		// anonymize mode just as it is from the structured field below.
		loggedPath := c.Path()
		if anonymizeEnabled() {
			loggedPath = "[redacted]"
		}

		// request_id is added by Ctx below via requestid.FromContext; adding it
		// here would read the wrong (request) header and duplicate the key.
		zapFields := []zap.Field{
			zap.String("method", c.Method()),
			Sensitive("path", c.Path()),
			MaybeIP("remote_addr", c.IP()),
			Sensitive("host", c.Hostname()),
			zap.String("scheme", c.Protocol()),
			zap.String("proto", c.Protocol()),
			zap.Int64("length", int64(len(c.Body()))),
			Sensitive("user_agent", c.Get(fiber.HeaderUserAgent)),
			zap.Int("status", statusCode),
			zap.Duration("duration", duration),
			zap.Int("response_bytes", len(c.Response().Body())),
		}

		msg := fmt.Sprintf("%s %s => HTTP %v (%v)", c.Method(), loggedPath, statusCode, duration)
		Ctx(c.Context()).Log(lvl, msg, zapFields...)

		return err
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

	if reqID := requestid.FromContext(ctx); reqID != "" {
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

// anonymizeEnabled reports whether minimal-logging (anonymize) mode is active.
// In this mode successful access logs are suppressed and every identifying
// field is omitted, for no-logs deployments such as Tor onion services.
func anonymizeEnabled() bool {
	return configuration.Get().Logging.Anonymize
}

// MaybeIP returns a zap field carrying the client IP address only when IP
// logging is enabled in the configuration. When disabled it returns
// zap.Skip() so the field is omitted from the record entirely.
//
// Use this wherever a remote client address is about to be logged so the
// LogIps configuration flag is honored consistently. Anonymize mode always
// suppresses the IP regardless of LogIps.
func MaybeIP(key, ipAddress string) zap.Field {
	cfg := configuration.Get().Logging
	if cfg.Anonymize || !cfg.LogIps {
		return zap.Skip()
	}

	return zap.String(key, ipAddress)
}

// Sensitive returns a zap string field carrying a potentially identifying
// value (filename, request path, host, and similar) only when anonymize mode
// is disabled. In anonymize mode it returns zap.Skip() so the value never
// reaches the log record.
//
// Use this for any field that could deanonymize a user, so the Anonymize
// configuration flag is honored consistently across the codebase.
func Sensitive(key, value string) zap.Field {
	if anonymizeEnabled() {
		return zap.Skip()
	}

	return zap.String(key, value)
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
