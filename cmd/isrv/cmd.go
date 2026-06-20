package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/markhc/isrv/internal/app"
	"github.com/markhc/isrv/internal/configuration"
	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/telemetry"
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
	if err := app.StartApp(ctx); err != nil {
		logging.LogError("isrv service stopped with error", logging.Error(err))

		return fmt.Errorf("app start: %w", err)
	}

	logging.LogInfo("shutting down iSrv service")

	return nil
}

var rootCmd = &cobra.Command{
	Use:   "isrv",
	Short: "isrv is a file sharing web server",
	RunE: func(cmd *cobra.Command, args []string) error {
		//nolint:all
		if versionFlag {
			fmt.Println("isrv: A file sharing web server")
			fmt.Println("Build info:")
			fmt.Println("  Version:  ", configuration.BuildVersion)
			fmt.Println("  Commit:   ", configuration.BuildCommit)
			fmt.Println("  Date:     ", configuration.BuildDate)
			fmt.Println("  Golang:   ", configuration.BuildGoVersion)
			fmt.Println("  Platform: ", configuration.BuildPlatform)

			return nil
		}

		if makeConfig {
			configuration.GenerateDefaultConfig(filepath.Join(os.Getenv("HOME"), ".config", "isrv", "config.yaml"))

			return nil
		}

		return runServer(cmd.Context())
	},
}

// runServer is the long-running entrypoint invoked by the root command. It
// loads configuration, initialises logging and telemetry exactly once, and
// supervises the application loop until the process is signalled.
func runServer(parentCtx context.Context) error {
	configuration.Load(configPath, debugFlag)
	logging.Initialize()
	defer func() {
		if err := logging.Shutdown(); err != nil {
			fmt.Fprintf(os.Stderr, "logging shutdown: %v\n", err)
		}
	}()

	// Install signal handling once for the whole process. The resulting
	// context is propagated down through the supervisor and StartApp.
	ctx, stop := signal.NotifyContext(parentCtx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Telemetry is set up once at process start and torn down on exit. The
	// supervisor-driven restarts of the app loop reuse the same exporters
	// and provider instances rather than re-creating them on every restart.
	shutdownTelemetry, err := telemetry.Setup(ctx, configuration.Get().Telemetry, configuration.BuildVersion)
	if err != nil {
		logging.LogError("failed to initialise telemetry", logging.Error(err))

		return fmt.Errorf("initialise telemetry: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := shutdownTelemetry(shutdownCtx); err != nil {
			logging.LogError("failed to flush telemetry", logging.Error(err))
		}
	}()

	// Once the OTel logger provider is registered globally, the otelzap
	// bridge can forward records. AttachOTelBridge swaps the global logger
	// in place without re-opening any file descriptors.
	logging.AttachOTelBridge()

	// In debug mode or when the supervisor is disabled, run the webserver
	// directly so failures surface immediately rather than being restarted.
	cfg := configuration.Get()
	if cfg.DebugMode || disableSupervisor {
		if cfg.DebugMode {
			logging.LogDebug("debug mode is enabled")
		} else {
			logging.LogInfo("supervisor is disabled")
		}

		if err := app.StartApp(ctx); err != nil {
			return fmt.Errorf("run app: %w", err)
		}

		return nil
	}

	supervisor := suture.NewSimple("iSrv")
	supervisor.Add(&iSrvService{})

	logging.LogInfo("starting isrv service supervisor")
	if err := supervisor.Serve(ctx); err != nil {
		logging.LogError("isrv service supervisor encountered an error", logging.Error(err))

		return fmt.Errorf("supervisor: %w", err)
	}

	return nil
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

	// Silence cobra's automatic error/usage output: errors are already
	// logged through the structured logger inside runServer.
	rootCmd.SilenceErrors = true
	rootCmd.SilenceUsage = true

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func main() {
	Execute()
}
