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

## TODO

This project is a work in progress, here's a list of things I am working on in no particular order:

- More remote storage options (GCS, FTP)
- Support PostgreSQL database
- Compress files at rest to save storage when convenient (text and other highly compressible formats)
- Optional file encryption at rest
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
isrv --makeconf

# Starts the webserver (will load config file if it exists)
isrv

# Starts the webserver with a specific config file
isrv -c config.yaml
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
| `ISRV_STORAGE_PATH` | - | Sets the storage base path |
| `ISRV_LOGGING_FILE_ENABLED` | `true` | Whether we should log to a file |
| `ISRV_LOGGING_IPS_ENABLED` | `true` | Log uploaders IP |
| `ISRV_LOGGING_UPLOADS_ENABLED` | `true` | Log uploads |
| `ISRV_LOGGING_PATH` | - | Sets the log file path |
| `ISRV_RANDOM_ID_LENGTH` | `12` | Sets the length of randomly generated file IDs |
| `ISRV_MAX_FILE_SIZE_MB` | `512` | Sets the maximum file size in megabytes |
| `ISRV_CLEANUP_ENABLED` | `true` | Enable the job that removes expired files |
| `ISRV_CLEANUP_INTERVAL` | `1m` | The interval at which the cleanup job runs |
| `ISRV_TELEMETRY_ENABLED` | `false` | Enable OpenTelemetry traces and metrics export |
| `ISRV_ADMIN_USERNAME` | - | Admin panel username (enables the panel when set together with the password) |
| `ISRV_ADMIN_PASSWORD` | - | Admin panel password |
| `ISRV_ADMIN_SESSION_SECRET` | - | HMAC key for signing admin session cookies (random if unset) |

When telemetry is enabled, configure the exporter using the standard [OTEL environment variables](https://opentelemetry.io/docs/specs/otel/configuration/sdk-environment-variables/):

| Variable | Description |
|----------|-------------|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Base URL of the OTLP receiver (e.g. `https://otlp-gateway-prod-us-east-0.grafana.net/otlp`) |
| `OTEL_EXPORTER_OTLP_HEADERS` | Comma-separated `key=value` auth headers (e.g. `Authorization=Basic <base64(id:token)>`) |
| `OTEL_SERVICE_NAME` | Overrides the `service.name` resource attribute |
| `OTEL_RESOURCE_ATTRIBUTES` | Additional resource attributes (e.g. `deployment.environment=production`) |
| `OTEL_TRACES_SAMPLER` | Trace sampler. Defaults to `parentbased_always_on`; set to `parentbased_traceidratio` for head-based sampling |
| `OTEL_TRACES_SAMPLER_ARG` | Sampling ratio in `[0.0, 1.0]` used by ratio samplers (e.g. `0.1` for 10%) |

> Beyond `ISRV_TELEMETRY_ENABLED`, all telemetry-related tuning (endpoint, auth headers, sampling, resource attributes, etc.) is controlled exclusively through the `OTEL_*` environment variables above — the YAML `telemetry` section intentionally exposes nothing else.

### Observability endpoints

When the server is running, the following infrastructure endpoints are always available and excluded from tracing/RED-metrics noise:

| Endpoint | Description |
|----------|-------------|
| `GET /healthz` | Liveness probe; always returns `200 {"status":"ok"}` |
| `GET /readyz` | Readiness probe; returns `200` when the database and storage backend are both reachable, otherwise `503` with a per-check error map |
| `GET /metrics` | Prometheus scrape endpoint exposing all OpenTelemetry-recorded metrics in OpenMetrics format |

When `debug: true` is set in the configuration, the standard Go `net/http/pprof` handlers are additionally mounted under `/debug/pprof/`. This endpoint exposes process internals and **must not** be reachable in untrusted environments without an upstream authentication layer.

## Admin Panel

A single-administrator panel lets you view, search, preview and delete uploaded files. It is reachable at `/admin` and is intentionally **not** linked from the home page.

The panel is disabled unless both an admin username and password are configured, either in the `admin` section of the configuration file or via the `ISRV_ADMIN_USERNAME` / `ISRV_ADMIN_PASSWORD` environment variables. When disabled, all `/admin/api/*` routes return `404`.

```yaml
admin:
  username: "admin"
  password: "change-me"
  # Optional: HMAC key for signing session cookies. If omitted, a random key is
  # generated at startup and existing sessions are invalidated on every restart.
  sessionSecret: ""
  # Optional: how long a login session stays valid (default 24h).
  sessionTtl: 24h
```

Features:

- Sign in with the configured credentials; the backend issues a signed, HTTP-only session cookie.
- List, search (by file name, content type or uploader IP address), sort and paginate uploaded files.
- Preview images and text files inline; delete files with confirmation.

The admin interface is code-split from the main bundle, so its JavaScript and translations are only downloaded when the `/admin` route is visited.

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

Format, lint, test, and build:
```bash
make dev
```

## License

See LICENSE file for details.
