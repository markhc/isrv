package server

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/markhc/isrv/internal/app"
	"github.com/markhc/isrv/internal/configuration"
	"github.com/markhc/isrv/internal/logging"
	"github.com/spf13/cobra"

	"github.com/markhc/isrv/internal/telemetry"
)

var ServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run the isrv server (the default when no subcommand is given)",
	RunE: func(cmd *cobra.Command, args []string) error {
		configPath := cmd.Flags().Lookup("config").Value.String()
		debugFlag := cmd.Flags().Lookup("debug").Value.String() == "true"

		return Run(cmd.Context(), configPath, debugFlag)
	},
}

// Run is the long-running entrypoint invoked by the root and serve
// commands. The process is crash-only: on failure it exits non-zero and the
// external supervisor (systemd, docker, k8s) applies its restart policy.
func Run(parentCtx context.Context, configPath string, debugFlag bool) error {
	configuration.Load(configPath, debugFlag)
	logging.Initialize()
	defer func() {
		if err := logging.Shutdown(); err != nil {
			fmt.Fprintf(os.Stderr, "logging shutdown: %v\n", err)
		}
	}()

	// Install signal handling once for the whole process.
	ctx, stop := signal.NotifyContext(parentCtx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Telemetry is set up once at process start and torn down on exit.
	shutdownTelemetry, err := telemetry.Setup(ctx, configuration.BuildVersion)
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

	if configuration.Get().DebugMode {
		logging.LogDebug("debug mode is enabled")
	}

	if err := app.StartApp(ctx); err != nil {
		logging.LogError("isrv stopped with error", logging.Error(err))

		return fmt.Errorf("run app: %w", err)
	}

	logging.LogInfo("shutting down iSrv service")

	return nil
}

var ServerCmds = []*cobra.Command{
	ServeCmd,
}
