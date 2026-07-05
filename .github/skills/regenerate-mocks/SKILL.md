---
name: regenerate-mocks
description: "Regenerate mockery mocks after interface changes. Use when: adding or removing methods from database.Database or storage.Storage interfaces, creating a new interface that needs a mock, or when mock files are out of sync with their interfaces."
argument-hint: "Optional: name of the interface that changed (e.g. 'Database', 'Storage')"
---

# Regenerate Mocks

Regenerates type-safe mocks for the `database.Database` and `storage.Storage` interfaces using [mockery](https://vektra.github.io/mockery/).

## When to Use

- After adding, removing, or changing a method signature on `Database` or `Storage`
- When mock compilation errors reference missing or mismatched methods
- When adding a new interface that should have a mock

## Procedure

### 1. Verify mockery is available

```bash
go tool mockery --version
```

If missing: `go get -tool github.com/vektra/mockery/v3@latest`

### 2. Regenerate mocks

Run `go generate` in the package whose interface changed:

| Interface changed | Command |
|---|---|
| `database.Database` | `go generate ./internal/database/...` |
| `storage.Storage` | `go generate ./internal/storage/...` |
| Both | `go generate ./internal/database/... ./internal/storage/...` |
| All at once | `go generate ./...` |

### 3. Verify output

Generated mocks land in:

| Interface | Mock file |
|---|---|
| `database.Database` | `internal/database/mocks/mock_Database.go` |
| `storage.Storage` | `internal/storage/mocks/mock_Storage.go` |

Check the file was updated (timestamp or diff) and compiles cleanly:

```bash
go build ./internal/database/mocks/... ./internal/storage/mocks/...
```

### 4. Update tests

If a method was **added or renamed**, find call sites in tests:

```bash
grep -r "mock_Database\|mock_Storage\|mocks\." internal/ --include="*_test.go" -l
```

Update `On(...)` / `EXPECT()` calls to match the new signature. Run tests to confirm:

```bash
make test
```

## Configuration

Mockery is configured in [.mockery.yml](../../../.mockery.yml). Key settings:

- `dir: '{{.InterfaceDir}}/mocks'` — mocks co-located with the interface package
- `filename: 'mock_{{.InterfaceName}}.go'`
- `structname: 'Mock{{.InterfaceName}}'`
- `template: testify` — generates `testify/mock`-compatible mocks

## Adding a New Interface

1. Add `//go:generate go tool mockery` at the top of the source file (after the `package` line)
2. Add the package + interface to [.mockery.yml](../../../.mockery.yml) under `packages:`
3. Run `go generate ./<package path>/...`
