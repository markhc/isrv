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

	// ProxyDownloads streams S3 objects through the server instead of
	// redirecting clients to a pre-signed URL.
	ProxyDownloads bool `yaml:"proxyDownloads"`
	// UploadPartSizeMB is the part size in MiB for multipart S3 uploads.
	// Zero uses the SDK default. S3 requires parts of at least 5 MiB.
	UploadPartSizeMB int `yaml:"uploadPartSizeMb"`
	// UploadConcurrency is the number of parts uploaded in parallel for
	// multipart S3 uploads. Zero uses the SDK default.
	UploadConcurrency int `yaml:"uploadConcurrency"`
}

// AdminConfiguration holds settings for the single-administrator admin panel.
type AdminConfiguration struct {
	Username                 string        `yaml:"username"`
	Password                 string        `yaml:"password"`
	SessionSecret            string        `yaml:"sessionSecret"`
	SessionTTL               time.Duration `yaml:"sessionTtl"`
	FailedLoginLimit         int           `yaml:"failedLoginLimit"`
	FailedLoginBlockDuration time.Duration `yaml:"failedLoginBlockDuration"`
}

// Enabled reports whether the admin panel is configured and should be served.
func (a AdminConfiguration) Enabled() bool {
	return a.Username != "" && a.Password != ""
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
type LoggingConfiguration struct {
	LogToFile bool `yaml:"logToFile"`
	LogIps    bool `yaml:"logIps"`
	// Anonymize enables minimal-logging mode: successful access-log lines are
	// dropped (only warnings and errors are kept for maintenance) and any
	// identifying field (IP, user agent, path, filename, host) is omitted.
	Anonymize bool          `yaml:"anonymize"`
	Level     zapcore.Level `yaml:"level"`
	Path      string        `yaml:"path"`

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

// EncryptionConfiguration holds settings for server-side encryption at rest.
type EncryptionConfiguration struct {
	// Enabled encrypts new uploads. Decryption of existing files only
	// requires an identity to be configured, regardless of this flag.
	Enabled bool `yaml:"enabled"`
	// Identity is the age X25519 identity (AGE-SECRET-KEY-1...).
	Identity string `yaml:"identity"`
	// IdentityFile is a path to a file containing the identity, in
	// age-keygen output format.
	IdentityFile string `yaml:"identityFile"`
}

// HasKey reports whether any identity source is configured.
func (e EncryptionConfiguration) HasKey() bool {
	return e.Identity != "" || e.IdentityFile != ""
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

type SecurityConfiguration struct {
	ValidateIpAddresses bool                   `yaml:"validateIpAddresses"`
	TrustedProxies      []string               `yaml:"trustedProxies"`
	RateLimit           RateLimitConfiguration `yaml:"rateLimit"`
}

// Configuration is the top-level application configuration.
type Configuration struct {
	ServerName           string                  `yaml:"serverName"`
	ServerURL            string                  `yaml:"serverUrl"`
	ServerHost           string                  `yaml:"serverHost"`
	ServerPort           int                     `yaml:"serverPort"`
	MaxFileSizeMB        int                     `yaml:"maxFileSizeMb"`
	MinAgeDays           int                     `yaml:"minAgeDays"`
	MaxAgeDays           int                     `yaml:"maxAgeDays"`
	RandomIDLength       int                     `yaml:"randomIdLength"`
	KeepOriginalFilename bool                    `yaml:"keepOriginalFilename"`
	DisableIndexPage     bool                    `yaml:"disableIndexPage"`
	DisableUploadPage    bool                    `yaml:"disableUploadPage"`
	FaviconURL           string                  `yaml:"faviconUrl"`
	FaviconFormat        string                  `yaml:"faviconFormat"`
	Security             SecurityConfiguration   `yaml:"security"`
	Admin                AdminConfiguration      `yaml:"admin"`
	Storage              StorageConfiguration    `yaml:"storage"`
	Database             DatabaseConfiguration   `yaml:"database"`
	Logging              LoggingConfiguration    `yaml:"logging"`
	Cleanup              CleanupConfiguration    `yaml:"cleanup"`
	Encryption           EncryptionConfiguration `yaml:"encryption"`
	DebugMode            bool                    `yaml:"debug"`
}

// FrontendConfig holds the subset of Configuration that is safe to expose to
// the browser. It is serialised as JSON and injected into index.html at
// startup.
type FrontendConfig struct {
	ServerName        string `json:"serverName"`
	DisableUploadPage bool   `json:"disableUploadPage"`
	MaxFileSizeMB     int    `json:"maxFileSizeMb"`
	MinAgeDays        int    `json:"minAgeDays"`
	MaxAgeDays        int    `json:"maxAgeDays"`
	Version           string `json:"version"`
}

// ToYaml returns the configuration marshalled as indented YAML.
func (c Configuration) ToYaml() []byte {
	result, err := yaml.Marshal(c)
	if err != nil {
		panic(err)
	}

	return result
}
