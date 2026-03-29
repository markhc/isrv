package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/markhc/isrv/internal/configuration"
	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/webserver"
	"github.com/spf13/cobra"
)

var (
	versionFlag bool
	debugFlag   bool
	makeConfig  bool
	configPath  string
)

var rootCmd = &cobra.Command{
	Use:   "isrv",
	Short: "isrv is a file sharing web server",
	Run: func(cmd *cobra.Command, args []string) {
		if versionFlag {
			fmt.Println("isrv: A file sharing web server")
			fmt.Println("Build info:")
			fmt.Println("  Version:  ", configuration.BuildVersion)
			fmt.Println("  Commit:   ", configuration.BuildCommit)
			fmt.Println("  Date:     ", configuration.BuildDate)
			fmt.Println("  Golang:   ", configuration.BuildGoVersion)
			fmt.Println("  Platform: ", configuration.BuildPlatform)
			return
		}

		if makeConfig {
			configuration.GenerateDefaultConfig(filepath.Join(os.Getenv("HOME"), ".config", "isrv", "config.yaml"))
			return
		}

		configuration.Load(configPath, debugFlag)
		logging.Initialize()

		webserver.Start()
	},
}

func Execute() {
	rootCmd.Flags().BoolVarP(&versionFlag, "version", "v", false, "Display the version of isrv")
	rootCmd.Flags().BoolVarP(&debugFlag, "debug", "d", false, "Enable debug mode")
	rootCmd.Flags().StringVarP(&configPath, "config", "c", "", "Path to configuration file")

	rootCmd.Flags().BoolVar(&makeConfig, "makeconf", false, "Generate a default configuration file and exit")

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
