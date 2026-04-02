package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/markhc/isrv/internal/configuration"
	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/webserver"
	"github.com/spf13/cobra"
	"github.com/thejerf/suture/v4"
)

var (
	versionFlag       bool
	debugFlag         bool
	makeConfig        bool
	disableSupervisor bool
	configPath        string
)

type iSrvService struct{}

func (s *iSrvService) Serve(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			logging.LogInfo("shutting down iSrv service")

			return nil
		default:
			webserver.Start(ctx)

			return nil
		}
	}
}

var rootCmd = &cobra.Command{
	Use:   "isrv",
	Short: "isrv is a file sharing web server",
	Run: func(cmd *cobra.Command, args []string) {
		//nolint:all
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

		// If debug mode is enabled or the supervisor is disabled, run the webserver directly
		if configuration.Get().DebugMode || disableSupervisor {
			if configuration.Get().DebugMode {
				logging.LogDebug("debug mode is enabled")
			} else {
				logging.LogInfo("supervisor is disabled")
			}

			webserver.Start(context.Background())
		} else {
			supervisor := suture.NewSimple("iSrv")
			service := &iSrvService{}
			supervisor.Add(service)

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, os.Kill)
			defer stop()

			logging.LogInfo("starting isrv service supervisor")
			err := supervisor.Serve(ctx)
			if err != nil {
				logging.LogError("isrv service supervisor encountered an error", logging.Error(err))
			}
		}
	},
}

func Execute() {
	rootCmd.Flags().BoolVarP(&versionFlag, "version", "v", false, "Display the version of isrv")
	rootCmd.Flags().BoolVarP(&debugFlag, "debug", "d", false, "Enable debug mode")
	rootCmd.Flags().StringVarP(&configPath, "config", "c", "", "Path to configuration file")

	rootCmd.Flags().BoolVar(&makeConfig, "makeconf", false, "Generate a default configuration file and exit")
	rootCmd.Flags().BoolVar(
		&disableSupervisor,
		"disable-supervisor",
		false,
		"Disable the supervisor and run the webserver directly")

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
