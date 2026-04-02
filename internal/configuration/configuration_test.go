package configuration

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/markhc/isrv/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestGet(t *testing.T) {
	// Save current config
	originalConfig := config

	// Set a test config
	testConfig := models.Configuration{
		ServerName: "test-server",
		ServerPort: 9999,
	}
	config = testConfig

	// Test Get returns pointer to current config
	result := Get()
	assert.Equal(t, &testConfig, result)
	assert.Equal(t, "test-server", result.ServerName)
	assert.Equal(t, 9999, result.ServerPort)

	// Restore original config
	config = originalConfig
}

func TestLoad_ExplicitPath(t *testing.T) {
	// Create temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "test-config.yaml")

	configContent := `
serverName: "test-server"
serverPort: 9000
randomIdLength: 12
maxFileSizeMb: 512
storage:
  type: "local" 
  basePath: "/test/path/"
`
	require.NoError(t, os.WriteFile(configPath, []byte(configContent), 0644))

	// Save original config
	originalConfig := config

	// Test loading from explicit path
	Load(configPath, false)

	assert.Equal(t, "test-server", config.ServerName)
	assert.Equal(t, 9000, config.ServerPort)
	assert.Equal(t, "local", config.Storage.Type)
	assert.Equal(t, "/test/path/", config.Storage.BasePath)
	assert.Equal(t, zap.InfoLevel, config.Logging.Level)
	assert.False(t, config.DebugMode)

	// Restore original config
	config = originalConfig
}

func TestLoad_ExplicitPath_DebugMode(t *testing.T) {
	// Create temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "test-config.yaml")

	configContent := `
serverName: "test-server"
serverPort: 9000
randomIdLength: 12
maxFileSizeMb: 512
storage:
  type: "local" 
  basePath: "./data/"
`
	require.NoError(t, os.WriteFile(configPath, []byte(configContent), 0644))

	// Save original config
	originalConfig := config

	// Test loading with debug mode
	Load(configPath, true)

	assert.Equal(t, "test-server", config.ServerName)
	assert.Equal(t, zap.DebugLevel, config.Logging.Level)
	assert.True(t, config.DebugMode)

	// Restore original config
	config = originalConfig
}

func TestLoad_DefaultLocations(t *testing.T) {
	// Save original config and working directory
	originalConfig := config
	originalWd, _ := os.Getwd()

	// Create temporary directory and change to it
	tmpDir := t.TempDir()
	require.NoError(t, os.Chdir(tmpDir))
	t.Setenv("HOME", tmpDir) // prevent ~/.config/isrv/config.yaml from matching

	// Create config in default location
	configContent := `
serverName: "default-location-server"
serverPort: 8888
randomIdLength: 12
maxFileSizeMb: 512
storage:
  type: "local"
  basePath: "./data/"
`
	require.NoError(t, os.WriteFile("config.yaml", []byte(configContent), 0644))

	// Test loading from default location (empty string triggers search)
	Load("", false)

	assert.Equal(t, "default-location-server", config.ServerName)
	assert.Equal(t, 8888, config.ServerPort)

	// Restore original state
	config = originalConfig
	require.NoError(t, os.Chdir(originalWd))
}

func TestLoad_NoConfigFile_UsesDefaults(t *testing.T) {
	// Save original config and working directory
	originalConfig := config
	originalWd, _ := os.Getwd()

	// Create temporary directory with no config files
	tmpDir := t.TempDir()
	require.NoError(t, os.Chdir(tmpDir))
	t.Setenv("HOME", tmpDir) // prevent ~/.config/isrv/config.yaml from matching

	// Test loading with no config file available
	Load("", false)

	// Should use default values from embedded default_config.yaml
	assert.Equal(t, "http://localhost:8080", config.ServerURL)
	assert.Equal(t, 8080, config.ServerPort)
	assert.Equal(t, "local", config.Storage.Type)
	assert.Equal(t, "./upload_data/", config.Storage.BasePath)

	// Restore original state
	config = originalConfig
	require.NoError(t, os.Chdir(originalWd))
}

func TestLoad_NoConfigFile_DebugMode(t *testing.T) {
	// Save original config and working directory
	originalConfig := config
	originalWd, _ := os.Getwd()

	// Create temporary directory with no config files
	tmpDir := t.TempDir()
	require.NoError(t, os.Chdir(tmpDir))
	t.Setenv("HOME", tmpDir) // prevent ~/.config/isrv/config.yaml from matching

	// Test loading with debug mode and no config file
	Load("", true)

	assert.Equal(t, zap.DebugLevel, config.Logging.Level)
	assert.True(t, config.DebugMode)

	// Restore original state
	config = originalConfig
	require.NoError(t, os.Chdir(originalWd))
}

func TestApplyEnvOverrides(t *testing.T) {
	tests := []struct {
		name     string
		envVars  map[string]string
		expected func(*testing.T, models.Configuration)
	}{
		{
			name: "server configuration overrides",
			envVars: map[string]string{
				"ISRV_SERVER_NAME": "env-server",
				"ISRV_SERVER_URL":  "https://example.com",
				"ISRV_SERVER_HOST": "127.0.0.1",
				"ISRV_SERVER_PORT": "9090",
			},
			expected: func(t *testing.T, cfg models.Configuration) {
				assert.Equal(t, "env-server", cfg.ServerName)
				assert.Equal(t, "https://example.com", cfg.ServerURL)
				assert.Equal(t, "127.0.0.1", cfg.ServerHost)
				assert.Equal(t, 9090, cfg.ServerPort)
			},
		},
		{
			name: "storage configuration overrides",
			envVars: map[string]string{
				"ISRV_STORAGE_PATH": "/env/storage/path/",
			},
			expected: func(t *testing.T, cfg models.Configuration) {
				assert.Equal(t, "/env/storage/path/", cfg.Storage.BasePath)
			},
		},
		{
			name: "logging configuration overrides",
			envVars: map[string]string{
				"ISRV_LOGGING_FILE_ENABLED":    "true",
				"ISRV_LOGGING_PATH":            "/env/log/path",
				"ISRV_LOGGING_IPS_ENABLED":     "false",
				"ISRV_LOGGING_UPLOADS_ENABLED": "false",
			},
			expected: func(t *testing.T, cfg models.Configuration) {
				assert.True(t, cfg.Logging.LogToFile)
				assert.Equal(t, "/env/log/path", cfg.Logging.Path)
				assert.False(t, cfg.Logging.LogIps)
				assert.False(t, cfg.Logging.LogUploads)
			},
		},
		{
			name: "file configuration overrides",
			envVars: map[string]string{
				"ISRV_RANDOM_ID_LENGTH": "16",
				"ISRV_MAX_FILE_SIZE_MB": "1024",
			},
			expected: func(t *testing.T, cfg models.Configuration) {
				assert.Equal(t, 16, cfg.RandomIDLength)
				assert.Equal(t, 1024, cfg.MaxFileSizeMB)
			},
		},
		{
			name: "cleanup configuration overrides",
			envVars: map[string]string{
				"ISRV_CLEANUP_ENABLED":  "false",
				"ISRV_CLEANUP_INTERVAL": "5m",
			},
			expected: func(t *testing.T, cfg models.Configuration) {
				assert.False(t, cfg.Cleanup.Enabled)
				assert.Equal(t, 5*time.Minute, cfg.Cleanup.Interval)
			},
		},
		{
			name: "invalid values are ignored",
			envVars: map[string]string{
				"ISRV_SERVER_PORT":             "invalid",
				"ISRV_LOGGING_FILE_ENABLED":    "invalid",
				"ISRV_LOGGING_IPS_ENABLED":     "invalid",
				"ISRV_LOGGING_UPLOADS_ENABLED": "invalid",
				"ISRV_RANDOM_ID_LENGTH":        "invalid",
				"ISRV_MAX_FILE_SIZE_MB":        "invalid",
				"ISRV_CLEANUP_ENABLED":         "invalid",
			},
			expected: func(t *testing.T, cfg models.Configuration) {
				// Values should remain at their default/original values
				// since invalid values are ignored
				assert.Equal(t, 8080, cfg.ServerPort) // default
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save original config and environment
			originalConfig := config

			// Set up test environment
			for key, value := range tt.envVars {
				t.Setenv(key, value)
			}

			// Initialize with defaults
			config = getDefaultConfig()

			// Apply environment overrides
			applyEnvOverrides()

			// Verify expected changes
			tt.expected(t, config)

			// Restore original config
			config = originalConfig
		})
	}
}

func TestConfigFileExists(t *testing.T) {
	tmpDir := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "config"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.yaml"), []byte("test"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config", "config.yaml"), []byte("test"), 0644))

	tests := []struct {
		name           string
		paths          []string
		expectedExists bool
		expectedPath   string
	}{
		{
			name:           "config.yaml in current directory",
			paths:          []string{filepath.Join(tmpDir, "config.yaml")},
			expectedExists: true,
			expectedPath:   filepath.Join(tmpDir, "config.yaml"),
		},
		{
			name:           "config in config subdirectory",
			paths:          []string{filepath.Join(tmpDir, "config", "config.yaml")},
			expectedExists: true,
			expectedPath:   filepath.Join(tmpDir, "config", "config.yaml"),
		},
		{
			name:           "first match wins",
			paths:          []string{filepath.Join(tmpDir, "config.yaml"), filepath.Join(tmpDir, "config", "config.yaml")},
			expectedExists: true,
			expectedPath:   filepath.Join(tmpDir, "config.yaml"),
		},
		{
			name:           "no config file exists",
			paths:          []string{"/nonexistent/a.yaml", "/nonexistent/b.yaml"},
			expectedExists: false,
			expectedPath:   "",
		},
		{
			name:           "empty path list",
			paths:          []string{},
			expectedExists: false,
			expectedPath:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exists, path := configFileExists(tt.paths)

			assert.Equal(t, tt.expectedExists, exists)
			assert.Equal(t, tt.expectedPath, path)
		})
	}
}

func TestVerifyConfiguration(t *testing.T) {
	tests := []struct {
		name         string
		setupFunc    func() models.Configuration
		expectPanic  bool
		panicMessage string
		postCheck    func(*testing.T, models.Configuration)
	}{
		{
			name: "valid local storage configuration",
			setupFunc: func() models.Configuration {
				cfg := getDefaultConfig()
				cfg.Storage.Type = "local"
				cfg.Storage.BasePath = "/valid/path"
				return cfg
			},
			expectPanic: false,
		},
		{
			name: "valid s3 configuration with region",
			setupFunc: func() models.Configuration {
				cfg := getDefaultConfig()
				cfg.Storage.Type = "s3"
				cfg.Storage.Region = "us-east-1"
				return cfg
			},
			expectPanic: false,
		},
		{
			name: "invalid storage type",
			setupFunc: func() models.Configuration {
				cfg := getDefaultConfig()
				cfg.Storage.Type = "invalid"
				return cfg
			},
			expectPanic:  true,
			panicMessage: "Invalid configuration: storage.type must be either 'local' or 's3'",
		},
		{
			name: "local storage with empty base path",
			setupFunc: func() models.Configuration {
				cfg := getDefaultConfig()
				cfg.Storage.Type = "local"
				cfg.Storage.BasePath = ""
				return cfg
			},
			expectPanic:  true,
			panicMessage: "Invalid configuration: base_path cannot be empty",
		},
		{
			name: "s3 storage without region",
			setupFunc: func() models.Configuration {
				cfg := getDefaultConfig()
				cfg.Storage.Type = "s3"
				cfg.Storage.Region = ""
				return cfg
			},
			expectPanic:  true,
			panicMessage: "Invalid configuration: region must be provided for S3 storage",
		},
		{
			name: "invalid server port - too low",
			setupFunc: func() models.Configuration {
				cfg := getDefaultConfig()
				cfg.ServerPort = 0
				return cfg
			},
			expectPanic:  true,
			panicMessage: "Invalid configuration: server_port must be between 1 and 65535",
		},
		{
			name: "invalid server port - too high",
			setupFunc: func() models.Configuration {
				cfg := getDefaultConfig()
				cfg.ServerPort = 65536
				return cfg
			},
			expectPanic:  true,
			panicMessage: "Invalid configuration: server_port must be between 1 and 65535",
		},
		{
			name: "invalid random ID length",
			setupFunc: func() models.Configuration {
				cfg := getDefaultConfig()
				cfg.RandomIDLength = 3
				return cfg
			},
			expectPanic:  true,
			panicMessage: "Invalid configuration: random_id_length must be at least 4",
		},
		{
			name: "invalid max file size",
			setupFunc: func() models.Configuration {
				cfg := getDefaultConfig()
				cfg.MaxFileSizeMB = 0
				return cfg
			},
			expectPanic:  true,
			panicMessage: "Invalid configuration: max_file_size_mb must be at least 1",
		},
		{
			name: "invalid cleanup interval",
			setupFunc: func() models.Configuration {
				cfg := getDefaultConfig()
				cfg.Cleanup.Enabled = true
				cfg.Cleanup.Interval = time.Duration(-1)
				return cfg
			},
			expectPanic:  true,
			panicMessage: "Invalid configuration: cleanup.interval must be a positive duration",
		},
		{
			name: "local storage path gets trailing separator added",
			setupFunc: func() models.Configuration {
				cfg := getDefaultConfig()
				cfg.Storage.Type = "local"
				cfg.Storage.BasePath = "/no/trailing/slash"
				return cfg
			},
			expectPanic: false,
			postCheck: func(t *testing.T, cfg models.Configuration) {
				assert.Equal(t, "/no/trailing/slash"+string(os.PathSeparator), cfg.Storage.BasePath)
			},
		},
		{
			name: "s3 storage gets default endpoint when region provided",
			setupFunc: func() models.Configuration {
				cfg := getDefaultConfig()
				cfg.Storage.Type = "s3"
				cfg.Storage.Region = "us-west-2"
				cfg.Storage.Endpoint = ""
				return cfg
			},
			expectPanic: false,
			postCheck: func(t *testing.T, cfg models.Configuration) {
				assert.Equal(t, "https://s3.us-west-2.amazonaws.com", cfg.Storage.Endpoint)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save original config
			originalConfig := config

			// Set up test configuration
			config = tt.setupFunc()

			if tt.expectPanic {
				assert.PanicsWithValue(t, tt.panicMessage, verifyConfiguration)
			} else {
				assert.NotPanics(t, verifyConfiguration)
				if tt.postCheck != nil {
					tt.postCheck(t, config)
				}
			}

			// Restore original config
			config = originalConfig
		})
	}
}

func TestGetDefaultConfig(t *testing.T) {
	cfg := getDefaultConfig()

	// Verify key default values
	assert.Equal(t, "http://localhost:8080", cfg.ServerURL)
	assert.Equal(t, "0.0.0.0", cfg.ServerHost)
	assert.Equal(t, 8080, cfg.ServerPort)
	assert.Equal(t, 512, cfg.MaxFileSizeMB)
	assert.Equal(t, 30, cfg.MinAgeDays)
	assert.Equal(t, 365, cfg.MaxAgeDays)
	assert.Equal(t, 12, cfg.RandomIDLength)
	assert.False(t, cfg.DisableIndexPage)
	assert.True(t, cfg.DisableUploadPage)
	assert.Equal(t, "", cfg.FaviconURL)
	assert.Equal(t, "png", cfg.FaviconFormat)

	// Verify storage defaults
	assert.Equal(t, "local", cfg.Storage.Type)
	assert.Equal(t, "./upload_data/", cfg.Storage.BasePath)

	// Verify database defaults
	assert.Equal(t, "sqlite", cfg.Database.Type)
	assert.Equal(t, "file:isrv.db?cache=shared&mode=rwc", cfg.Database.DSN)

	// Verify logging defaults
	assert.True(t, cfg.Logging.LogUploads)
	assert.True(t, cfg.Logging.LogIps)
	assert.Equal(t, zap.InfoLevel, cfg.Logging.Level)
	assert.Equal(t, "./isrv.log", cfg.Logging.Path)
}

func TestGenerateDefaultConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "generated-config.yaml")

	// Test generating default config
	assert.NotPanics(t, func() {
		GenerateDefaultConfig(configPath)
	})

	// Verify file was created
	assert.FileExists(t, configPath)

	// Read and verify contents
	data, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Should contain embedded default config content
	content := string(data)
	assert.Contains(t, content, "serverName:")
	assert.Contains(t, content, "serverPort:")
	assert.Contains(t, content, "storage:")
}

func TestGenerateDefaultConfig_CreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "nested", "path", "config.yaml")

	// Directory doesn't exist initially
	assert.NoDirExists(t, filepath.Dir(configPath))

	// Test generating config with nested path
	assert.NotPanics(t, func() {
		GenerateDefaultConfig(configPath)
	})

	// Directory should be created
	assert.DirExists(t, filepath.Dir(configPath))
	assert.FileExists(t, configPath)
}

func TestGenerateDefaultConfig_NoPermission(t *testing.T) {
	// On Unix systems, /root is typically not writable by non-root users
	configPath := "/root/isrv/config.yaml"

	// Test generating config in a non-writable location should panic
	assert.Panics(t, func() {
		GenerateDefaultConfig(configPath)
	})
}

func TestLoadFromFile_InvalidYaml(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "invalid.yaml")

	// Create file with invalid YAML
	require.NoError(t, os.WriteFile(configPath, []byte("invalid: yaml: content: ["), 0644))

	// Should panic on invalid YAML
	assert.Panics(t, func() {
		loadFromFile(configPath, false)
	})
}

func TestLoadFromFile_NonexistentFile(t *testing.T) {
	// Should panic when file doesn't exist
	assert.Panics(t, func() {
		loadFromFile("/nonexistent/path/config.yaml", false)
	})
}

func TestLoad_WithEnvironmentOverrides(t *testing.T) {
	// Create temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "test-config.yaml")

	configContent := `
serverName: "yaml-server"
serverPort: 8000
randomIdLength: 12
maxFileSizeMb: 512
storage:
  type: "local"
  basePath: "./data/"
`
	require.NoError(t, os.WriteFile(configPath, []byte(configContent), 0644))

	// Save original config
	originalConfig := config

	// Set environment variables that should override YAML values
	t.Setenv("ISRV_SERVER_NAME", "env-override-server")
	t.Setenv("ISRV_SERVER_PORT", "9999")

	// Load config (which calls applyEnvOverrides internally)
	Load(configPath, false)

	// Environment should override YAML
	assert.Equal(t, "env-override-server", config.ServerName)
	assert.Equal(t, 9999, config.ServerPort)

	// Restore original config
	config = originalConfig
}
