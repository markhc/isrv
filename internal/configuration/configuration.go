package configuration

import (
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/markhc/isrv/internal/models"
	"github.com/markhc/isrv/internal/utils"
	"go.uber.org/zap"
)

//go:embed default_config.yaml
var defaultConfig string

var config models.Configuration

// Get returns the current configuration.
func Get() *models.Configuration {
	return &config
}

// Load reads the application configuration. When configPath is non-empty it
// loads from that file; otherwise it searches known default locations. If no
// file is found, the built-in defaults are used. The debug flag forces debug
// logging and DebugMode on regardless of the source.
func Load(configPath string, debug bool) {
	if configPath != "" {
		loadFromFile(configPath, debug)
	} else if exists, path := configFileExists(defaultSearchPaths()); exists {
		loadFromFile(path, debug)
	} else {
		fmt.Println("no configuration file found, using built-in defaults") //nolint
		config = getDefaultConfig()

		if debug {
			config.Logging.Level = zap.DebugLevel
			config.DebugMode = true
		}
	}

	applyEnvOverrides()
	verifyConfiguration()
}

// applyEnvOverrides overrides config values with explicitly set ISRV_*
// environment variables. Unset variables leave the YAML-derived value
// untouched.
func applyEnvOverrides() {
	mapEnv := map[string]string{
		// Server and general settings.
		"ISRV_SERVER_NAME":            "ServerName",
		"ISRV_SERVER_URL":             "ServerURL",
		"ISRV_SERVER_HOST":            "ServerHost",
		"ISRV_SERVER_PORT":            "ServerPort",
		"ISRV_MAX_FILE_SIZE_MB":       "MaxFileSizeMB",
		"ISRV_MIN_AGE_DAYS":           "MinAgeDays",
		"ISRV_MAX_AGE_DAYS":           "MaxAgeDays",
		"ISRV_RANDOM_ID_LENGTH":       "RandomIDLength",
		"ISRV_KEEP_ORIGINAL_FILENAME": "KeepOriginalFilename",
		"ISRV_DISABLE_INDEX_PAGE":     "DisableIndexPage",
		"ISRV_DISABLE_UPLOAD_PAGE":    "DisableUploadPage",
		"ISRV_FAVICON_URL":            "FaviconURL",
		"ISRV_FAVICON_FORMAT":         "FaviconFormat",
		"ISRV_DEBUG":                  "DebugMode",

		// Validate IP addresses and trusted proxies.
		"ISRV_SECURITY_VALIDATE_IPS": "Security.ValidateIpAddresses",
		// Comma-separated list of trusted proxy IPs.
		"ISRV_SECURITY_TRUSTED_PROXIES": "Security.TrustedProxies",

		// Rate limiting.
		"ISRV_RATE_LIMIT_ENABLED":        "Security.RateLimit.Enabled",
		"ISRV_RATE_LIMIT_RPM":            "Security.RateLimit.RequestsPerMinute",
		"ISRV_RATE_LIMIT_BURST":          "Security.RateLimit.BurstSize",
		"ISRV_RATE_LIMIT_ACTION":         "Security.RateLimit.OnLimitExceeded",
		"ISRV_RATE_LIMIT_BLOCK_DURATION": "Security.RateLimit.BlockDuration",

		// Admin panel.
		"ISRV_ADMIN_USERNAME":                    "Admin.Username",
		"ISRV_ADMIN_PASSWORD":                    "Admin.Password",
		"ISRV_ADMIN_SESSION_SECRET":              "Admin.SessionSecret",
		"ISRV_ADMIN_SESSION_TTL":                 "Admin.SessionTTL",
		"ISRV_ADMIN_FAILED_LOGIN_LIMIT":          "Admin.FailedLoginLimit",
		"ISRV_ADMIN_FAILED_LOGIN_BLOCK_DURATION": "Admin.FailedLoginBlockDuration",

		// Storage backend.
		"ISRV_STORAGE_TYPE":        "Storage.Type",
		"ISRV_STORAGE_PATH":        "Storage.BasePath",
		"ISRV_STORAGE_ACCESS_KEY":  "Storage.AccessKey",
		"ISRV_STORAGE_SECRET_KEY":  "Storage.SecretKey",
		"ISRV_STORAGE_BUCKET_NAME": "Storage.BucketName",
		"ISRV_STORAGE_REGION":      "Storage.Region",
		"ISRV_STORAGE_ENDPOINT":    "Storage.Endpoint",

		// S3-specific behaviour.
		"ISRV_STORAGE_PROXY_DOWNLOADS":     "Storage.ProxyDownloads",
		"ISRV_STORAGE_UPLOAD_PART_SIZE_MB": "Storage.UploadPartSizeMB",
		"ISRV_STORAGE_UPLOAD_CONCURRENCY":  "Storage.UploadConcurrency",

		// Database backend.
		"ISRV_DATABASE_TYPE":      "Database.Type",
		"ISRV_DATABASE_DSN":       "Database.DSN",
		"ISRV_DATABASE_HOST":      "Database.Host",
		"ISRV_DATABASE_PORT":      "Database.Port",
		"ISRV_DATABASE_USER":      "Database.User",
		"ISRV_DATABASE_PASSWORD":  "Database.Password",
		"ISRV_DATABASE_DBNAME":    "Database.DBName",
		"ISRV_DATABASE_FILE_PATH": "Database.FilePath",

		// Logging.
		"ISRV_LOGGING_LEVEL":        "Logging.Level",
		"ISRV_LOGGING_LOG_TO_FILE":  "Logging.LogToFile",
		"ISRV_LOGGING_PATH":         "Logging.Path",
		"ISRV_LOGGING_LOG_IPS":      "Logging.LogIps",
		"ISRV_LOGGING_MAX_SIZE_MB":  "Logging.MaxSizeMB",
		"ISRV_LOGGING_MAX_BACKUPS":  "Logging.MaxBackups",
		"ISRV_LOGGING_MAX_AGE_DAYS": "Logging.MaxAgeDays",
		"ISRV_LOGGING_COMPRESS":     "Logging.Compress",

		// Cleanup routine.
		"ISRV_CLEANUP_ENABLED":  "Cleanup.Enabled",
		"ISRV_CLEANUP_INTERVAL": "Cleanup.Interval",

		// Encryption at rest.
		"ISRV_ENCRYPTION_ENABLED":       "Encryption.Enabled",
		"ISRV_ENCRYPTION_IDENTITY":      "Encryption.Identity",
		"ISRV_ENCRYPTION_IDENTITY_FILE": "Encryption.IdentityFile",
	}

	for envVar, configField := range mapEnv {
		if v, ok := os.LookupEnv(envVar); ok {
			// Special handling for trusted proxies, which is a list of strings.
			// We need to split the comma-separated string into a slice of strings.
			if envVar == "ISRV_SECURITY_TRUSTED_PROXIES" {
				config.Security.TrustedProxies = strings.Split(v, ",")
				continue
			}
			err := utils.SetStructField(&config, configField, v)
			if err != nil {
				panic(fmt.Errorf("failed to apply environment variable override for %s: %w", envVar, err))
			}
		}
	}
}

// defaultSearchPaths returns the ordered list of locations probed for a
// configuration file when none is supplied on the command line.
func defaultSearchPaths() []string {
	return []string{
		"./config.yaml",
		"./config/config.yaml",
		"/config/config.yaml",
		filepath.Join(os.Getenv("HOME"), ".config", "isrv", "config.yaml"),
		"/etc/isrv/config.yaml",
	}
}

// configFileExists returns the first existing path from the given list and
// whether such a path was found.
func configFileExists(paths []string) (bool, string) {
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return true, path
		}
	}

	return false, ""
}

func loadFromFile(path string, debug bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}

	// Seed with the embedded defaults so fields or whole sections absent from
	// the user's file fall back to real defaults instead of Go zero values.
	// YAML unmarshaling only overwrites keys present in the document.
	config = getDefaultConfig()

	err = yaml.Unmarshal(data, &config)
	if err != nil {
		panic(err)
	}

	if debug {
		config.Logging.Level = zap.DebugLevel
		config.DebugMode = true
	}
}

// verifyConfiguration validates the loaded configuration and panics with a
// descriptive message on invalid values. Some normalization (e.g. trailing
// path separator) is also applied here.
func verifyConfiguration() {
	if config.ServerPort < 1 || config.ServerPort > 65535 {
		panic("Invalid configuration: server_port must be between 1 and 65535")
	}

	if config.RandomIDLength < 4 {
		panic("Invalid configuration: random_id_length must be at least 4")
	}

	if config.MaxFileSizeMB < 1 {
		panic("Invalid configuration: max_file_size_mb must be at least 1")
	}

	verifyStorageConfig(&config.Storage)
	verifyCleanupConfig(&config.Cleanup)
	verifyRateLimitConfig(&config.Security.RateLimit)
	verifyAdminConfig(&config.Admin)
	verifyEncryptionConfig(&config.Encryption, &config.Storage)
}

// verifyEncryptionConfig validates the server-side encryption settings. It
// panics on unusable combinations and logs a warning when encrypted uploads
// will be force-proxied on S3.
func verifyEncryptionConfig(enc *models.EncryptionConfiguration, storageConfig *models.StorageConfiguration) {
	if enc.Identity != "" && enc.IdentityFile != "" {
		panic("Invalid configuration: set either encryption.identity or encryption.identityFile, not both")
	}

	if enc.Enabled && !enc.HasKey() {
		panic("Invalid configuration: encryption.enabled requires identity or identityFile; " +
			"generate one with 'isrv --gen-encryption-key'")
	}

	// Identity parsing/reading is deferred to encryption.NewManager: the file
	// may be a runtime-mounted secret not yet present at validation time.

	if enc.Enabled && storageConfig.Type == "s3" && !storageConfig.ProxyDownloads {
		fmt.Fprintln(os.Stderr, "warning: encryption is enabled with S3 storage and proxyDownloads "+
			"disabled; encrypted files will be proxied through the server regardless, "+
			"while plaintext files still use pre-signed redirects")
	}
}

// verifyAdminConfig normalizes admin panel settings. When the panel is enabled
// it generates a random session secret if none was provided and applies a
// default session TTL.
func verifyAdminConfig(adminConfig *models.AdminConfiguration) {
	if !adminConfig.Enabled() {
		return
	}

	if adminConfig.SessionSecret == "" {
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			panic(fmt.Errorf("failed to generate admin session secret: %w", err))
		}
		adminConfig.SessionSecret = hex.EncodeToString(secret)
	}

	if adminConfig.SessionTTL <= 0 {
		adminConfig.SessionTTL = 24 * time.Hour
	}

	if adminConfig.FailedLoginLimit <= 0 {
		adminConfig.FailedLoginLimit = 5
	}

	if adminConfig.FailedLoginBlockDuration <= 0 {
		adminConfig.FailedLoginBlockDuration = 12 * time.Hour
	}
}

func getDefaultConfig() models.Configuration {
	if defaultConfig == "" {
		panic("Default configuration is not embedded in the binary")
	}

	var defaultConfigStruct models.Configuration

	err := yaml.Unmarshal([]byte(defaultConfig), &defaultConfigStruct)
	if err != nil {
		panic(err)
	}

	return defaultConfigStruct
}

// GenerateDefaultConfig writes the embedded default configuration to the
// given path, creating any missing parent directories. It panics on failure.
func GenerateDefaultConfig(configPath string) {
	if defaultConfig == "" {
		panic("Default configuration is not embedded in the binary")
	}

	dir := filepath.Dir(configPath)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		err = os.MkdirAll(dir, 0o755)
		if err != nil {
			panic(err)
		}
	}

	err := os.WriteFile(configPath, []byte(defaultConfig), 0o644)
	if err != nil {
		panic(err)
	}
}

func verifyStorageConfig(storageConfig *models.StorageConfiguration) {
	if storageConfig.Type != "local" && storageConfig.Type != "s3" {
		panic("Invalid configuration: storage.type must be either 'local' or 's3'")
	}

	switch storageConfig.Type {
	case "local":
		if storageConfig.BasePath == "" {
			panic("Invalid configuration: base_path cannot be empty")
		}
		// Normalize the base path so it always ends with a separator.
		if !strings.HasSuffix(storageConfig.BasePath, string(os.PathSeparator)) {
			storageConfig.BasePath += string(os.PathSeparator)
		}
	case "s3":
		// Without a custom endpoint the SDK derives the AWS endpoint from the
		// region, so the region is mandatory.
		if storageConfig.Region == "" {
			if storageConfig.Endpoint == "" {
				panic("Invalid configuration: region must be provided for S3 storage")
			}
			storageConfig.Region = "auto"
		}

		if storageConfig.UploadPartSizeMB != 0 && storageConfig.UploadPartSizeMB < 5 {
			panic("Invalid configuration: storage.upload_part_size_mb must be at least 5 (S3 minimum part size)")
		}

		if storageConfig.UploadConcurrency < 0 {
			panic("Invalid configuration: storage.upload_concurrency cannot be negative")
		}
	}
}

func verifyCleanupConfig(cleanupConfig *models.CleanupConfiguration) {
	if cleanupConfig.Enabled {
		if cleanupConfig.Interval <= 0 {
			panic("Invalid configuration: cleanup.interval must be a positive duration")
		}
	}
}

func verifyRateLimitConfig(rateLimitConfig *models.RateLimitConfiguration) {
	if rateLimitConfig.Enabled {
		// Propagate trusted proxies so middleware can resolve client IPs correctly.
		rateLimitConfig.TrustedProxies = config.Security.TrustedProxies

		if rateLimitConfig.RequestsPerMinute <= 0 {
			panic("Invalid configuration: rate_limit.requests_per_minute must be a positive integer")
		}

		if rateLimitConfig.BurstSize < 0 {
			panic("Invalid configuration: rate_limit.burst_size cannot be negative")
		}

		possibleActions := []string{
			string(models.RateLimitActionThrottle),
			string(models.RateLimitActionBlock),
			string(models.RateLimitActionNone),
		}

		if !slices.Contains(possibleActions, string(rateLimitConfig.OnLimitExceeded)) {
			panic("Invalid configuration: rate_limit.on_limit_exceeded must be one of: " + strings.Join(possibleActions, ", "))
		}

		if rateLimitConfig.OnLimitExceeded == models.RateLimitActionBlock && rateLimitConfig.BlockDuration <= 0 {
			panic("Invalid configuration: rate_limit.block_duration must be a positive duration")
		}
	}
}
