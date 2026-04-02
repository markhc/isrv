package configuration

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/markhc/isrv/internal/models"
	"go.uber.org/zap"
)

//go:embed default_config.yaml
var defaultConfig string

var config models.Configuration

// Get returns the current configuration.
func Get() *models.Configuration {
	return &config
}

// Load reads the application configuration.
// If configPath is non-empty it loads from that file; otherwise it searches
// known default locations. If no file is found, built-in defaults are used.
func Load(configPath string, debug bool) {
	if configPath != "" {
		loadFromFile(configPath, debug)
	} else if exists, path := configFileExists(defaultSearchPaths()); exists {
		loadFromFile(path, debug)
	} else {
		fmt.Println("no configuration file found, using built-in defaults") //nolint
		// No configuration file found, use defaults
		config = getDefaultConfig()

		if debug {
			config.Logging.Level = zap.DebugLevel
			config.DebugMode = true
		}
	}

	applyEnvOverrides()
	verifyConfiguration()
}

// applyEnvOverrides overrides config values with any explicitly set ISRV_* environment variables.
// Uses os.LookupEnv so that only variables present in the environment take effect;
// unset variables do not override YAML-derived values.
//
//nolint:cyclop
func applyEnvOverrides() {
	if v, ok := os.LookupEnv("ISRV_SERVER_NAME"); ok {
		config.ServerName = v
	}
	if v, ok := os.LookupEnv("ISRV_SERVER_URL"); ok {
		config.ServerURL = v
	}
	if v, ok := os.LookupEnv("ISRV_SERVER_HOST"); ok {
		config.ServerHost = v
	}
	if v, ok := os.LookupEnv("ISRV_SERVER_PORT"); ok {
		if port, err := strconv.Atoi(v); err == nil {
			config.ServerPort = port
		}
	}
	if v, ok := os.LookupEnv("ISRV_STORAGE_PATH"); ok {
		config.Storage.BasePath = v
	}
	if v, ok := os.LookupEnv("ISRV_LOGGING_FILE_ENABLED"); ok {
		if enabled, err := strconv.ParseBool(v); err == nil {
			config.Logging.LogToFile = enabled
		}
	}
	if v, ok := os.LookupEnv("ISRV_LOGGING_PATH"); ok {
		config.Logging.Path = v
	}
	if v, ok := os.LookupEnv("ISRV_LOGGING_IPS_ENABLED"); ok {
		if enabled, err := strconv.ParseBool(v); err == nil {
			config.Logging.LogIps = enabled
		}
	}
	if v, ok := os.LookupEnv("ISRV_LOGGING_UPLOADS_ENABLED"); ok {
		if enabled, err := strconv.ParseBool(v); err == nil {
			config.Logging.LogUploads = enabled
		}
	}
	if v, ok := os.LookupEnv("ISRV_RANDOM_ID_LENGTH"); ok {
		if n, err := strconv.Atoi(v); err == nil {
			config.RandomIDLength = n
		}
	}
	if v, ok := os.LookupEnv("ISRV_MAX_FILE_SIZE_MB"); ok {
		if n, err := strconv.Atoi(v); err == nil {
			config.MaxFileSizeMB = n
		}
	}
	if v, ok := os.LookupEnv("ISRV_CLEANUP_ENABLED"); ok {
		if enabled, err := strconv.ParseBool(v); err == nil {
			config.Cleanup.Enabled = enabled
		}
	}
	if v, ok := os.LookupEnv("ISRV_CLEANUP_INTERVAL"); ok {
		if duration, err := time.ParseDuration(v); err == nil {
			config.Cleanup.Interval = duration
		}
	}
}

// defaultSearchPaths returns the ordered list of paths to probe for a config file.
func defaultSearchPaths() []string {
	return []string{
		"./config.yaml",
		"./config/config.yaml",
		"/config/config.yaml",
		filepath.Join(os.Getenv("HOME"), ".config", "isrv", "config.yaml"),
		"/etc/isrv/config.yaml",
	}
}

// configFileExists checks which of the given paths exists and returns the first match.
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

	err = yaml.Unmarshal(data, &config)
	if err != nil {
		panic(err)
	}

	if debug {
		config.Logging.Level = zap.DebugLevel
		config.DebugMode = true
	}
}

// Verify that the loaded configuration is valid and attempt to fix any issues.
//
//nolint:cyclop
func verifyConfiguration() {
	if config.Storage.Type != "local" && config.Storage.Type != "s3" {
		panic("Invalid configuration: storage.type must be either 'local' or 's3'")
	}

	switch config.Storage.Type {
	case "local":
		if config.Storage.BasePath == "" {
			panic("Invalid configuration: base_path cannot be empty")
		}
		// Ensure data directory ends with a slash
		if !strings.HasSuffix(config.Storage.BasePath, string(os.PathSeparator)) {
			config.Storage.BasePath += string(os.PathSeparator)
		}
	case "s3":
		if config.Storage.Region == "" {
			panic("Invalid configuration: region must be provided for S3 storage")
		}

		if config.Storage.Endpoint == "" {
			// Set default endpoint based on region if not provided
			if config.Storage.Region != "" {
				config.Storage.Endpoint = fmt.Sprintf("https://s3.%s.amazonaws.com", config.Storage.Region)
			}
		}
	}

	if config.ServerPort < 1 || config.ServerPort > 65535 {
		panic("Invalid configuration: server_port must be between 1 and 65535")
	}

	if config.RandomIDLength < 4 {
		panic("Invalid configuration: random_id_length must be at least 4")
	}

	if config.MaxFileSizeMB < 1 {
		panic("Invalid configuration: max_file_size_mb must be at least 1")
	}

	if config.Cleanup.Enabled {
		if config.Cleanup.Interval <= 0 {
			panic("Invalid configuration: cleanup.interval must be a positive duration")
		}
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

// GenerateDefaultConfig generates a default configuration file at the specified path.
func GenerateDefaultConfig(configPath string) {
	// Check if the embedded default config is available
	if defaultConfig == "" {
		panic("Default configuration is not embedded in the binary")
	}

	// Check that the directory exists, if not attempt to create it
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
