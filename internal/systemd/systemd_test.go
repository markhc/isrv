package systemd_test

import (
	"os"
	"path/filepath"
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

	require.NoError(t, systemd.Uninstall(opts, true))

	assert.NoDirExists(t, opts.ConfigDir)
	assert.NoDirExists(t, opts.StateDir)
}

func TestUninstallToleratesDisableFailure(t *testing.T) {
	opts, rec := testOptions(t)
	rec.fail = map[string]error{
		"systemctl disable --now isrv.service": assert.AnError,
	}
	require.NoError(t, os.MkdirAll(filepath.Dir(opts.UnitPath), 0o755))
	require.NoError(t, os.WriteFile(opts.UnitPath, []byte("[Unit]\n"), 0o644))

	require.NoError(t, systemd.Uninstall(opts, false))

	assert.NoFileExists(t, opts.UnitPath)
}
