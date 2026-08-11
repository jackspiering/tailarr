# Development

## Build

```bash
go build -o bin/tailarr ./cmd/tailarr
```

With version injection:

```bash
go build -ldflags "-X github.com/jackspiering/tailarr/internal/version.Version=0.1.0" \
  -o bin/tailarr ./cmd/tailarr
```

## Test

```bash
go test ./...
go test -race ./...
go test -count=1 ./internal/security/...
```

Lifecycle unit tests fake Docker Compose via an internal `composeFn` hook;
no daemon is required for `go test ./...`.

## Lint (local)

```bash
gofmt -w .
go vet ./...
# optional, version matches CI; includes staticcheck and gofmt checks
golangci-lint run
```

## Run against temporary paths

```bash
export TAILARR_CONFIG_PATH=/tmp/tailarr-dev/tailarr.conf
export TAILARR_REPO_PATH=/tmp/tailarr-dev/scaletail
export TAILARR_DEPLOY_PATH=/tmp/tailarr-dev/stacks
export TAILARR_AUTHKEYS_PATH=/tmp/tailarr-dev/authkeys.conf
export TAILARR_LOG_PATH=/tmp/tailarr-dev/tailarr.log
mkdir -p /tmp/tailarr-dev/stacks /tmp/tailarr-dev
# Point repo at fixtures:
./bin/tailarr --repo-path ./testdata/scaletail --no-refresh list
./bin/tailarr --config "$TAILARR_CONFIG_PATH" doctor
```

## Package notes

- Prefer `internal/` over `pkg/` unless an API must be importable.
- Keep CLI wiring thin; business logic stays in packages with unit tests.
- TUI (`internal/ui`) should call the same packages as CLI commands.

## Conventional Commits

Examples: `feat:`, `fix:`, `docs:`, `chore:`, `test:`, `refactor:`.
Optional scope: `feat(deploy): restore data dirs on redeploy`.
