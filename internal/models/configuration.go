package models

import (
	"time"

	"github.com/goccy/go-yaml"
	"go.uber.org/zap/zapcore"
)

// StorageConfiguration holds settings for the storage backend.
type StorageConfiguration struct {
	Type string `yaml:"type"` // "local" or "s3"
	// BasePath is the root directory for local storage or the key prefix for S3 storage.
	BasePath string `yaml:"basePath"`

	// Object storage settings (S3).
	AccessKey  string `yaml:"accessKey"`
	SecretKey  string `yaml:"secretKey"`
	BucketName string `yaml:"bucketName"`
	Region     string `yaml:"region"`
	Endpoint   string `yaml:"endpoint"`
}

// DatabaseConfiguration holds settings for the database backend.
type DatabaseConfiguration struct {
	Type     string `yaml:"type"`     // "sqlite" and "postgres" supported
	DSN      string `yaml:"dsn"`      // Data source name; when set, overrides the per-field settings below.
	Host     string `yaml:"host"`     // Host for networked databases.
	Port     int    `yaml:"port"`     // Port for networked databases.
	User     string `yaml:"user"`     // User for networked databases.
	Password string `yaml:"password"` // Password for networked databases.
	DBName   string `yaml:"dbName"`   // Database name.
	FilePath string `yaml:"filePath"` // File path for file-based databases.
}

// LoggingConfiguration holds settings for structured logging.
//
// When LogToFile is true the file sink is wrapped with lumberjack to provide
// size-based rotation. Zero values for the rotation knobs fall back to
// lumberjack's defaults (100 MiB per file, unlimited backups, no expiration,
// no compression).
type LoggingConfiguration struct {
	LogToFile  bool          `yaml:"logToFile"`
	LogUploads bool          `yaml:"logUploads"`
	LogIps     bool          `yaml:"logIps"`
	Level      zapcore.Level `yaml:"level"`
	Path       string        `yaml:"path"`

	// Rotation settings (file sink only).
	MaxSizeMB  int  `yaml:"maxSizeMb"`  // Max size in MiB before a file is rotated.
	MaxBackups int  `yaml:"maxBackups"` // Max number of rotated files to retain.
	MaxAgeDays int  `yaml:"maxAgeDays"` // Max age in days before a rotated file is removed.
	Compress   bool `yaml:"compress"`   // Whether rotated files should be gzip-compressed.
}

// CleanupConfiguration holds settings for the background file cleanup service.
type CleanupConfiguration struct {
	Enabled  bool          `yaml:"enabled"`
	Interval time.Duration `yaml:"interval"`
}

// TelemetryConfiguration holds settings for OpenTelemetry observability.
// Exporter endpoint, authentication headers, service name, and other resource
// attributes are configured via the standard OTEL_* environment variables
// (e.g. OTEL_EXPORTER_OTLP_ENDPOINT, OTEL_EXPORTER_OTLP_HEADERS, OTEL_SERVICE_NAME).
type TelemetryConfiguration struct {
	Enabled bool `yaml:"enabled"`
}

type RateLimitExceededAction string

const (
	RateLimitActionThrottle RateLimitExceededAction = "throttle"
	RateLimitActionBlock    RateLimitExceededAction = "block"
	RateLimitActionNone     RateLimitExceededAction = "none"
)

type RateLimitConfiguration struct {
	Enabled           bool                    `yaml:"enabled"`
	RequestsPerMinute int                     `yaml:"requestsPerMinute"`
	BurstSize         int                     `yaml:"burstSize"`
	WhitelistIPs      []string                `yaml:"whitelistIps"`
	OnLimitExceeded   RateLimitExceededAction `yaml:"onLimitExceeded"`
	BlockDuration     time.Duration           `yaml:"blockDuration,omitempty"`

	TrustedProxies []string `yaml:"-"` // Populated from top-level TrustedProxies for use in middleware.
}

// Configuration is the top-level application configuration.
type Configuration struct {
	ServerName        string                 `yaml:"serverName"`
	ServerURL         string                 `yaml:"serverUrl"`
	ServerHost        string                 `yaml:"serverHost"`
	ServerPort        int                    `yaml:"serverPort"`
	TrustedProxies    []string               `yaml:"trustedProxies"`
	MaxFileSizeMB     int                    `yaml:"maxFileSizeMb"`
	MinAgeDays        int                    `yaml:"minAgeDays"`
	MaxAgeDays        int                    `yaml:"maxAgeDays"`
	RandomIDLength    int                    `yaml:"randomIdLength"`
	DisableIndexPage  bool                   `yaml:"disableIndexPage"`
	DisableUploadPage bool                   `yaml:"disableUploadPage"`
	FaviconURL        string                 `yaml:"faviconUrl"`
	FaviconFormat     string                 `yaml:"faviconFormat"`
	Storage           StorageConfiguration   `yaml:"storage"`
	Database          DatabaseConfiguration  `yaml:"database"`
	RateLimit         RateLimitConfiguration `yaml:"rateLimit"`
	Logging           LoggingConfiguration   `yaml:"logging"`
	Cleanup           CleanupConfiguration   `yaml:"cleanup"`
	Telemetry         TelemetryConfiguration `yaml:"telemetry"`
	DebugMode         bool                   `yaml:"debug"`
}

// ToYaml returns the configuration marshalled as indented YAML.
func (c Configuration) ToYaml() []byte {
	result, err := yaml.Marshal(c)
	if err != nil {
		panic(err)
	}

	return result
}
