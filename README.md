# isrv

[![Go Build](https://github.com/markhc/isrv/actions/workflows/build.yaml/badge.svg)](https://github.com/markhc/isrv/actions/workflows/build.yaml)

Simple anonymous and temporary file sharing service.

Visit https://isrv.nl to see it in action.

## Description

isrv is a lightweight file sharing service that provides anonymous temporary storage with customizable expiration times. Users can upload files and share them via generated links without requiring registration or personal information.

## Goals

- Anonymous temporary storage, with customizable expiration time
- Easy installation: Single statically-linked binary that can be deployed anywhere
- Manage your own uploads, without compromising anonymity

## Roadmap

This project is a work in progress, here's a list of things I am working on in no particular order:

- True E2EE with the key in the URL fragment; The server never sees the key and cannot decrypt files
- Client mode in the same binary, allowing users to upload and manage their files with ease
- Metadata stripping for uploaded files (EXIF, ID3, etc.)
- Support non-temporary storage option for users who want to keep their files indefinitely
- Built-in TLS support with automatic certificate management CertMagic
- "Burn After Reading" feature: Expiry after N downloads
- Zip file support for multiple uploads: 
  - Upload multiple files and download them as a single zip file
- Text/Paste file preview in the web interface with syntax highlighting
- Improvements to abuse detection and mitigation
- Storage tiers (namely "hot" and "cold") to optimize costs and performance based on file access patterns

## Installation

### Pre-built binaries

Download the latest release for your platform from the releases page and make it executable:

```bash
# Linux
wget https://github.com/markhc/isrv/releases/latest/download/isrv-linux-amd64
chmod +x isrv-linux-amd64
sudo mv isrv-linux-amd64 /usr/local/bin/isrv
```

To run isrv as a system service (auto-start on boot, restart on failure), the
binary can install itself as a systemd unit:

```bash
sudo isrv install
```

See [docs/systemd.md](docs/systemd.md) for details, upgrades, and removal.

### Docker

- Create a `docker-compose.yaml` file (you can use the project's [docker-compose.yaml](docker-compose.yaml) as base)
- Create the data folder where the database and files will be kept.
- Start the container

Example:
```bash
mkdir -p ./data
docker compose up -d
```

### From source

Requires Go 1.25 or later:

```bash
git clone https://github.com/markhc/isrv.git
cd isrv
make build
```

The binary will be available in the `build/` directory.

## Usage

Running the server is as easy as starting the binary.

```bash
# Generates a default configuration file on $HOME/.config/isrv/config.yaml
isrv makeconf

# Starts the webserver (will load config file if it exists)
isrv

# Starts the webserver with a specific config file
isrv serve -c config.yaml

# Installs and starts isrv as a systemd service (see docs/systemd.md)
sudo isrv install
```

If no config file is provided the application will look for one in standard places and, if none can be found, default values will be used.

The web interface will be available at `http://localhost:8080`.

## Configuration

Configuration can be provided via:
- Configuration file
- Environment variables

### Configuration file

[default_config.yaml](internal/configuration/default_config.yaml)

### Environment Variables

When set, environment variables override the corresponding values from the configuration file.

| Variable | Default | Description |
|----------|---------|-------------|
| `ISRV_SERVER_NAME` | `iSRV` | Sets the server name |
| `ISRV_SERVER_URL` | `http://localhost:8080` | Sets the server URL |
| `ISRV_SERVER_HOST` | `0.0.0.0` | Sets the server host address |
| `ISRV_SERVER_PORT` | `8080` | Sets the server port |
| `ISRV_STORAGE_TYPE` | `local` | Storage backend: `local`, `s3` or `gcs` |
| `ISRV_STORAGE_PATH` | - | Sets the storage base path (directory for `local`, key prefix for `s3`/`gcs`) |
| `ISRV_STORAGE_BUCKET_NAME` | - | Bucket name for `s3` and `gcs` storage |
| `ISRV_STORAGE_CREDENTIALS_FILE` | - | Service account JSON key file for `gcs` storage (empty = Application Default Credentials) |
| `ISRV_LOGGING_LOG_TO_FILE` | `false` | Whether we should log to a file |
| `ISRV_LOGGING_LOG_IPS` | `true` | Log uploaders IP |
| `ISRV_LOGGING_ANONYMIZE` | `false` | Minimal-logging mode: drop successful request logs and omit all identifying fields (supersedes `logIps`) |
| `ISRV_LOGGING_PATH` | - | Sets the log file path |
| `ISRV_RANDOM_ID_LENGTH` | `12` | Sets the length of randomly generated file IDs |
| `ISRV_MAX_FILE_SIZE_MB` | `512` | Sets the maximum file size in megabytes |
| `ISRV_CLEANUP_ENABLED` | `true` | Enable the job that removes expired files |
| `ISRV_CLEANUP_INTERVAL` | `1m` | The interval at which the cleanup job runs |
| `ISRV_ADMIN_USERNAME` | - | Admin panel username (enables the panel when set together with the password) |
| `ISRV_ADMIN_PASSWORD` | - | Admin panel password |
| `ISRV_ADMIN_SESSION_SECRET` | - | HMAC key for signing admin session cookies (random if unset) |
| `ISRV_ADMIN_FAILED_LOGIN_LIMIT` | `5` | Max failed admin login attempts per hour before the IP is blocked |
| `ISRV_ADMIN_FAILED_LOGIN_BLOCK_DURATION` | `12h` | How long an IP is blocked after exceeding the failed login limit |

### Reverse proxy

isrv does not terminate TLS, so production deployments normally run behind a
reverse proxy. See [docs/reverse-proxies/nginx.md](docs/reverse-proxies/nginx.md) for a complete NGINX server
block and guidance on client IP detection, upload/download buffering, and
which endpoints to keep private.

### Anonymity-focused deployments

For running isrv as a Tor onion service, along with the no-logs `anonymize`
logging mode and the privacy trade-offs involved, see [docs/tor.md](docs/tor.md).

### Observability endpoints

When the server is running, the following infrastructure endpoints are always available:

| Endpoint | Description |
|----------|-------------|
| `GET /healthz` | Liveness probe; always returns `200 {"status":"ok"}` |
| `GET /readyz` | Readiness probe; returns `200` when the database and storage backend are both reachable, otherwise `503` with a per-check error map |
| `GET /metrics` | Prometheus scrape endpoint |

## Admin Panel

A single-administrator panel lets you view, search, preview and delete uploaded files. It is reachable at `/admin`

The panel is disabled unless both an admin username and password are configured.

```yaml
admin:
  username: "admin"
  password: "change-me"
  # Optional: HMAC key for signing session cookies. If omitted, a random key is
  # generated at startup and existing sessions are invalidated on every restart.
  sessionSecret: ""
  # Optional: how long a login session stays valid (default 24h).
  sessionTtl: 24h
  # Max failed login attempts per hour before the offending IP is blocked.
  failedLoginLimit: 5
  # How long to block IPs that exceed the failed login limit (backs off
  # exponentially for repeated offenses).
  failedLoginBlockDuration: 12h
```

## Development

### Building

Build for current platform:
```bash
make build
```

### Testing

Run tests:
```bash
make test
```

Run tests with coverage:
```bash
make test-coverage
```

### Development workflow

Spin up a development server with hot reload:
```bash
make dev
```

## AI Usage

I believe AI disclosure is important, and as such I should say that this project does make use of AI tools.

Test cases, documentation and some code snippets have been generated with the help of tools such as Claude Code and GitHub Copilot. Furthermore, code reviews are currently performed by Coderabbit.

Contributions from the community are welcome. However, please disclose any AI usage in your pull requests. Contributions deemed to be AI generated might be  rejected if they do not meet the quality standards of the project.