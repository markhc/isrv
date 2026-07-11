package systemd_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/markhc/isrv/internal/systemd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recorder captures systemctl invocations instead of executing them.
type recorder struct {
	calls []string
	fail  map[string]error
}

func (r *recorder) run(name string, args ...string) error {
	call := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, call)

	if r.fail != nil {
		if err, ok := r.fail[call]; ok {
			return err
		}
	}

	return nil
}

func testOptions(t *testing.T) (systemd.Options, *recorder) {
	t.Helper()

	dir := t.TempDir()
	src := filepath.Join(dir, "isrv-source")
	require.NoError(t, os.WriteFile(src, []byte("fake-binary"), 0o755))

	rec := &recorder{}

	return systemd.Options{
		BinaryPath:   filepath.Join(dir, "bin", "isrv"),
		UnitPath:     filepath.Join(dir, "system", "isrv.service"),
		ConfigDir:    filepath.Join(dir, "etc"),
		ConfigPath:   filepath.Join(dir, "etc", "config.yaml"),
		EnvFilePath:  filepath.Join(dir, "etc", "isrv.env"),
		StateDir:     filepath.Join(dir, "state"),
		SourceBinary: src,
		Run:          rec.run,
	}, rec
}

func TestDefaultOptions(t *testing.T) {
	opts, err := systemd.DefaultOptions()
	require.NoError(t, err)

	assert.Equal(t, "/usr/local/bin/isrv", opts.BinaryPath)
	assert.Equal(t, "/etc/systemd/system/isrv.service", opts.UnitPath)
	assert.Equal(t, "/etc/isrv", opts.ConfigDir)
	assert.Equal(t, "/etc/isrv/config.yaml", opts.ConfigPath)
	assert.Equal(t, "/etc/isrv/isrv.env", opts.EnvFilePath)
	assert.Equal(t, "/var/lib/isrv", opts.StateDir)
	assert.NotEmpty(t, opts.SourceBinary, "SourceBinary should be the running executable")
	require.NotNil(t, opts.Run, "Run seam should default to the real command runner")
}

// TestDefaultOptionsRun exercises the default Run seam (runCommand) without
// touching systemctl: it runs harmless commands and asserts success/error.
func TestDefaultOptionsRun(t *testing.T) {
	opts, err := systemd.DefaultOptions()
	require.NoError(t, err)

	t.Run("successful command returns nil", func(t *testing.T) {
		assert.NoError(t, opts.Run("true"))
	})

	t.Run("missing command returns error", func(t *testing.T) {
		assert.Error(t, opts.Run("isrv-no-such-command-xyz"))
	})
}

func TestDetect(t *testing.T) {
	err := systemd.Detect()

	switch {
	case runtime.GOOS != "linux":
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Linux")
	case os.Geteuid() != 0:
		// Non-root on Linux: fails either because systemd is absent or because
		// the caller is not root. Both are legitimate non-nil outcomes.
		require.Error(t, err)
	default:
		// Root on a systemd host: Detect may succeed; nothing to assert safely.
		t.Skip("running as root; Detect outcome depends on host systemd state")
	}
}

func TestInstallFailsWhenSourceBinaryMissing(t *testing.T) {
	opts, rec := testOptions(t)
	opts.SourceBinary = filepath.Join(t.TempDir(), "does-not-exist")
	require.NoError(t, os.MkdirAll(filepath.Dir(opts.UnitPath), 0o755))

	err := systemd.Install(opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stat source binary")
	// The failure happens before any systemctl call.
	assert.Empty(t, rec.calls)
}

func TestInstallPropagatesSystemctlFailure(t *testing.T) {
	opts, rec := testOptions(t)
	rec.fail = map[string]error{
		"systemctl daemon-reload": assert.AnError,
	}
	require.NoError(t, os.MkdirAll(filepath.Dir(opts.UnitPath), 0o755))

	err := systemd.Install(opts)
	require.ErrorIs(t, err, assert.AnError)
	assert.Contains(t, err.Error(), "systemctl daemon-reload")
	// Aborts on the first failing systemctl call.
	assert.Equal(t, []string{"systemctl daemon-reload"}, rec.calls)
}

func TestInstall(t *testing.T) {
	opts, rec := testOptions(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(opts.UnitPath), 0o755))

	require.NoError(t, systemd.Install(opts))

	// Binary copied with the executable bit set.
	data, err := os.ReadFile(opts.BinaryPath)
	require.NoError(t, err)
	assert.Equal(t, "fake-binary", string(data))
	info, err := os.Stat(opts.BinaryPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())

	// Default config generated.
	cfg, err := os.ReadFile(opts.ConfigPath)
	require.NoError(t, err)
	assert.Contains(t, string(cfg), "serverPort: 8080")

	// Secrets env file is root-only.
	envInfo, err := os.Stat(opts.EnvFilePath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), envInfo.Mode().Perm())

	// Unit references the installed paths.
	unit, err := os.ReadFile(opts.UnitPath)
	require.NoError(t, err)
	assert.Contains(t, string(unit), "ExecStart="+opts.BinaryPath+" serve --config "+opts.ConfigPath)
	assert.Contains(t, string(unit), "EnvironmentFile=-"+opts.EnvFilePath)
	assert.Contains(t, string(unit), "WorkingDirectory="+opts.StateDir)
	assert.Contains(t, string(unit), "Restart=always")
	assert.Contains(t, string(unit), "DynamicUser=yes")

	assert.Equal(t, []string{
		"systemctl daemon-reload",
		"systemctl enable isrv.service",
		"systemctl restart isrv.service",
	}, rec.calls)
}

func TestInstallKeepsExistingConfigAndEnvFile(t *testing.T) {
	opts, _ := testOptions(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(opts.UnitPath), 0o755))
	require.NoError(t, os.MkdirAll(opts.ConfigDir, 0o755))
	require.NoError(t, os.WriteFile(opts.ConfigPath, []byte("serverPort: 9999\n"), 0o644))
	require.NoError(t, os.WriteFile(opts.EnvFilePath, []byte("ISRV_ADMIN_PASSWORD=x\n"), 0o600))

	require.NoError(t, systemd.Install(opts))

	cfg, err := os.ReadFile(opts.ConfigPath)
	require.NoError(t, err)
	assert.Equal(t, "serverPort: 9999\n", string(cfg))

	env, err := os.ReadFile(opts.EnvFilePath)
	require.NoError(t, err)
	assert.Equal(t, "ISRV_ADMIN_PASSWORD=x\n", string(env))
}

func TestInstallIsIdempotent(t *testing.T) {
	opts, rec := testOptions(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(opts.UnitPath), 0o755))

	require.NoError(t, systemd.Install(opts))
	require.NoError(t, systemd.Install(opts))

	// Two full systemctl sequences; the second restart is the upgrade path.
	assert.Len(t, rec.calls, 6)
}

func TestInstallSkipsCopyWhenRunningFromTarget(t *testing.T) {
	opts, _ := testOptions(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(opts.UnitPath), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(opts.BinaryPath), 0o755))
	require.NoError(t, os.Rename(opts.SourceBinary, opts.BinaryPath))
	opts.SourceBinary = opts.BinaryPath

	require.NoError(t, systemd.Install(opts))

	data, err := os.ReadFile(opts.BinaryPath)
	require.NoError(t, err)
	assert.Equal(t, "fake-binary", string(data))
}

func TestUninstall(t *testing.T) {
	opts, rec := testOptions(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(opts.UnitPath), 0o755))
	require.NoError(t, os.WriteFile(opts.UnitPath, []byte("[Unit]\n"), 0o644))
	require.NoError(t, os.MkdirAll(opts.ConfigDir, 0o755))

	require.NoError(t, systemd.Uninstall(opts, false))

	assert.NoFileExists(t, opts.UnitPath)
	assert.Equal(t, []string{
		"systemctl disable --now isrv.service",
		"systemctl daemon-reload",
	}, rec.calls)

	// Config and state are untouched without --purge.
	assert.DirExists(t, opts.ConfigDir)
}

func TestUninstallWithoutUnitFile(t *testing.T) {
	opts, rec := testOptions(t)

	require.NoError(t, systemd.Uninstall(opts, false))

	assert.Empty(t, rec.calls)
}

func TestUninstallPurge(t *testing.T) {
	opts, _ := testOptions(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(opts.UnitPath), 0o755))
	require.NoError(t, os.WriteFile(opts.UnitPath, []byte("[Unit]\n"), 0o644))
	require.NoError(t, os.MkdirAll(opts.ConfigDir, 0o755))
	require.NoError(t, os.MkdirAll(opts.StateDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(opts.StateDir, "isrv.db"), []byte("db"), 0o644))

	// DynamicUser=yes backs StateDir with /var/lib/private/isrv; seed a file
	// there to confirm purge follows into the private backing directory.
	backingDir := filepath.Join(filepath.Dir(opts.StateDir), "private", filepath.Base(opts.StateDir))
	require.NoError(t, os.MkdirAll(backingDir, 0o755))
	backingDB := filepath.Join(backingDir, "isrv.db")
	require.NoError(t, os.WriteFile(backingDB, []byte("db"), 0o644))

	require.NoError(t, systemd.Uninstall(opts, true))

	assert.NoDirExists(t, opts.ConfigDir)
	assert.NoDirExists(t, opts.StateDir)
	assert.NoFileExists(t, backingDB)
	assert.NoDirExists(t, backingDir)
}

func TestUninstallPropagatesDisableFailure(t *testing.T) {
	opts, rec := testOptions(t)
	rec.fail = map[string]error{
		"systemctl disable --now isrv.service": assert.AnError,
	}
	require.NoError(t, os.MkdirAll(filepath.Dir(opts.UnitPath), 0o755))
	require.NoError(t, os.WriteFile(opts.UnitPath, []byte("[Unit]\n"), 0o644))

	err := systemd.Uninstall(opts, false)
	require.ErrorIs(t, err, assert.AnError)

	// The unit file is left in place so a retry can complete the teardown.
	assert.FileExists(t, opts.UnitPath)
	assert.Equal(t, []string{"systemctl disable --now isrv.service"}, rec.calls)
}
