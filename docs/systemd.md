# Running isrv as a systemd service

For bare-metal (non-docker) deployments, isrv can install itself as a systemd
service. The process is crash-only by design — on any failure it exits
non-zero and systemd's restart policy brings it back.

## Install

```bash
sudo ./isrv install
```

This is idempotent and does the following:

1. Copies the binary to `/usr/local/bin/isrv` (skipped when already running
   from there).
2. Generates `/etc/isrv/config.yaml` from the defaults if absent — an
   existing config is never overwritten.
3. Creates `/etc/isrv/isrv.env` (mode `0600`) if absent, for secrets.
4. Writes `/etc/systemd/system/isrv.service` (always refreshed).
5. Runs `systemctl daemon-reload`, enables the service, and (re)starts it.

Then check on it:

```bash
systemctl status isrv
journalctl -u isrv -f
```

## Where things live

| What | Path |
|------|------|
| Binary | `/usr/local/bin/isrv` |
| Configuration | `/etc/isrv/config.yaml` |
| Secrets | `/etc/isrv/isrv.env` |
| Uploads, database, logs | `/var/lib/isrv` |

The service runs as an unprivileged dynamic user (`DynamicUser=yes`) inside a
sandbox (`ProtectSystem=strict`, `ProtectHome=yes`, `PrivateTmp=yes`); the
only writable location is `/var/lib/isrv`. The unit sets
`WorkingDirectory=/var/lib/isrv`, so the relative paths in the default config
(`./upload_data/`, `isrv.db`, `./isrv.log`) resolve there.

## Secrets

`/etc/isrv/config.yaml` is world-readable so the dynamic service user can
read it. Do not put credentials in it. Instead use `/etc/isrv/isrv.env`,
which systemd reads as root (the file stays `0600`), and which overrides the
config via `ISRV_*` environment variables:

```bash
# /etc/isrv/isrv.env
ISRV_ADMIN_USERNAME=admin
ISRV_ADMIN_PASSWORD=...
ISRV_ENCRYPTION_IDENTITY=AGE-SECRET-KEY-1...
ISRV_STORAGE_ACCESS_KEY=...
ISRV_STORAGE_SECRET_KEY=...
```

Apply changes with `sudo systemctl restart isrv`.

## Upgrades

Re-run the installer from the new binary:

```bash
sudo ./isrv-new install
```

It replaces `/usr/local/bin/isrv`, refreshes the unit file, and restarts the
service. Config and data are untouched.

## Customizing the unit

Don't edit `/etc/systemd/system/isrv.service` directly — the next
`isrv install` overwrites it. Use a drop-in instead:

```bash
sudo systemctl edit isrv
```

Common overrides:

```ini
[Service]
# Bind ports below 1024 (e.g. serving :443 directly)
AmbientCapabilities=CAP_NET_BIND_SERVICE

# Cap memory so a runaway isrv is OOM-killed (and restarted) without
# taking down the rest of the host
MemoryMax=512M
```

## Uninstall

```bash
sudo isrv uninstall           # stop, disable, remove the unit
sudo isrv uninstall --purge   # ALSO delete /etc/isrv and /var/lib/isrv
```

`--purge` deletes the configuration, all uploaded files, and the database.
The binary at `/usr/local/bin/isrv` is left in place either way.

## Manual setup / non-systemd hosts

If you prefer to manage the unit yourself, the generated file is a good
starting point — copy it from `/etc/systemd/system/isrv.service` after an
install, or adapt this minimal version:

```ini
[Unit]
Description=isrv file sharing server
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/isrv serve --config /etc/isrv/config.yaml
Restart=always
RestartSec=2
DynamicUser=yes
StateDirectory=isrv
WorkingDirectory=/var/lib/isrv

[Install]
WantedBy=multi-user.target
```

On hosts without systemd (Alpine/OpenRC, runit, BSDs), run `isrv serve`
under your init system's supervision with "restart on failure" semantics —
the binary needs nothing beyond that. Docker users get the same via
`restart: unless-stopped` in the provided compose files.
