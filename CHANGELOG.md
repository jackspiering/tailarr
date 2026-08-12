# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- Force-replace restore no longer deletes a sibling service named
  `{service}.old`; the partial tree is parked under `.tailarr_backups`.
- Backup list/prune/latest match the exact timestamped name, so `web` cannot
  pick up or delete `web-ui` backups (and cannot inherit the wrong
  `TS_AUTHKEY`).
- SIGTERM/SIGINT during `docker compose` returns an error so a force replace
  can restore the previous deployment instead of killing Tailarr mid-swap.
- Unix lock reclaim treats `EPERM` as a live owner and uses `flock` so a
  shared deploy root cannot steal another operator's lock.
- Compose YAML fallback no longer treats nested keys (`ports:`,
  `environment:`) as service names.
- Editing configuration in the TUI applies the new paths immediately, not
  only after restart.
- Interactive prompts reuse one `bufio.Reader` so pasted answers are not
  dropped.
- Compose output flushes a trailing partial line through the redactor.
- Compose project names hash the deploy root so long service names cannot
  collide across roots.
- Catalog refresh runs off the TUI update loop.
- Storing an auth key during deploy takes the same lock as the Authkeys menu.
- Repair restores previous compose files when `compose up` fails.
- Service health matches exact `app-`/`tailscale-` names or the
  `tailarr.service` label (not a Docker substring filter).
- `LooksSecret` matches `_`-delimited keywords so `TIMEOUT` is not treated
  as a token.
- Config files reject invalid `TAILARR_REPO_URL` values the same way env
  overrides do.
- First-run config save failures are returned instead of ignored.
- Remove/replace confirms default to no.
- Offline (non-git) catalog refresh reports that pull was skipped.
- Self-upgrade validates release tags and caps download size.

## [0.3.0] - 2026-08-12

### Added

- **Maintenance > Upgrade Tailarr**: self-update for release binaries;
  checks GitHub for a newer SemVer release, verifies the release asset
  SHA256 against the published SHA256SUMS, then atomically replaces the
  running binary.

### Security

- In-TUI prompts hand the terminal over to the prompt
  (ReleaseTerminal/RestoreTerminal) so secrets are never raced or echoed by
  the TUI input reader.
- Redaction coverage extended to URL userinfo and JSON/colon secret forms.
- Compose subprocess env filtering and redacted output.
- Backup pruning: the newest 2 backups are kept.
- Stale-lock reclaim when the owning PID is dead.
- Atomic restore.
- Git operation timeouts.
- Config BOM/symlink/URL-validation hardening.
- Authkeys fd-based chmod and parent-directory fsync.
- Install script temp-file and PATH-probe hardening.
- CI action SHA pinning and safe version interpolation.

### Fixed

- Install script warns when another `tailarr` is earlier on `PATH` (legacy
  installs) and prints next steps using the full path to the Go binary.
- Install script prefers replacing the first `tailarr` on `PATH` when that
  directory is writable (e.g. `~/.local/bin` ahead of `/usr/local/bin`).
- TUI auth key add/rename/replace/remove now serializes read-modify-write
  with the authkeys lock, matching the former CLI behavior.

### Removed

- `--repo-ref` / `TAILARR_REPO_REF`: ScaleTail ref pinning removed; the
  catalog clone always tracks the repository default branch.
- TUI `j`/`k` navigation keys; use arrow keys or number shortcuts.
- CLI removed: Tailarr is TUI-only. Subcommands (`list`, `deploy`, `doctor`,
  `upgrade`, `authkeys`, ...), global flags, and `internal/cli` /
  `internal/exitcode` are gone; Cobra, pflag, and mousetrap dependencies
  dropped. The TUI gains a **Services > Refresh catalog** action that clones
  or pulls the ScaleTail templates.

## [0.2.0] - 2026-08-11

### Added

- Hierarchical interactive menus: Status, Services, Auth keys, Configuration,
  Maintenance, with multi-select batch lifecycle actions.
- Interactive deploy env prompts for empty/placeholder values; shared auth key
  for batch deploys; store/paste flow for `TS_AUTHKEY`.
- Status overview with managed counts and container health classification.
- Compose override labels (`tailarr.managed`, `tailarr.service`, `tailarr.version`).
- Confirmations for replace/remove; `--yes` / `TAILARR_ASSUME_YES` support.
- First-run config create/edit; interactive `config` / `config edit`.
- Auth key rename (CLI + TUI); remove can offer to delete retained backups.
- One-liner install script (`scripts/install.sh`) with OS/arch detection and
  SHA256 verification of release assets.

### Fixed

- Docs no longer link to the private legacy Bash prototype repository.

## [0.1.0] - 2026-08-11

First public Go release of Tailarr (`github.com/jackspiering/tailarr`).

### Added

- Initial Go scaffolding (CLI + Bubble Tea TUI).
- Config load/save (plain `KEY=VALUE`, env and flag overrides, atomic write, mode 600).
- Path and service-name validation, symlink refusal (including ancestry), atomic writes.
- Auth key store (mode 600, `tskey-auth-*` validation, redacted listings, RMW lock).
- ScaleTail catalog discovery (`list` / `deployed`).
- Doctor checks for commands, paths, and Docker reachability (exclusive write probe).
- Deploy / repair / update / stop / restart / remove with locks, backups, managed-only lifecycle.
- `deploy --authkey <name>` resolves empty `TS_AUTHKEY` from the store (never the secret on flags).
- Unit tests for pure logic including force-redeploy secret preservation and fail-closed remove (no Docker required).
- Binary-first install story documented in README.

### Security

- Reject repository URLs with embedded credentials; redact userinfo in config display.
- Auth key interactive input uses hidden terminal read; non-interactive input is single-line and size-bounded.
- Force redeploy merges backup `.env` secrets; restores previous deployment if copy/up fails.
- Remove fails closed when `compose down` fails (directory left intact).
- Lifecycle ops refuse unmanaged directories (no Tailarr marker).
- Locks are ownership-bound (PID+token); Release does not steal another process's lock.
- Compose project names include deploy-root fingerprint to reduce cross-root collisions.

### Fixed

- Log rotation runs on every event (not once per process).
- Service locks live under `deployPath/.tailarr_locks` (consistent with backups).
- Git commit SHA pins clone/checkout without invalid `--branch` usage; detached HEAD can rejoin default branch for unpinned pull.
- golangci-lint / misspell CI (errcheck, empty branches, US `Canceled` exit code name).

[Unreleased]: https://github.com/jackspiering/tailarr/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/jackspiering/tailarr/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/jackspiering/tailarr/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/jackspiering/tailarr/releases/tag/v0.1.0
