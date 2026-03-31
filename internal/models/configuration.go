package models

import (
	"github.com/goccy/go-yaml"
	"go.uber.org/zap/zapcore"
)

// StorageConfiguration holds settings for the storage backend.
type StorageConfiguration struct {
	Type string `yaml:"type"` // "local" or "s3"
	// Base directory for local storage
	// or base path/prefix for S3 storage
	BasePath string `yaml:"base_path"`

	// Object storage settings
	AccessKey  string `yaml:"access_key"`
	SecretKey  string `yaml:"secret_key"`
	BucketName string `yaml:"bucket_name"`
	Region     string `yaml:"region"`
	Endpoint   string `yaml:"endpoint"`
}

// DatabaseConfiguration holds settings for the database backend.
type DatabaseConfiguration struct {
	Type     string `yaml:"type"`      // "sqlite" and "postgres" supported
	DSN      string `yaml:"dsn"`       // Data Source Name. If provided, overrides other settings
	Host     string `yaml:"host"`      // For networked databases
	Port     int    `yaml:"port"`      // For networked databases
	User     string `yaml:"user"`      // For networked databases
	Password string `yaml:"password"`  // For networked databases
	DBName   string `yaml:"db_name"`   // Database name
	FilePath string `yaml:"file_path"` // For file-based databases
}

// LoggingConfiguration holds settings for structured logging.
type LoggingConfiguration struct {
	LogUploads bool          `yaml:"log_uploads"`
	LogIps     bool          `yaml:"log_ips"`
	Level      zapcore.Level `yaml:"level"`
	Path       string        `yaml:"path"`
}

// CleanupConfiguration holds settings for the background file cleanup service.
type CleanupConfiguration struct {
	Enabled  bool   `yaml:"enabled"`
	Interval string `yaml:"interval"`
}

// Configuration is the top-level application configuration.
type Configuration struct {
	ServerName        string                `yaml:"server_name"`
	ServerURL         string                `yaml:"server_url"`
	ServerHost        string                `yaml:"server_host"`
	ServerPort        int                   `yaml:"server_port"`
	MaxFileSizeMB     int                   `yaml:"max_file_size_mb"`
	MinAgeDays        int                   `yaml:"min_age_days"`
	MaxAgeDays        int                   `yaml:"max_age_days"`
	RandomIDLength    int                   `yaml:"random_id_length"`
	DisableIndexPage  bool                  `yaml:"disable_index_page"`
	DisableUploadPage bool                  `yaml:"disable_upload_page"`
	FaviconURL        string                `yaml:"favicon_url"`
	FaviconFormat     string                `yaml:"favicon_format"`
	Storage           StorageConfiguration  `yaml:"storage"`
	Database          DatabaseConfiguration `yaml:"database"`
	Logging           LoggingConfiguration  `yaml:"logs"`
	Cleanup           CleanupConfiguration  `yaml:"cleanup"`
	DebugMode         bool                  `yaml:"debug"`
}

// ToYaml returns an indented Yaml representation
func (c Configuration) ToYaml() []byte {
	result, err := yaml.Marshal(c)

	if err != nil {
		panic(err)
	}

	return result
}
