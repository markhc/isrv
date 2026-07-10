package logging

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/requestid"
	"github.com/markhc/isrv/internal/configuration"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// setLogging mutates the global logging configuration for the duration of a
// test and restores it afterwards.
func setLogging(t *testing.T, logIps, anonymize bool) {
	t.Helper()

	cfg := &configuration.Get().Logging
	original := *cfg
	t.Cleanup(func() { *cfg = original })

	cfg.LogIps = logIps
	cfg.Anonymize = anonymize
}

// fieldValue encodes a single zap.Field and returns its key and decoded value.
// Skipped fields produce ("", nil) because they add nothing to the encoder.
func fieldValue(t *testing.T, f zap.Field) (string, any) {
	t.Helper()
	enc := zapcore.NewMapObjectEncoder()
	f.AddTo(enc)
	for k, v := range enc.Fields {
		return k, v
	}
	return "", nil
}

// installObservedLogger swaps the package logger for one backed by an observer
// core so emitted entries can be inspected, restoring the original afterwards.
func installObservedLogger(t *testing.T) *observer.ObservedLogs {
	t.Helper()

	core, logs := observer.New(zapcore.DebugLevel)

	original := logger
	logger = zap.New(core)
	t.Cleanup(func() { logger = original })

	return logs
}

func TestFieldHelpers(t *testing.T) {
	// Anonymize disabled so Sensitive/MaybeIP-independent helpers behave plainly.
	setLogging(t, true, false)

	err := errors.New("boom")
	ts := time.Date(2026, time.July, 10, 12, 30, 0, 0, time.UTC)

	tests := []struct {
		name    string
		field   zap.Field
		wantKey string
		wantVal any
	}{
		{"String", String("k", "v"), "k", "v"},
		{"Int", Int("n", 42), "n", int64(42)},
		{"Int64", Int64("n64", int64(99)), "n64", int64(99)},
		{"Float32", Float32("f32", float32(1.5)), "f32", float32(1.5)},
		{"Float64", Float64("f64", 2.5), "f64", 2.5},
		{"Error", Error(err), "error", "boom"},
		{"Time", Time("t", ts, time.RFC1123), "t", ts.Format(time.RFC1123)},
		{"TimeRFC3339", TimeRFC3339("t3339", ts), "t3339", ts.Format(time.RFC3339)},
		{"Any string", Any("a", "hello"), "a", "hello"},
		{"Any int", Any("ai", 7), "ai", int64(7)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, val := fieldValue(t, tt.field)
			assert.Equal(t, tt.wantKey, key)
			assert.Equal(t, tt.wantVal, val)
		})
	}
}

func TestFieldHelperTypes(t *testing.T) {
	assert.Equal(t, zapcore.StringType, String("k", "v").Type)
	assert.Equal(t, zapcore.Int64Type, Int("k", 1).Type)
	assert.Equal(t, zapcore.Int64Type, Int64("k", 1).Type)
	assert.Equal(t, zapcore.Float32Type, Float32("k", 1).Type)
	assert.Equal(t, zapcore.Float64Type, Float64("k", 1).Type)
	assert.Equal(t, zapcore.ErrorType, Error(errors.New("x")).Type)
}

func TestGetLogLevel(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   zapcore.Level
	}{
		{"200 OK", http.StatusOK, zapcore.InfoLevel},
		{"204 No Content", http.StatusNoContent, zapcore.InfoLevel},
		{"301 Moved", http.StatusMovedPermanently, zapcore.InfoLevel},
		{"399 boundary", 399, zapcore.InfoLevel},
		{"400 Bad Request", http.StatusBadRequest, zapcore.WarnLevel},
		{"403 Forbidden", http.StatusForbidden, zapcore.WarnLevel},
		{"404 Not Found", http.StatusNotFound, zapcore.DebugLevel},
		{"405 Method Not Allowed", http.StatusMethodNotAllowed, zapcore.DebugLevel},
		{"429 Too Many Requests", http.StatusTooManyRequests, zapcore.InfoLevel},
		{"499 client", 499, zapcore.WarnLevel},
		{"500 Internal Error", http.StatusInternalServerError, zapcore.ErrorLevel},
		{"503 Unavailable", http.StatusServiceUnavailable, zapcore.ErrorLevel},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, getLogLevel(tt.status))
		})
	}
}

func TestAnonymizeEnabled(t *testing.T) {
	setLogging(t, true, false)
	assert.False(t, anonymizeEnabled())

	setLogging(t, true, true)
	assert.True(t, anonymizeEnabled())
}

func TestMaybeIPValue(t *testing.T) {
	setLogging(t, true, false)
	key, val := fieldValue(t, MaybeIP("remote_addr", "9.9.9.9"))
	assert.Equal(t, "remote_addr", key)
	assert.Equal(t, "9.9.9.9", val)
}

func TestSensitiveValue(t *testing.T) {
	setLogging(t, true, false)
	key, val := fieldValue(t, Sensitive("path", "/secret"))
	assert.Equal(t, "path", key)
	assert.Equal(t, "/secret", val)

	setLogging(t, true, true)
	assert.Equal(t, zapcore.SkipType, Sensitive("path", "/secret").Type)
}

func TestInitializeNopAndGetLogger(t *testing.T) {
	original := logger
	t.Cleanup(func() { logger = original })

	InitializeNop()
	got := GetLogger()
	require.NotNil(t, got)

	// A no-op logger must not panic when used.
	assert.NotPanics(t, func() {
		got.Info("noop", zap.String("k", "v"))
	})
}

func TestInitialize_NoFile(t *testing.T) {
	originalLogger := logger
	originalSink := logSink
	t.Cleanup(func() {
		logger = originalLogger
		logSink = originalSink
	})

	cfg := &configuration.Get().Logging
	saved := *cfg
	t.Cleanup(func() { *cfg = saved })
	cfg.LogToFile = false
	cfg.Level = zapcore.InfoLevel

	logSink = nil
	initializeLocked()

	require.NotNil(t, logger)
	assert.Nil(t, logSink)
	assert.NotPanics(t, func() { logger.Info("hello") })

	require.NoError(t, Shutdown())
}

func TestInitialize_WithFileSink(t *testing.T) {
	originalLogger := logger
	originalSink := logSink
	t.Cleanup(func() {
		logger = originalLogger
		logSink = originalSink
	})

	dir := t.TempDir()
	logPath := filepath.Join(dir, "nested", "isrv.log")

	cfg := &configuration.Get().Logging
	saved := *cfg
	t.Cleanup(func() { *cfg = saved })
	cfg.LogToFile = true
	cfg.Path = logPath
	cfg.Level = zapcore.DebugLevel
	cfg.MaxSizeMB = 1
	cfg.MaxBackups = 1
	cfg.MaxAgeDays = 1
	cfg.Compress = false

	logSink = nil
	initializeLocked()

	require.NotNil(t, logger)
	require.NotNil(t, logSink)

	logger.Info("written to file", zap.String("k", "v"))

	require.NoError(t, Shutdown())
	assert.Nil(t, logSink, "Shutdown must clear the sink")

	// The rotator should have created the log directory and file.
	assert.FileExists(t, logPath)
}

func TestInitialize_SyncOnce(t *testing.T) {
	// Initialize is guarded by sync.Once; calling it must not panic regardless
	// of whether initialization already ran elsewhere.
	assert.NotPanics(t, Initialize)
}

func TestShutdown_NoLogger(t *testing.T) {
	originalLogger := logger
	originalSink := logSink
	t.Cleanup(func() {
		logger = originalLogger
		logSink = originalSink
	})

	logger = nil
	logSink = nil
	assert.NoError(t, Shutdown())
}

func TestLogLevelHelpers(t *testing.T) {
	logs := installObservedLogger(t)

	LogDebug("d", zap.String("k", "1"))
	LogInfo("i", zap.String("k", "2"))
	LogWarn("w", zap.String("k", "3"))
	LogError("e", zap.String("k", "4"))

	all := logs.All()
	require.Len(t, all, 4)
	assert.Equal(t, zapcore.DebugLevel, all[0].Level)
	assert.Equal(t, "d", all[0].Message)
	assert.Equal(t, zapcore.InfoLevel, all[1].Level)
	assert.Equal(t, zapcore.WarnLevel, all[2].Level)
	assert.Equal(t, zapcore.ErrorLevel, all[3].Level)
}

func TestCtxHelpers_NoRequestID(t *testing.T) {
	logs := installObservedLogger(t)

	DebugCtx(context.Background(), "d")
	InfoCtx(context.Background(), "i")
	WarnCtx(nil, "w") //nolint:staticcheck // intentionally nil to exercise the branch
	ErrorCtx(context.Background(), "e")

	all := logs.All()
	require.Len(t, all, 4)
	for _, entry := range all {
		// No request ID in context, so no request_id field should be attached.
		_, ok := entry.ContextMap()["request_id"]
		assert.False(t, ok)
	}
}

func TestCtx_NilLogger(t *testing.T) {
	original := logger
	t.Cleanup(func() { logger = original })

	logger = nil
	assert.Nil(t, Ctx(context.Background()))
}

func TestCtx_ReturnsSameLoggerWithoutRequestID(t *testing.T) {
	installObservedLogger(t)
	assert.Same(t, logger, Ctx(nil))                  //nolint:staticcheck // nil ctx branch
	assert.Same(t, logger, Ctx(context.Background())) // no request id → same logger
}

// newLoggerApp builds a Fiber app that attaches a request ID and runs the
// RequestLogger middleware, ending in a handler that returns the given status.
func newLoggerApp(opts *RequestLoggerOptions, status int) *fiber.App {
	app := fiber.New(fiber.Config{PassLocalsToContext: true})
	app.Use(requestid.New())
	app.Use(RequestLogger(opts))
	app.Add([]string{fiber.MethodGet, fiber.MethodOptions}, "/files/:id", func(c fiber.Ctx) error {
		return c.SendStatus(status)
	})
	return app
}

func TestRequestLogger_LogsWithFields(t *testing.T) {
	setLogging(t, true, false)
	logs := installObservedLogger(t)

	opts := &RequestLoggerOptions{LogLevel: zapcore.DebugLevel}
	app := newLoggerApp(opts, http.StatusOK)

	req := httptest.NewRequest(http.MethodGet, "/files/secret-id", nil)
	req.Header.Set(fiber.HeaderUserAgent, "test-agent")
	resp, err := app.Test(req)
	require.NoError(t, err)
	resp.Body.Close()

	all := logs.All()
	require.Len(t, all, 1)

	entry := all[0]
	assert.Equal(t, zapcore.InfoLevel, entry.Level)
	fields := entry.ContextMap()
	assert.Equal(t, "GET", fields["method"])
	assert.Equal(t, "/files/secret-id", fields["path"])
	assert.Equal(t, "test-agent", fields["user_agent"])
	assert.Contains(t, fields, "remote_addr")
	assert.Contains(t, fields, "request_id", "Ctx should attach the request ID")
	assert.Contains(t, entry.Message, "/files/secret-id")
}

func TestRequestLogger_AnonymizeDropsSuccess(t *testing.T) {
	setLogging(t, true, true)
	logs := installObservedLogger(t)

	opts := &RequestLoggerOptions{LogLevel: zapcore.DebugLevel}
	app := newLoggerApp(opts, http.StatusOK)

	req := httptest.NewRequest(http.MethodGet, "/files/secret-id", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	resp.Body.Close()

	// Successful access logs (info level) are dropped in anonymize mode.
	assert.Empty(t, logs.All())
}

func TestRequestLogger_AnonymizeErrorOmitsIdentifyingFields(t *testing.T) {
	setLogging(t, true, true)
	logs := installObservedLogger(t)

	opts := &RequestLoggerOptions{LogLevel: zapcore.DebugLevel}
	app := newLoggerApp(opts, http.StatusInternalServerError)

	req := httptest.NewRequest(http.MethodGet, "/files/secret-id", nil)
	req.Header.Set(fiber.HeaderUserAgent, "test-agent")
	resp, err := app.Test(req)
	require.NoError(t, err)
	resp.Body.Close()

	all := logs.All()
	require.Len(t, all, 1)

	entry := all[0]
	assert.Equal(t, zapcore.ErrorLevel, entry.Level)

	fields := entry.ContextMap()
	// Identifying fields must be omitted in anonymize mode.
	assert.NotContains(t, fields, "path")
	assert.NotContains(t, fields, "host")
	assert.NotContains(t, fields, "user_agent")
	assert.NotContains(t, fields, "remote_addr")
	// Non-identifying fields still present.
	assert.Equal(t, "GET", fields["method"])
	assert.Contains(t, fields, "status")
	// The path is redacted from the message too.
	assert.Contains(t, entry.Message, "[redacted]")
	assert.NotContains(t, entry.Message, "secret-id")
}

func TestRequestLogger_SkipFuncPre(t *testing.T) {
	setLogging(t, true, false)
	logs := installObservedLogger(t)

	opts := &RequestLoggerOptions{
		LogLevel: zapcore.DebugLevel,
		SkipFunc: func(c fiber.Ctx, _ int) bool {
			return c.Method() == fiber.MethodOptions
		},
	}
	app := newLoggerApp(opts, http.StatusOK)

	req := httptest.NewRequest(http.MethodOptions, "/files/x", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Empty(t, logs.All(), "OPTIONS requests should be skipped")
}

func TestRequestLogger_SkipFuncPost(t *testing.T) {
	setLogging(t, true, false)
	logs := installObservedLogger(t)

	opts := &RequestLoggerOptions{
		LogLevel: zapcore.DebugLevel,
		SkipFunc: func(_ fiber.Ctx, respStatus int) bool {
			// Skip only once the status is known (post-chain call).
			return respStatus == http.StatusOK
		},
	}
	app := newLoggerApp(opts, http.StatusOK)

	req := httptest.NewRequest(http.MethodGet, "/files/x", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Empty(t, logs.All(), "post-chain skip should drop the log")
}

func TestRequestLogger_LevelBelowThresholdDropped(t *testing.T) {
	setLogging(t, true, false)
	logs := installObservedLogger(t)

	// Only warn and above are logged; a 200 (info) must be dropped.
	opts := &RequestLoggerOptions{LogLevel: zapcore.WarnLevel}
	app := newLoggerApp(opts, http.StatusOK)

	req := httptest.NewRequest(http.MethodGet, "/files/x", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Empty(t, logs.All())
}

func TestMaybeIP(t *testing.T) {
	tests := []struct {
		name      string
		logIps    bool
		anonymize bool
		wantValue string
		wantSkip  bool
	}{
		{name: "logs ip when enabled", logIps: true, anonymize: false, wantValue: "1.2.3.4"},
		{name: "skips ip when logIps disabled", logIps: false, anonymize: false, wantSkip: true},
		{name: "skips ip in anonymize mode even with logIps on", logIps: true, anonymize: true, wantSkip: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setLogging(t, tt.logIps, tt.anonymize)

			field := MaybeIP("remote_addr", "1.2.3.4")

			if tt.wantSkip {
				if field.Type != zapcore.SkipType {
					t.Fatalf("expected skipped field, got type %v value %q", field.Type, field.String)
				}
				return
			}

			if field.String != tt.wantValue {
				t.Fatalf("expected %q, got %q", tt.wantValue, field.String)
			}
		})
	}
}

func TestSensitive(t *testing.T) {
	t.Run("logs value when not anonymized", func(t *testing.T) {
		setLogging(t, true, false)

		field := Sensitive("filename", "secret.txt")
		if field.String != "secret.txt" {
			t.Fatalf("expected value to be logged, got %q (type %v)", field.String, field.Type)
		}
	})

	t.Run("skips value in anonymize mode", func(t *testing.T) {
		setLogging(t, true, true)

		field := Sensitive("filename", "secret.txt")
		if field.Type != zapcore.SkipType {
			t.Fatalf("expected skipped field in anonymize mode, got type %v value %q", field.Type, field.String)
		}
	})
}
