---
name: isrv-workspace
description: "Workspace instructions for the isrv Go file server project. Use when: working on Go code, HTTP handlers, storage operations, database interactions, error handling, testing, or documentation in the isrv codebase."
---

# isrv Project Instructions

## Project Overview
This is `isrv`, a Go-based file upload/download server with support for local and S3 storage backends. The project emphasizes proper context propagation, explicit error handling, and clean HTTP server patterns.

## Go Coding Standards

### Context Propagation
- **ALWAYS** pass `context.Context` as the first parameter to functions that may block or make external calls
- Use `r.Context()` from HTTP requests, never `context.Background()` in request handlers
- Propagate context through the entire call chain: HTTP → Service → Storage → Database
- Example: `storage.SaveFileUpload(ctx context.Context, ...)` not `storage.SaveFileUpload(...)`

### Error Handling
- **Prefer explicit error handling** - always check and handle errors immediately
- Use wrapped errors with context: `fmt.Errorf("failed to save file: %w", err)`
- Return meaningful error messages to help with debugging
- Don't ignore errors - if you must ignore, add a comment explaining why
- For predictable errors, define static error messages using `errors.New`
- Example:
```go
var ErrorFileTooLarge = errors.New("upload limit exceeded")

...

if header.Size > int64(config.MaxFileSizeMB*1024*1024) {
    return ErrorFileTooLarge
}
```

### HTTP Handler Patterns
- Use structured logging with request context
- Return appropriate HTTP status codes
- Handle timeouts and cancellation gracefully
- Validate input before processing
- Always close request bodies and files

### Storage and Database Patterns  
- All storage operations must accept `context.Context`
- Use prepared statements for database queries
- Handle database migrations properly via the `/internal/database/migrations/` folder
- Support both local storage and S3 backends through the unified Storage interface
- Test storage implementations with both backends

## Project Structure
- `/cmd/` - Command-line interface
- `/internal/` - Private application code
  - `/webserver/` - HTTP handlers and middleware
  - `/storage/` - Storage backends (local, S3)
  - `/database/` - Database operations and migrations
  - `/configuration/` - Config management
  - `/logging/` - Structured logging
  - `/cleanup/` - Background cleanup tasks

## Build and Test Commands
- Build: `make build`
- Test: `make test`
- Lint: `make lint`
- Run: `./build/isrv` or `go run .`
- Docker: `docker-compose up` for full environment
- Clean: `make clean` to remove build artifacts

## Documentation Standards
- Document all public functions and types
- Include examples for complex functionality  
- Update README.md when adding new features
- Comment non-obvious business logic
- Use godoc conventions: `// FunctionName does X and returns Y`
- Avoid emotes and lengthy comments

## Dependencies
- Minimize external dependencies where possible
- Prefer standard library solutions
- Keep go.mod clean and up-to-date

## Security Considerations
- Validate all file uploads (size, type, name)
- Sanitize request inputs
- Use secure headers in HTTP responses
- Log security-relevant events
- Handle sensitive configuration data properly

## When Working on This Codebase
1. Always run `go mod tidy` after dependency changes
2. Test with both storage backends before committing
3. Ensure database migrations work in both directions
4. Update configuration documentation when adding new options
5. Consider backwards compatibility for configuration changes

