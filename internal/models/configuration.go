package models

import (
	"time"

	"github.com/goccy/go-yaml"
	"go.uber.org/zap/zapcore"
)

// StorageConfiguration holds settings for the storage backend.
type StorageConfiguration struct {
	Type string `yaml:"type"` // "local" or "s3"
	// Base directory for local storage
	// or base path/prefix for S3 storage
	BasePath string `yaml:"basePath"`

	// Object storage settings
	AccessKey  string `yaml:"accessKey"`
	SecretKey  string `yaml:"secretKey"`
	BucketName string `yaml:"bucketName"`
	Region     string `yaml:"region"`
	Endpoint   string `yaml:"endpoint"`
}

// DatabaseConfiguration holds settings for the database backend.
type DatabaseConfiguration struct {
	Type     string `yaml:"type"`     // "sqlite" and "postgres" supported
	DSN      string `yaml:"dsn"`      // Data Source Name. If provided, overrides other settings
	Host     string `yaml:"host"`     // For networked databases
	Port     int    `yaml:"port"`     // For networked databases
	User     string `yaml:"user"`     // For networked databases
	Password string `yaml:"password"` // For networked databases
	DBName   string `yaml:"dbName"`   // Database name
	FilePath string `yaml:"filePath"` // For file-based databases
}

// LoggingConfiguration holds settings for structured logging.
type LoggingConfiguration struct {
	LogToFile  bool          `yaml:"logToFile"`
	LogUploads bool          `yaml:"logUploads"`
	LogIps     bool          `yaml:"logIps"`
	Level      zapcore.Level `yaml:"level"`
	Path       string        `yaml:"path"`
}

// CleanupConfiguration holds settings for the background file cleanup service.
type CleanupConfiguration struct {
	Enabled  bool          `yaml:"enabled"`
	Interval time.Duration `yaml:"interval"`
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
	BlockDuration     time.Duration           `yaml:"blockDuration,omitempty"` // Only used if action is "block"
}

// Configuration is the top-level application configuration.
type Configuration struct {
	ServerName        string                 `yaml:"serverName"`
	ServerURL         string                 `yaml:"serverUrl"`
	ServerHost        string                 `yaml:"serverHost"`
	ServerPort        int                    `yaml:"serverPort"`
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
	DebugMode         bool                   `yaml:"debug"`
}

// ToYaml returns an indented Yaml representation.
func (c Configuration) ToYaml() []byte {
	result, err := yaml.Marshal(c)
	if err != nil {
		panic(err)
	}

	return result
}
