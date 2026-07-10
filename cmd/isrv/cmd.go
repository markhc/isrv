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
	"github.com/markhc/isrv/internal/encryption"
	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/systemd"
	"github.com/spf13/cobra"

	"github.com/markhc/isrv/internal/telemetry"
)

var (
	versionFlag       bool
	debugFlag         bool
	makeConfig        bool
	genEncryptionKey  bool
	disableSupervisor bool
	purgeFlag         bool
	configPath        string
)

var rootCmd = &cobra.Command{
	Use:   "isrv",
	Short: "isrv is a file sharing web server",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Deprecated flag forms of the one-shot subcommands. Cobra prints a
		// deprecation notice when they are used.
		if versionFlag {
			printVersion()

			return nil
		}

		if makeConfig {
			return writeUserConfig()
		}

		if genEncryptionKey {
			return printEncryptionKey()
		}

		return runServer(cmd.Context())
	},
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run the isrv server (the default when no subcommand is given)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runServer(cmd.Context())
	},
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print build information",
	Run: func(cmd *cobra.Command, args []string) {
		printVersion()
	},
}

var makeconfCmd = &cobra.Command{
	Use:   "makeconf",
	Short: "Generate a default configuration file at ~/.config/isrv/config.yaml",
	RunE: func(cmd *cobra.Command, args []string) error {
		return writeUserConfig()
	},
}

var genEncryptionKeyCmd = &cobra.Command{
	Use:   "gen-encryption-key",
	Short: "Generate a new age encryption identity (AGE-SECRET-KEY-1...)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return printEncryptionKey()
	},
}

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install and start isrv as a systemd service (requires root)",
	Long: `Install isrv as a systemd service.

Copies the binary to /usr/local/bin/isrv, generates /etc/isrv/config.yaml if
absent, writes a hardened unit file, and enables + starts the service.
Re-run after replacing the binary to upgrade. See docs/systemd.md.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := systemd.Detect(); err != nil {
			return fmt.Errorf("install: %w", err)
		}

		opts, err := systemd.DefaultOptions()
		if err != nil {
			return fmt.Errorf("install: %w", err)
		}
		opts.Logf = printf

		if err := systemd.Install(opts); err != nil {
			return fmt.Errorf("install: %w", err)
		}

		printf("\nisrv is installed and running. Next steps:")
		printf("  edit %s (secrets go in %s)", opts.ConfigPath, opts.EnvFilePath)
		printf("  systemctl restart isrv    # apply config changes")
		printf("  systemctl status isrv")
		printf("  journalctl -u isrv -f")

		return nil
	},
}

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Stop, disable, and remove the isrv systemd service (requires root)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := systemd.Detect(); err != nil {
			return fmt.Errorf("uninstall: %w", err)
		}

		opts, err := systemd.DefaultOptions()
		if err != nil {
			return fmt.Errorf("uninstall: %w", err)
		}
		opts.Logf = printf

		if err := systemd.Uninstall(opts, purgeFlag); err != nil {
			return fmt.Errorf("uninstall: %w", err)
		}

		return nil
	},
}

//nolint:forbidigo
func printf(format string, args ...any) {
	fmt.Printf(format+"\n", args...)
}

func printVersion() {
	printf("isrv: A file sharing web server")
	printf("Build info:")
	printf("  Version:   %s", configuration.BuildVersion)
	printf("  Commit:    %s", configuration.BuildCommit)
	printf("  Date:      %s", configuration.BuildDate)
	printf("  Golang:    %s", configuration.BuildGoVersion)
	printf("  Platform:  %s", configuration.BuildPlatform)
}

func writeUserConfig() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	path := filepath.Join(home, ".config", "isrv", "config.yaml")
	if err := configuration.WriteDefaultConfig(path); err != nil {
		return fmt.Errorf("generate config: %w", err)
	}
	printf("wrote default configuration to %s", path)

	return nil
}

func printEncryptionKey() error {
	key, err := encryption.GenerateIdentity()
	if err != nil {
		return fmt.Errorf("generate encryption key: %w", err)
	}
	printf("%s", key)

	return nil
}

// runServer is the long-running entrypoint invoked by the root and serve
// commands. The process is crash-only: on failure it exits non-zero and the
// external supervisor (systemd, docker, k8s) applies its restart policy.
func runServer(parentCtx context.Context) error {
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

func Execute() {
	rootCmd.PersistentFlags().StringVarP(&configPath, "config", "c", "", "Path to configuration file")
	rootCmd.PersistentFlags().BoolVarP(&debugFlag, "debug", "d", false, "Enable debug mode")

	rootCmd.Flags().BoolVarP(&versionFlag, "version", "v", false, "Display the version of isrv")

	// Deprecated flag forms, kept for one release cycle; use the subcommands.
	rootCmd.Flags().BoolVar(&makeConfig, "makeconf", false, "Generate a default configuration file and exit")
	rootCmd.Flags().BoolVar(
		&genEncryptionKey,
		"gen-encryption-key",
		false,
		"Generate a new age encryption identity (AGE-SECRET-KEY-1...) and exit.")
	rootCmd.Flags().BoolVar(
		&disableSupervisor,
		"disable-supervisor",
		false,
		"No effect; the in-process supervisor was removed")
	_ = rootCmd.Flags().MarkDeprecated("makeconf", "use 'isrv makeconf' instead")
	_ = rootCmd.Flags().MarkDeprecated("gen-encryption-key", "use 'isrv gen-encryption-key' instead")
	_ = rootCmd.Flags().MarkDeprecated("disable-supervisor",
		"the in-process supervisor was removed; restart policy now belongs to systemd/docker")

	uninstallCmd.Flags().BoolVar(&purgeFlag, "purge", false,
		"Also delete /etc/isrv and /var/lib/isrv (config, uploads, database)")

	rootCmd.AddCommand(serveCmd, versionCmd, makeconfCmd, genEncryptionKeyCmd, installCmd, uninstallCmd)

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
