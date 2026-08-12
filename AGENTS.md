# Tailarr agent guide

Read this before changing the repository. For humans and AI coding agents.

## What this repository is

Tailarr is a Go application that deploys Docker Compose services from the
[ScaleTail](https://github.com/tailscale-dev/ScaleTail) template repository.
It is a TUI-only program: the binary opens a Bubble Tea TUI and has no
subcommands. All logic lives in `internal/` packages.

There is no daemon and no cloud control plane. Operators run the binary next
to Docker.

This is a from-scratch Go application. Do not assume legacy Bash sources
live in this tree unless they are added as explicit reference material.

## Layout

| Path | Purpose |
| --- | --- |
| `cmd/tailarr` | `main` (TUI entrypoint) |
| `internal/ui` | Bubble Tea models, first-run setup |
| `internal/config` | Plain-text config load/save |
| `internal/authkeys` | Named TS_AUTHKEY store (mode 600) |
| `internal/scaletail` | Catalog discovery, git refresh, env merge |
| `internal/deploy` | Compose lifecycle, locks, backups |
| `internal/doctor` | Host readiness checks |
| `internal/security/*` | Names, paths, atomic write, redaction |
| `internal/logging` | Redacted file logging |
| `internal/version` | SemVer string (ldflags-friendly) |
| `testdata/` | Fixtures for unit tests |
| `.github/` | CI and templates |

Module path: `github.com/jackspiering/tailarr`.

## Non-negotiable rules

- Never log secrets. Redact before writing logs.
- Never accept secrets through CLI flags or TUI flags; prompts or files only.
- Config is plain `KEY=VALUE`. Parse with a scanner; never `source`/`eval`
  user-controlled files (and do not shell out to interpret them).
- Prefer safe filesystem ops: atomic writes, refuse symlink write escapes,
  validate service names, back up before destructive replace/repair.
- ScaleTail git clone is trusted input; still harden git protocol flags where
  practical.
- Plain ASCII in Markdown (no curly quotes, em dashes, decorative unicode).
- Do not add a web UI or encrypt auth keys at rest unless the owner asks.
- Do not create GitHub releases/tags unless the owner asks.

## Versioning

- Version lives in `internal/version/version.go` (`Version` variable).
- Override at build: `-ldflags "-X github.com/jackspiering/tailarr/internal/version.Version=X.Y.Z"`.
- Keep README version badge (when present) and CHANGELOG in sync for releases.

## Testing

```bash
go test -race ./...
```

Unit tests must pass without Docker. Optional integration tests may use
`//go:build integration` and skip cleanly when the daemon is absent.

## Git workflow

- Branches: `feat/`, `fix/`, `docs/`, `chore/` from `main`.
- Conventional Commits; one logical change per commit.
- Prefer small PRs after the initial bootstrap.
- Never force-push unless asked.
- Never commit secrets.

## Before you claim done

```bash
go test -race ./...
go test -race -tags integration ./...
go vet ./...
go build -o bin/tailarr ./cmd/tailarr
./bin/tailarr
gofmt -l .
golangci-lint run
go mod tidy && git diff --exit-code go.mod go.sum
govulncheck ./...
rumdl check .
```

## Exit behavior

The binary is interactive-only: when stdin/stdout is not a terminal it prints
"Tailarr is interactive; run inside a terminal." and exits 1. No sysexits exit
codes exist; there are no subcommands to signal.
