package models

import (
	"testing"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
)

func Test_AdminConfiguration_Enabled(t *testing.T) {
	tests := []struct {
		name     string
		username string
		password string
		want     bool
	}{
		{"both set", "admin", "secret", true},
		{"username only", "admin", "", false},
		{"password only", "", "secret", false},
		{"neither set", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := AdminConfiguration{Username: tt.username, Password: tt.password}
			assert.Equal(t, tt.want, a.Enabled())
		})
	}
}

func Test_EncryptionConfiguration_HasKey(t *testing.T) {
	tests := []struct {
		name         string
		identity     string
		identityFile string
		want         bool
	}{
		{"identity set", "AGE-SECRET-KEY-1...", "", true},
		{"identity file set", "", "/etc/isrv/key.txt", true},
		{"both set", "AGE-SECRET-KEY-1...", "/etc/isrv/key.txt", true},
		{"neither set", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := EncryptionConfiguration{Identity: tt.identity, IdentityFile: tt.identityFile}
			assert.Equal(t, tt.want, e.HasKey())
		})
	}
}

func Test_Configuration_ToYaml_roundTrip(t *testing.T) {
	cfg := Configuration{
		ServerName:     "isrv",
		ServerURL:      "https://example.com",
		ServerPort:     8080,
		MaxFileSizeMB:  100,
		MinAgeDays:     30,
		MaxAgeDays:     365,
		RandomIDLength: 6,
		Admin: AdminConfiguration{
			Username:   "admin",
			Password:   "secret",
			SessionTTL: 2 * time.Hour,
		},
		Storage: StorageConfiguration{
			Type:     "local",
			BasePath: "/data",
		},
		Logging: LoggingConfiguration{
			Level: zapcore.InfoLevel,
		},
		Encryption: EncryptionConfiguration{
			Enabled:  true,
			Identity: "AGE-SECRET-KEY-1TEST",
		},
		DebugMode: true,
	}

	out := cfg.ToYaml()
	require.NotEmpty(t, out)

	// The output should contain recognisable YAML keys.
	yamlStr := string(out)
	assert.Contains(t, yamlStr, "serverName: isrv")
	assert.Contains(t, yamlStr, "serverPort: 8080")

	// Unmarshalling it back should reproduce the original configuration.
	// Nil vs empty slices are not distinguishable through a YAML round trip, so
	// compare the stable marshalled representation rather than struct equality.
	var got Configuration
	require.NoError(t, yaml.Unmarshal(out, &got))
	assert.Equal(t, string(out), string(got.ToYaml()))

	// Spot-check a few scalar fields survive the round trip exactly.
	assert.Equal(t, cfg.ServerPort, got.ServerPort)
	assert.Equal(t, cfg.Admin.SessionTTL, got.Admin.SessionTTL)
	assert.Equal(t, cfg.Logging.Level, got.Logging.Level)
	assert.Equal(t, cfg.Encryption.Enabled, got.Encryption.Enabled)
}

func Test_Configuration_ToYaml_zeroValue(t *testing.T) {
	var cfg Configuration
	out := cfg.ToYaml()
	require.NotEmpty(t, out)

	var got Configuration
	require.NoError(t, yaml.Unmarshal(out, &got))
	assert.Equal(t, string(out), string(got.ToYaml()))
}
