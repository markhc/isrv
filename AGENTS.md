# isrv – Agent Instructions

`isrv` is a lightweight, anonymous, temporary file-sharing service written in Go. It compiles to a single static binary (CGO disabled) and supports local and S3-compatible storage backends.

## Essential Commands

```bash
make build          # build static binary → build/isrv
make test           # run all tests
make lint           # golangci-lint
make fmt            # gofmt
make dev            # fmt + lint + test + build (pre-commit workflow)
make test-coverage  # generates coverage.html
make bench          # run benchmarks (see docs/benchmarking.md)
```

Run tests for a specific package: `go test ./internal/app/handlers/...`

## Architecture

```
cmd/                        # CLI entrypoint (cobra)
internal/
  app/
    application.go          # Server lifecycle, dependency wiring
    routes.go               # chi router, middleware, endpoint mapping
    handlers/               # HTTP handlers (closure-based DI pattern)
    middleware/             # Rate limiting, token auth, file ID validation
  configuration/            # YAML config + ISRV_* env var overrides
  database/                 # Database interface + SQLite implementation (modernc/sqlite)
  storage/                  # Storage interface + LocalStorage + S3
  cleanup/                  # Background worker: deletes expired files
  telemetry/                # OpenTelemetry tracing + Prometheus metrics
  models/                   # Shared model types
web/
  src/                      # React frontend source
    i18n/                   # Internationalization (i18next) setup and locale JSON
  public/                   # Static assets (favicon, icons, etc.)

```

## Key Conventions

### Handler Pattern
Handlers are closures that return `http.HandlerFunc`, enabling dependency injection:
```go
func Upload(db database.Database, store storage.Storage, ...) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) { ... }
}
```

### Interfaces and Mocks
`database.Database` and `storage.Storage` are the two core interfaces. Mocks are generated with mockery:
```bash
go generate ./internal/database/...
go generate ./internal/storage/...
```
Mocks live in `internal/database/mocks/` and `internal/storage/mocks/`. Use these in tests; never use real DB/storage in unit tests.

### Testing
- Table-driven tests with `testify/assert` and `testify/require`
- Test files are co-located with the code they test
- Handler tests in `internal/app/handlers/handlers_test.go` use a local `testdata/` directory for templates

### Error Handling
Wrap errors with `fmt.Errorf("context: %w", err)`. Do not log and return — choose one.

### Configuration
Fields map to env vars via the `ISRV_` prefix (e.g., `server.port` → `ISRV_SERVER_PORT`). See [internal/configuration/default_config.yaml](internal/configuration/default_config.yaml) for all defaults.

## Paths / Security

isrv runs on Linux/Unix-like hosts primarily but should be portable.

- Local filesystem paths: `filepath.Join`, `filepath.Clean`, `filepath.Rel`.
- Slash-delimited formats only for URLs and API payloads.
- Validate all external input at API boundaries.

## Potential Pitfalls

- **Static builds**: `CGO_ENABLED=0` is required. Do not introduce CGO dependencies. The SQLite driver (`modernc.org/sqlite`) is CGO-free by design.
- **Build info injection**: Version, commit, date are injected via ldflags at build time (see Makefile). Do not read them from files at runtime.
- **GCS + S3 SDK**: If using Google Cloud Storage with the AWS S3 SDK, set `Region: "auto"` and `UsePathStyle: true`. See user memory for details.
- **Manual rollback in Upload**: If storage succeeds but DB insert fails, the handler manually deletes the stored file. Keep this pattern consistent if adding new upload logic.

## Frontend

Frontend-specific rules live in `web/AGENTS.md`. Read that file before editing `web/`, React components, or frontend tests.

## Commits / PRs

- Conventional commits: `feat(scope):`, `fix(scope):`, etc.
- Keep commits focused; split backend/frontend when practical.
- Never add AI advertising/attribution/co-author lines.
- PRs need clear summary, testing checklist, and screenshots for visual UI changes.
