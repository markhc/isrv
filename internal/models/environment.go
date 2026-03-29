package models

type Environment struct {
	ServerName     string `env:"SERVER_NAME" envDefault:"iSRV"`                 // Sets the server name
	ServerURL      string `env:"SERVER_URL" envDefault:"http://localhost:8080"` // Sets the server URL
	ServerHost     string `env:"SERVER_HOST" envDefault:"0.0.0.0"`              // Sets the server host address
	ServerPort     int    `env:"SERVER_PORT" envDefault:"8080"`                 // Sets the server port
	ConfigDir      string `env:"CONFIG_DIR" envDefault:""`                      // Sets the configuration directory
	ConfigFile     string `env:"CONFIG_FILE" envDefault:""`                     // Sets the configuration file name
	DataDir        string `env:"DATA_DIR" envDefault:"data"`                    // Sets the data directory
	LogDir         string `env:"LOG_DIR" envDefault:"config"`                   // Sets the log directory
	LogFile        string `env:"LOG_FILE" envDefault:"isrv.log"`                // Sets the log file name
	FileNameLength int    `env:"FILENAME_LENGTH" envDefault:"12"`               // Sets the length of randomly generated file names
	ShareIdLength  int    `env:"SHARE_ID_LENGTH" envDefault:"8"`                // Sets the length of shareable links ids
	MaxFileSizeMB  int    `env:"MAX_FILE_SIZE_MB" envDefault:"102400"`          // Sets the maximum file size in megabytes

	// Non-env fields
	ConfigPath      string // Full path to the configuration file
	ConfigPathIsSet bool   // Whether the configuration path was explicitly set
}
