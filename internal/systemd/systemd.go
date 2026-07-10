// Package systemd installs and removes a systemd unit for isrv, backing the
// `isrv install` and `isrv uninstall` commands. The process itself is
// crash-only; restart policy, boot persistence, and sandboxing come from the
// generated unit. See docs/systemd.md and docs/notepad/systemd-install-plan.md.
package systemd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"

	"github.com/markhc/isrv/internal/configuration"
)

const unitName = "isrv.service"

// Options holds the target paths and seams used by Install and Uninstall.
// Use DefaultOptions for the production layout; tests point the paths at a
// temp directory and Run at a recording stub.
type Options struct {
	BinaryPath  string // installed binary location
	UnitPath    string // systemd unit file
	ConfigDir   string // holds ConfigPath and EnvFilePath; removed by purge
	ConfigPath  string // YAML configuration
	EnvFilePath string // root-only env file for secrets (ISRV_* overrides)
	StateDir    string // uploads, database, logs; removed by purge

	SourceBinary string // running executable that Install copies from

	// Run executes an external command (systemctl).
	Run func(name string, args ...string) error
	// Logf reports progress to the user; nil means silent.
	Logf func(format string, args ...any)
}

// DefaultOptions returns the production layout with the running executable
// as the copy source.
func DefaultOptions() (Options, error) {
	src, err := os.Executable()
	if err != nil {
		return Options{}, fmt.Errorf("locate running executable: %w", err)
	}

	return Options{
		BinaryPath:   "/usr/local/bin/isrv",
		UnitPath:     filepath.Join("/etc/systemd/system", unitName),
		ConfigDir:    "/etc/isrv",
		ConfigPath:   "/etc/isrv/config.yaml",
		EnvFilePath:  "/etc/isrv/isrv.env",
		StateDir:     "/var/lib/isrv",
		SourceBinary: src,
		Run:          runCommand,
	}, nil
}

// Detect reports whether this host can be managed by Install/Uninstall:
// Linux, systemd running, and effective root.
func Detect() error {
	if runtime.GOOS != "linux" {
		return errors.New("this command requires Linux with systemd")
	}

	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return errors.New("systemd does not appear to be running on this host; see docs/systemd.md for alternatives")
	}

	if os.Geteuid() != 0 {
		return errors.New("this command must run as root (try sudo)")
	}

	return nil
}

// unitTemplate is the generated service definition. WorkingDirectory points
// at the state directory so the relative paths in the default config
// (./upload_data/, isrv.db, ./isrv.log) land under /var/lib/isrv, which is
// the only writable location under ProtectSystem=strict.
const unitTemplate = `[Unit]
Description=isrv file sharing server
After=network-online.target
Wants=network-online.target
StartLimitIntervalSec=60
StartLimitBurst=5

[Service]
ExecStart={{.BinaryPath}} serve --config {{.ConfigPath}}
EnvironmentFile=-{{.EnvFilePath}}
Restart=always
RestartSec=2
DynamicUser=yes
StateDirectory=isrv
ConfigurationDirectory=isrv
WorkingDirectory={{.StateDir}}
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
NoNewPrivileges=yes

[Install]
WantedBy=multi-user.target
`

// envFileTemplate seeds the root-only secrets file. EnvironmentFile is read
// by systemd itself, so it stays 0600 root:root while the service runs as an
// unprivileged dynamic user — unlike config.yaml, which must stay readable.
const envFileTemplate = `# Environment overrides for the isrv service (systemd EnvironmentFile).
# Keep secrets here instead of the world-readable config.yaml; this file is
# read by systemd as root and should remain mode 0600.
#
# Any configuration field can be overridden via ISRV_* variables. Common ones:
#
# ISRV_ADMIN_USERNAME=admin
# ISRV_ADMIN_PASSWORD=change-me
# ISRV_ADMIN_SESSION_SECRET=...
# ISRV_ENCRYPTION_IDENTITY=AGE-SECRET-KEY-1...
# ISRV_STORAGE_ACCESS_KEY=...
# ISRV_STORAGE_SECRET_KEY=...
# ISRV_DATABASE_PASSWORD=...
`

// Install copies the binary, seeds config and secrets files if absent,
// writes the unit, and enables + (re)starts the service. It is idempotent:
// re-running after replacing the binary is the upgrade path.
func Install(opts Options) error {
	logf := opts.logf()

	if err := opts.installBinary(logf); err != nil {
		return err
	}

	if err := os.MkdirAll(opts.ConfigDir, 0o755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	if _, err := os.Stat(opts.ConfigPath); errors.Is(err, fs.ErrNotExist) {
		if err := configuration.WriteDefaultConfig(opts.ConfigPath); err != nil {
			return fmt.Errorf("write default config: %w", err)
		}
		logf("created default config at %s", opts.ConfigPath)
	} else if err != nil {
		return fmt.Errorf("stat config: %w", err)
	} else {
		logf("keeping existing config at %s", opts.ConfigPath)
	}

	if _, err := os.Stat(opts.EnvFilePath); errors.Is(err, fs.ErrNotExist) {
		if err := os.WriteFile(opts.EnvFilePath, []byte(envFileTemplate), 0o600); err != nil {
			return fmt.Errorf("write env file: %w", err)
		}
		logf("created secrets env file at %s", opts.EnvFilePath)
	} else if err != nil {
		return fmt.Errorf("stat env file: %w", err)
	}

	unit, err := opts.renderUnit()
	if err != nil {
		return err
	}
	if err := os.WriteFile(opts.UnitPath, []byte(unit), 0o644); err != nil {
		return fmt.Errorf("write unit file: %w", err)
	}
	logf("wrote unit file %s", opts.UnitPath)

	for _, args := range [][]string{
		{"daemon-reload"},
		{"enable", unitName},
		{"restart", unitName},
	} {
		if err := opts.Run("systemctl", args...); err != nil {
			return fmt.Errorf("systemctl %s: %w", strings.Join(args, " "), err)
		}
	}
	logf("service %s enabled and started", unitName)

	return nil
}

// Uninstall stops and disables the service and removes the unit file. With
// purge it also deletes the config and state directories — including uploads
// and the database. The installed binary is left in place.
func Uninstall(opts Options, purge bool) error {
	logf := opts.logf()

	if _, err := os.Stat(opts.UnitPath); err == nil {
		if err := opts.Run("systemctl", "disable", "--now", unitName); err != nil {
			return fmt.Errorf("systemctl disable --now %s: %w", unitName, err)
		}

		if err := os.Remove(opts.UnitPath); err != nil {
			return fmt.Errorf("remove unit file: %w", err)
		}

		if err := opts.Run("systemctl", "daemon-reload"); err != nil {
			return fmt.Errorf("systemctl daemon-reload: %w", err)
		}
		logf("removed %s", opts.UnitPath)
	} else if errors.Is(err, fs.ErrNotExist) {
		logf("no unit file at %s; nothing to disable", opts.UnitPath)
	} else {
		return fmt.Errorf("stat unit file %s: %w", opts.UnitPath, err)
	}

	if purge {
		// DynamicUser=yes makes systemd store StateDirectory=isrv under
		// /var/lib/private/isrv and symlink StateDir to it. Removing StateDir
		// only drops the symlink, so delete the private backing dir too or the
		// uploads and database survive the purge.
		backingDir := filepath.Join(filepath.Dir(opts.StateDir), "private", filepath.Base(opts.StateDir))
		for _, dir := range []string{opts.ConfigDir, opts.StateDir, backingDir} {
			if err := os.RemoveAll(dir); err != nil {
				return fmt.Errorf("purge %s: %w", dir, err)
			}
			logf("removed %s", dir)
		}
	}

	return nil
}

// installBinary copies the running executable to BinaryPath. It writes to a
// temp file and renames so an upgrade never hits ETXTBSY on the live binary.
func (o Options) installBinary(logf func(string, ...any)) error {
	srcInfo, err := os.Stat(o.SourceBinary)
	if err != nil {
		return fmt.Errorf("stat source binary: %w", err)
	}
	if dstInfo, err := os.Stat(o.BinaryPath); err == nil && os.SameFile(srcInfo, dstInfo) {
		logf("already running from %s; skipping binary copy", o.BinaryPath)

		return nil
	}

	src, err := os.Open(o.SourceBinary)
	if err != nil {
		return fmt.Errorf("open source binary: %w", err)
	}
	defer src.Close()

	if err := os.MkdirAll(filepath.Dir(o.BinaryPath), 0o755); err != nil {
		return fmt.Errorf("create binary directory: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(o.BinaryPath), ".isrv-install-*")
	if err != nil {
		return fmt.Errorf("create temp binary: %w", err)
	}
	defer os.Remove(tmp.Name())

	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()

		return fmt.Errorf("copy binary: %w", err)
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()

		return fmt.Errorf("chmod binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp binary: %w", err)
	}

	if err := os.Rename(tmp.Name(), o.BinaryPath); err != nil {
		return fmt.Errorf("install binary: %w", err)
	}
	logf("installed binary to %s", o.BinaryPath)

	return nil
}

func (o Options) renderUnit() (string, error) {
	tmpl, err := template.New("unit").Parse(unitTemplate)
	if err != nil {
		return "", fmt.Errorf("parse unit template: %w", err)
	}

	var b strings.Builder
	if err := tmpl.Execute(&b, o); err != nil {
		return "", fmt.Errorf("render unit template: %w", err)
	}

	return b.String(), nil
}

func (o Options) logf() func(string, ...any) {
	if o.Logf != nil {
		return o.Logf
	}

	return func(string, ...any) {}
}

func runCommand(name string, args ...string) error {
	// One-shot CLI invocation; there is no caller context to inherit.
	cmd := exec.CommandContext(context.Background(), name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}

	return nil
}
