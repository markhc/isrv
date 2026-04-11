package logging

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/markhc/isrv/internal/configuration"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type RequestLoggerOptions struct {
	LogLevel     zapcore.Level
	RecoverPanic bool
	SkipFunc     func(req *http.Request, respStatus int) bool
}

var logger *zap.Logger

// Initialize sets up the global logger, writing to both the configured log
// file and the console (stdout/stderr split by level).
func Initialize() {
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

	if config.Logging.LogToFile {
		if dir := filepath.Dir(config.Logging.Path); dir != "." {
			if err := os.Mkdir(dir, 0o755); err != nil && !os.IsExist(err) {
				panic(err)
			}
		}

		// Append to file it if exists, create it if it doesn't
		file, err := os.OpenFile(config.Logging.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			panic(err)
		}

		fileSyncer := zapcore.AddSync(file)

		// Join the outputs
		core := zapcore.NewTee(
			zapcore.NewCore(encoder, fileSyncer, zapcore.DebugLevel),
			zapcore.NewCore(encoder, consoleErrors, highPriority),
			zapcore.NewCore(encoder, consoleDebugging, lowPriority),
		)

		logger = zap.New(core)
	} else {
		// Only log to console
		core := zapcore.NewTee(
			zapcore.NewCore(encoder, consoleErrors, highPriority),
			zapcore.NewCore(encoder, consoleDebugging, lowPriority),
		)

		logger = zap.New(core)
	}
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
					zap.String("remote_addr", r.RemoteAddr),
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
				logger.Log(lvl, msg, zapFields...)
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
func LogFatal(message string, fields ...zap.Field) {
	logger.Fatal(message, fields...)
}

// String creates a zap string field.
func String(key, value string) zap.Field {
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
