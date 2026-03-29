package configuration

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/markhc/isrv/internal/environment"
	"github.com/markhc/isrv/internal/models"
	"go.uber.org/zap"
)

//go:embed default_config.yaml
var defaultConfig string

var config models.Configuration

// Get returns the current configuration
func Get() *models.Configuration {
	return &config
}

// Loads the app configuration
// If a path is provided, it attempts to load from that path,
// otherwise it checks known locations for a configuration file
func Load(configPath string, debug bool) {
	if configPath != "" {
		loadFromFile(configPath, debug)
	} else if exists, path := configFileExists(); exists {
		loadFromFile(path, debug)
	} else {
		fmt.Println("Using default configuration values...")
		// No configuration file found, use defaults
		config = getDefaultConfig()

		if debug {
			config.Logging.Level = zap.DebugLevel
			config.DebugMode = true
		}
	}

	verifyConfiguration()
}

// Check known configuration file paths to see if any exist
func configFileExists() (bool, string) {
	env := environment.ParseEnv()

	if env.ConfigPathIsSet {
		_, err := os.Stat(env.ConfigPath)

		if !os.IsNotExist(err) {
			return true, env.ConfigPath
		}

		fmt.Println("Configuration path is set but the file does not exist:", env.ConfigPath)
	} else {
		knownPaths := []string{
			"./config.yaml",
			"./config/config.yaml",
			"/config/config.yaml",
			filepath.Join(os.Getenv("HOME"), ".config", "isrv", "config.yaml"),
			"/etc/isrv/config.yaml",
		}
		for _, path := range knownPaths {
			_, err := os.Stat(path)

			if !os.IsNotExist(err) {
				return true, path
			}
		}
	}

	fmt.Println("No configuration file found in known locations")

	return false, ""
}

func loadFromFile(path string, debug bool) {
	data, err := os.ReadFile(path)

	if err != nil {
		fmt.Println("Failed to read configuration file:", err)
		panic(err)
	}

	err = yaml.Unmarshal(data, &config)

	if err != nil {
		fmt.Println("Failed to parse configuration file:", err)
		panic(err)
	}

	if debug {
		config.Logging.Level = zap.DebugLevel
		config.DebugMode = true
	}
}

// Verify that the loaded configuration is valid and attempt to fix any issues
func verifyConfiguration() {
	if config.Storage.Type != "local" && config.Storage.Type != "s3" {
		panic("Invalid configuration: storage.type must be either 'local' or 's3'")
	}

	if config.Storage.Type == "local" {
		if config.Storage.BasePath == "" {
			panic("Invalid configuration: base_path cannot be empty")
		}
		// Ensure data directory ends with a slash
		if !strings.HasSuffix(config.Storage.BasePath, string(os.PathSeparator)) {
			config.Storage.BasePath += string(os.PathSeparator)
		}
	}
}

func getDefaultConfig() models.Configuration {
	if defaultConfig != "" {
		var defaultConfigStruct models.Configuration

		err := yaml.Unmarshal([]byte(defaultConfig), &defaultConfigStruct)

		if err != nil {
			fmt.Println("Failed to parse default configuration:", err)
			panic(err)
		}

		return defaultConfigStruct
	}

	return models.Configuration{
		ServerURL:         "http://localhost:8080",
		ServerHost:        "0.0.0.0",
		ServerPort:        8080,
		MaxFileSizeMB:     512,
		MinAgeDays:        30,
		MaxAgeDays:        365,
		RandomIDLength:    20,
		DisableIndexPage:  false,
		DisableUploadPage: true,
		FaviconURL:        "",
		FaviconFormat:     "png",
		Storage: models.StorageConfiguration{
			Type:     "local",
			BasePath: "./data/",
		},
		Database: models.DatabaseConfiguration{
			Type: "sqlite",
			DSN:  "file:isrv.db?cache=shared&mode=rwc",
		},
		Logging: models.LoggingConfiguration{
			LogUploads: true,
			LogIps:     true,
			Level:      zap.InfoLevel,
			Path:       "./isrv.log",
		},
	}
}

// GenerateDefaultConfig generates a default configuration file at the specified path
func GenerateDefaultConfig(configPath string) {
	fmt.Println("Generating default configuration file", configPath)
	// Check if the embedded default config is available,
	// if it is, we write it as is, otherwise we generate a new default config struct and marshal it to YAML
	// This allows us to maintain comments and formatting in the default config file, which can be helpful for users
	var contents string
	if defaultConfig != "" {
		contents = defaultConfig
	} else {
		defaultConfigStruct := getDefaultConfig()

		data, err := yaml.Marshal(&defaultConfigStruct)

		if err != nil {
			fmt.Println("Failed to generate default configuration:", err)
			panic(err)
		}

		contents = string(data)
	}

	// Check that the directory exists, if not attempt to create it
	dir := filepath.Dir(configPath)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		err = os.MkdirAll(dir, 0755)
		if err != nil {
			fmt.Println("Failed to create configuration directory:", err)
			panic(err)
		}
	}

	err := os.WriteFile(configPath, []byte(contents), 0644)

	if err != nil {
		fmt.Println("Failed to write default configuration file:", err)
		panic(err)
	}

	fmt.Println("Default configuration file generated at:", configPath)
}
