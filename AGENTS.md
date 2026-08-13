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
| `internal/prompt` | Interactive prompts/confirmation |
| `internal/security/*` | Names, paths, atomic write, redaction |
| `internal/logging` | Redacted file logging |
| `internal/version` | SemVer string (ldflags-friendly) |
| `internal/upgrade` | Release self-update |
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

## Release safety

Release tags use strict SemVer with a leading `v`: `vMAJOR.MINOR.PATCH`,
optionally followed by a standard SemVer prerelease suffix, build suffix, or
both (`-PRERELEASE`, `+BUILD`, or `-PRERELEASE+BUILD`). Suffix identifiers are
dot-separated ASCII alphanumerics or hyphens, and numeric prerelease
identifiers cannot have leading zeroes. No other tag names are supported.

The tagged commit must already be merged into and reachable from the default
branch, `main`. Do not tag a feature branch, an unmerged commit, or a commit
that merely appears in a pull request. Tag creation/push and GitHub release
creation/publication are different operations. The safe default for an AI
coding agent is read-only: prepare the metadata and a pull request, but never
create or push a release tag, dispatch the release workflow, approve its
environment, or publish a release. Each such action requires explicit owner
approval; approval to create a tag does not by itself approve publication.

Before a tag is approved, check that `internal/version/version.go`, the README
version badge, `CHANGELOG.md`, and the `scripts/install.sh` fallback all carry
the intended version. The release workflow then runs the full repository CI
gates, verifies the binaries and release notes, and uploads a bundle containing
the validated assets and notes. Its publication job downloads that bundle,
expects SHA256 checksums and provenance attestations for release assets, and,
after the protected `release` environment gate, creates a draft release. A
human must review the draft and manually publish it only after that review.

The protected `release` environment is configured in repository Settings, not
created by workflow YAML. An owner must create/select that environment and set
required reviewers and any other protection rules there. The workflow's
environment reference only invokes those repository protections; it cannot
invent the reviewer list. See [CONTRIBUTING.md](CONTRIBUTING.md#releases) for
the step-by-step release procedure.

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
