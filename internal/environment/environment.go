package environment

import (
	"fmt"
	"path/filepath"

	envParser "github.com/caarlos0/env/v11"
	"github.com/markhc/isrv/internal/models"
)

func ParseEnv() models.Environment {
	var result models.Environment

	err := envParser.ParseWithOptions(&result, envParser.Options{
		Prefix: "ISRV_",
	})

	if err != nil {
		fmt.Println("Error parsing env variables:", err)
		panic(1)
	}

	// If both ConfigDir and ConfigFile are set, then we use this path as the config path
	// Otherwise, we look at other known locations like $HOME/.config/isrv and /etc/isrv
	result.ConfigPathIsSet = result.ConfigFile != "" && result.ConfigDir != ""

	if result.ConfigPathIsSet {
		result.ConfigPath = filepath.Join(result.ConfigDir, result.ConfigFile)
	}

	return result
}
