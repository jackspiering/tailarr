# Repository Guidelines

Guide for AI coding agents working in this repository. For the operator manual see README.md; for process see CONTRIBUTING.md.

## Project Overview

Tailarr is a Go application that deploys Docker Compose services from the
[ScaleTail](https://github.com/tailscale-dev/ScaleTail) template repository.
The binary starts a Bubble Tea TUI and has no subcommands, no daemon, and no
cloud control plane. Operators run it next to Docker on the host.

- Module path: `github.com/jackspiering/tailarr`.
- All logic lives in `internal/`; `cmd/tailarr` only wires dependencies.
- Interactive-only: without a TTY it prints `Tailarr is interactive; run inside a terminal.` and exits 1.

Non-negotiable rules:

- Never log secrets. Route output through `internal/security/redact`.
- Never accept secrets through CLI or TUI flags; prompts or files only.
- Config is plain `KEY=VALUE` parsed with a scanner. Never shell out to interpret user-controlled config.
- Do not add a web UI or encrypt auth keys at rest unless the owner asks.
- Markdown is plain ASCII: no curly quotes, em dashes, or decorative unicode.
- Never create or push release tags, dispatch the release workflow, or publish releases without explicit owner approval.

## Architecture & Data Flow

Two layers. `cmd/tailarr/main.go` (~60 lines) loads config, builds the logger, runs first-run setup, then calls `ui.Run`. Everything else is `internal/*`.

Import direction, arrows point at dependencies:

```text
ui       -> config, deploy, scaletail, authkeys, doctor, prompt, upgrade, logging, version
deploy   -> config, scaletail, authkeys, prompt, logging, version
security -> atomic, paths, redact, names (leaf helpers imported by nearly everything)
```

Deployment flow end to end:

1. `config.Default()` + `config.Load()`: file values, overridden by `TAILARR_*` env vars; defaults under `/opt/tailarr`.
2. Catalog refresh (`internal/scaletail/repo.go`): git CLI clone `--depth 1` or pull `--ff-only`, https/ssh only, 5 minute timeout.
3. Discovery (`scaletail.ListAvailable`): scans `<repo>/services` for directories with `compose.yaml|yml` plus `.env`; symlinked service dirs are skipped.
4. Lifecycle (`deploy.Manager` facade): validates the name
   (`security/names.ValidateServiceName`), merges template `.env` with stored
   values (`ValidateMergedTSAuthkey`), writes env mode 0600 atomically, writes
   managed override `.tailarr.compose.yaml`, takes a pid+flock lock
   (`deploy.AcquireLock`, 30 s default), backs up persistent data to
   `.tailarr_backups` (keep 2), then execs `docker compose` (package var
   `composeFn`) with filtered env and redacted stdio. Failure restores the
   backup.
5. Status (`deploy.CollectOverview`): one `docker ps -a` pass; health groups by `app-` / `tailscale-` name prefixes.
6. Every event appends a redacted line via `logging.Logger.Event` (size rotation, O_NOFOLLOW).

Key architectural patterns:

- TUI concurrency: one Bubble Tea v2 `model` owns state. Blocking work escapes
  the event loop as `tea.Sequence(func() tea.Msg { ... })` commands that call
  `prog.ReleaseTerminal` / `RestoreTerminal`, run prompts or exec directly, and
  return a `resultMsg`.
- Docker integration is subprocess-only: Tailarr execs the `docker compose` CLI. There is no Docker SDK. Git is CLI-only too; there is no go-git.
- Security posture is fail-closed everywhere: refuse symlink parents and
  destinations, redact secrets at every boundary, filter env before the compose
  subprocess, verify checksums during self-upgrade.

## Key Directories

|Path|Purpose|
|---|---|
|`cmd/tailarr/`|Entrypoint: wiring only|
|`internal/ui/`|Bubble Tea model, screens, menus, first-run setup|
|`internal/config/`|KEY=VALUE load/save, `TAILARR_*` overrides|
|`internal/scaletail/`|Catalog discovery, git refresh, env parse/merge/write|
|`internal/deploy/`|Lifecycle facade, compose exec, locks, backups, status|
|`internal/security/atomic/`|Atomic temp-file+rename writes|
|`internal/security/paths/`|Symlink refusal, path containment|
|`internal/security/redact/`|Secret scrubbing for any output|
|`internal/security/names/`|Service/authkey/URL validation|
|`internal/authkeys/`|Named TS_AUTHKEY store, mode 600|
|`internal/prompt/`|Terminal prompts; `UI` interface|
|`internal/doctor/`|Host readiness checks|
|`internal/logging/`|Redacted, rotating file log|
|`internal/upgrade/`|Self-update with checksum verification, SemVer compare|
|`internal/version/`|ldflags-injected `Version` variable|
|`testdata/scaletail/services/`|Fixtures for catalog tests|
|`.github/workflows/`|CI and release pipelines|
|`.grok/rules/`|README writing rule|

## Development Commands

Local gate set (all of these run in CI):

```bash
go test -race ./...
go test -race -tags integration ./...
go vet ./...
gofmt -l .
go build -o bin/tailarr ./cmd/tailarr
./bin/tailarr
rumdl check .
golangci-lint run
go mod tidy && git diff --exit-code go.mod go.sum
govulncheck ./...
```

Version override at build time:

```bash
go build -ldflags "-X github.com/jackspiering/tailarr/internal/version.Version=X.Y.Z" -o bin/tailarr ./cmd/tailarr
```

## Code Conventions & Common Patterns

Error handling:

- Sentinels per domain: `deploy.{ErrAlreadyDeployed, ErrNotDeployed,
  ErrNotManaged, ErrNoCompose, ErrEmptyAuthkey, ErrComposeFailed,
  ErrInterrupted, ErrSymlink}`, `prompt.ErrCanceled`, `upgrade.ErrUpToDate`.
- Wrap with `fmt.Errorf("context: %w", err)`; pair sentinel with detail as `fmt.Errorf("%w: refusing symlink: %s", ErrSymlink, path)`. Match with `errors.Is`.
- `main` prints errors to stderr and exits 1. Logger failures stay silent by design; startup validates separately.

Filesystem safety. Use these instead of raw os calls:

- Writes: `security/atomic.WriteFile` / `WriteFileString` (temp file, chmod, Sync, rename, parent fsync).
- Paths: `security/paths.RefuseSymlinkAncestry`, `JoinUnder`, `Within`, `ContainsSymlinks`; open with `paths.OpenFileNoFollow`.
- Identifiers: validate user-typed names with `security/names` before using them in paths or commands.

Concurrency and locking:

- `deploy.AcquireLock`: O_EXCL file holding pid+token, non-blocking flock on
  top, stale-owner reclaim via `/proc/<pid>/comm`; `Release` never removes a
  live owner's lock.
- Platform code splits by build tags: `lock_unix.go`, `lock_linux.go`, `lock_other.go`, `process_unix.go`, `nofollow_unix.go` and `_other` twins.

Dependency injection:

- Concrete structs wired in `main` and `ui`. The only interface is `prompt.UI` (`Confirm`, `Line`, `Secret`, `Printf`).
- Tests inject fakes through seams, not mocks: swap package var
  `deploy.composeFn`, pass `upgrade.Options{Client, apiBase}` pointing at an
  httptest server, feed `strings.Reader` into `prompt.Std`.

Style:

- Single-letter receivers matching the type: `m *Manager`, `s *Store`, `l *Logger`, `r Result`.
- Small exported surfaces; most logic is unexported within its package.
- Doc comments are complete sentences: `// Save writes all keys atomically with mode 600.`
- errcheck is enabled: handle returned errors; deliberate ignores use `_ =` where failure truly does not matter (deferred Close).
- README prose follows ASD-STE100 plus Zinsser per `.grok/rules/readme-writing.md`:
  short active sentences, imperative steps, fixed vocabulary (`Tailarr`,
  `catalog`, `TUI`), no contractions, no semicolons. rumdl lints all Markdown
  at 160 columns.

Versioning and release safety:

- Version source of truth: `var Version` in `internal/version/version.go`.
- A release requires four locations to agree: `version.go`, README version badge, `CHANGELOG.md` entry (Keep-a-Changelog), `scripts/install.sh` `DEFAULT_VERSION`.
- Tags are strict SemVer `vMAJOR.MINOR.PATCH` with optional `-PRERELEASE`/`+BUILD`, and must be reachable from `main`.
- The release workflow verifies metadata, cross-compiles linux/darwin x
  amd64/arm64, uploads SHA256SUMS, attests provenance, then creates a draft
  release behind a protected `release` environment. A human reviews and
  publishes. Agents prepare metadata and pull requests only.

Git workflow:

- Branches from `main`: `feat/`, `fix/`, `docs/`, `chore/`.
- Conventional Commits, one logical change per commit. Never force-push. Never commit secrets (issue templates warn about `tskey-auth-*`).

## Important Files

|File|Role|
|---|---|
|`cmd/tailarr/main.go`|Entrypoint, non-TTY exit gate|
|`internal/ui/app.go`|TUI model, screens, terminal release/restore|
|`internal/deploy/deploy.go`|DeployWith / Apply / Stop / Restart / RemoveWith|
|`internal/deploy/compose.go`|docker compose exec, project naming, probes|
|`internal/deploy/lock.go`|pid+flock acquisition and reclaim|
|`internal/deploy/backup.go`|Timestamped backups, prune, restore|
|`internal/deploy/errors.go`|Lifecycle sentinel errors|
|`internal/config/config.go`|Config defaults, load/save, env precedence|
|`internal/scaletail/catalog.go`|Service discovery and filtering|
|`internal/scaletail/env.go`|Env merge, placeholder detection, atomic write|
|`internal/scaletail/repo.go`|Hardened git refresh|
|`internal/authkeys/authkeys.go`|TS_AUTHKEY store|
|`internal/security/*`|Validation, paths, atomic write, redaction primitives|
|`internal/version/version.go`|Single version declaration|
|`internal/upgrade/upgrade.go`|Release download, SHA256 verify, binary swap|
|`.github/workflows/ci.yml`|Gate commands and cross-build matrix|
|`.github/workflows/release.yml`|Tag verification and draft publication|
|`scripts/install.sh`|curl/sh installer, checksum verified|
|`.golangci.yml`, `.rumdl.toml`|Lint configs|

## Runtime/Tooling Preferences

- Go: `go.mod` declares the language floor (`go 1.26.0`). CI tests, scans, and builds releases with `GO_VERSION` in the workflows
  (Renovate-bumped), so govulncheck sees the stdlib that ships. The `test-min` job still tests the `go.mod` floor. No separate toolchain directive.
- Direct dependencies stay minimal: `charm.land/bubbletea/v2`,
  `charm.land/lipgloss/v2`, `golang.org/x/sys`, `golang.org/x/term`. No Docker
  SDK, no go-git, no third-party logging or assertion libraries.
- Build tooling: plain `go` commands. No Makefile, no Taskfile, no codegen directives anywhere.
- Dependency updates: Renovate (not Dependabot), weekend schedule, grouped
  updates; custom regex managers pin CI tool versions in ci.yml and
  `DEFAULT_VERSION` in install.sh.
- Binary name `tailarr`; development builds land in `bin/`, release artifacts in `dist/`.

## Testing & QA

- Stdlib `testing` only. About 109 test functions across 12 packages; largest suite is `internal/deploy` (42 tests).
- Guard-clause assertions with `t.Fatal`/`t.Fatalf`; table-driven loops report every row with `t.Errorf`. No testify, no golden files, no `t.Run` subtests.
- Test names read as behavior specs: `TestLoadRefusesSymlinkFile`, `TestApplyRestoresOnInterrupt`, `TestRemoveFailsClosedOnComposeError`, `TestLockReleaseDoesNotSteal`.
- Isolation idioms: `t.TempDir()` for filesystem, `t.Setenv()` for env and
  PATH, `t.Parallel()` throughout security packages, `t.Helper()` and
  `t.Cleanup` for fixture helpers and global-var restoration.
- Unit tests must pass without Docker. Fake compose via the `composeFn` seam or
  a fake `#!/bin/sh docker` script prepended to PATH. Optional daemon-dependent
  tests would use `//go:build integration` and skip cleanly; no such files
  exist yet.
- Platform-specific tests skip at runtime (`runtime.GOOS` checks) instead of build tags.
- Fixtures: only `testdata/scaletail/services/`, consumed solely by
  `internal/scaletail/catalog_test.go`. It covers good services, invalid names,
  missing compose/env, and a symlinked dir that must be skipped. Prefer
  synthesized fixtures in `t.TempDir()` for new tests.
- `-race` is mandatory locally, in CI, and in release verification. No coverage tooling is configured.
- UI tests construct the `model` struct directly, set `NO_COLOR=1`, drive `Update` with `tea.KeyPressMsg`, and assert rendered substrings. No teatest framework.
