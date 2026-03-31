package logging

import (
	"fmt"
	"os"
	"time"

	"github.com/markhc/isrv/internal/configuration"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

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

	// Append to file it if exists, create it if it doesn't
	file, err := os.OpenFile(config.Logging.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)

	if err != nil {
		fmt.Println("Failed to create log file:", err)
		panic(err)
	}

	fileSyncer := zapcore.AddSync(file)

	consoleDebugging := zapcore.Lock(os.Stdout)
	consoleErrors := zapcore.Lock(os.Stderr)

	// Join the outputs
	core := zapcore.NewTee(
		zapcore.NewCore(encoder, fileSyncer, zapcore.DebugLevel),
		zapcore.NewCore(encoder, consoleErrors, highPriority),
		zapcore.NewCore(encoder, consoleDebugging, lowPriority),
	)

	logger = zap.New(core)
}

func customLevelEncoder(l zapcore.Level, enc zapcore.PrimitiveArrayEncoder) {
	enc.AppendString(fmt.Sprintf("%-5s", l.CapitalString()))
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

func Any(key string, value interface{}) zap.Field {
	return zap.Any(key, value)
}
