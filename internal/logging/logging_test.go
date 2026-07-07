package logging

import (
	"testing"

	"github.com/markhc/isrv/internal/configuration"
	"go.uber.org/zap/zapcore"
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
