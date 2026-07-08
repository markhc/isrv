# isrv – Agent Instructions

`isrv` is a lightweight, anonymous, temporary file-sharing service written in Go. It compiles to a single static binary (CGO disabled) and supports local, S3-compatible, and Google Cloud Storage backends.

## Essential Commands

```bash
make build          # build static binary → build/isrv
make test           # run all tests
make lint           # golangci-lint
make fmt            # gofmt
make test-coverage  # generates coverage.html
```

Run tests for a specific package: `go test ./internal/app/handlers/...`

## Architecture

```
cmd/                        # CLI entrypoint (cobra)
docs/                       # Documentation files (in markdown format)
  notepad/                  # git-ignored place to store project notes
internal/
  app/
    application.go          # Server lifecycle, dependency wiring
    routes.go               # Fiber (gofiber/fiber/v3) router, middleware, endpoint mapping
    handlers/               # HTTP handlers (closure-based DI pattern)
    middleware/             # Rate limiting (incl. admin failed-login blocking), token auth, admin session auth
  configuration/            # YAML config + ISRV_* env var overrides
  database/                 # Database interface + SQLite (modernc/sqlite) and PostgreSQL implementations
  storage/                  # Storage interface + LocalStorage + S3 + GCS
  cleanup/                  # Background worker: deletes expired files
  encryption/               # age-based encryption-at-rest for stored files
  logging/                  # zap-based structured logging; supports an anonymize (no-logs) mode
  telemetry/                # OpenTelemetry metrics only (no tracing/OTLP logs) + Prometheus exporter
  models/                   # Shared model types
web/
  src/                      # React frontend source
    i18n/                   # Internationalization (i18next) setup and locale JSON
  public/                   # Static assets (favicon, icons, etc.)

```

## Key Conventions

### Comments
Keep comments concise and relevant. Avoid repeating what the code does; focus on why it does it.
Do not use `// TODO` or `// FIXME` comments; instead, create a GitHub issue and reference it in the comment. 

### Handler Pattern
Handlers are closures that return `fiber.Handler`, enabling dependency injection:
```go
func Upload(db database.Database, store storage.Storage, ...) fiber.Handler {
    return func(c fiber.Ctx) error { ... }
}
```

### Interfaces and Mocks
`database.Database` and `storage.Storage` are the two core interfaces. Mocks are generated with mockery:
```bash
go generate ./internal/database/...
go generate ./internal/storage/...
```
Mocks live in `internal/database/mocks/` and `internal/storage/mocks/`. Use these in tests; never use real DB/storage in unit tests.

When adding tests that need interface mocks, generate them with mockery (`.mockery.yml` + `go generate ./internal/<pkg>/...`) and keep them in the `<pkg>/mocks/` sub-package — do not hand-write `type MockX struct{ mock.Mock }` doubles, unless they are sufficiently trivial, or if wiring them up for use with mockery (see "How to apply" below) requires considerable effort.

**How to apply:** Because `mocks/` imports the package under test, in-package test files cannot import it (import cycle in test). So interfaces to be mocked must be exported, their signature types exported, and the tests written in an external `package <pkg>_test` with an exported injection point (see `GCSStorage.Client` in [[gcs-backend-design]]).


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
- **GCS + S3 SDK**: There is a native GCS backend (`storage.type: gcs`); prefer it over GCS's S3-compatibility layer. If GCS must be used through the AWS S3 SDK anyway, set `Region: "auto"` and `UsePathStyle: true`.
- **Manual rollback in Upload**: If storage succeeds but DB insert fails, the handler manually deletes the stored file. Keep this pattern consistent if adding new upload logic.
- **Telemetry is metrics-only by design**: traces and OTLP log export are deliberately not configured (request-level records would carry client IPs, filenames, timestamps). Do not reintroduce tracing without revisiting that privacy tradeoff.
- **Admin login rate limiting**: `RateLimitFailedLogins` (internal/app/middleware/ratelimit.go) only spends budget on requests the handler rejects with 401; successful/non-credential attempts are free. Keep this pattern if touching admin auth.
- **Logging anonymize mode**: `logging.anonymize: true` drops successful request logs and omits identifying fields (IP, user agent, path, filename, host); it supersedes `logIps`. New log call sites should go through the existing helpers rather than logging identifying fields directly.

## Frontend

Frontend-specific rules live in `web/AGENTS.md`. Read that file before editing `web/`, React components, or frontend tests.

## Commits / PRs

- Conventional commits: `feat(scope):`, `fix(scope):`, etc.
- Keep commits focused; split backend/frontend when practical.
- Never add AI advertising/attribution/co-author lines.
- PRs need clear summary, testing checklist, and screenshots for visual UI changes.
