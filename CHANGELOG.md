# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.7.0] - 2026-09-23

### Added

- Services > Search asks for a query and lists the matching services.
- In multi-select, `/` filters the list and `n` clears the selection. Selected services stay visible under any filter.
- Deploy, Stop, and Restart batches ask once before they start and list the selected services.
- Doctor checks for `/dev/net/tun` on Linux. It also notes when Tailarr does not run as root and Remove may need root.
- The installer and Maintenance > Upgrade verify the GitHub build attestation when `gh` is installed and logged in.
  A failed check stops the install. Without `gh`, they print a note.

### Fixed

- A failed Apply no longer breaks the running service. Apply used to swap the whole deployment directory for a backup copy and delete the old one,
  so running containers lost their bind-mounted data. Apply now saves only the files it changes (template files, `.env`, and the managed override)
  and restores them in place. When `docker compose up` had started, Apply runs it again with the restored files.
- Apply no longer reads container data, so it works for a user in the `docker` group.
- Backups keep file modes and modification times, and owners when Tailarr runs as root.
  A restored copy used to be owned by the Tailarr user, and containers that run as another user could not write to it.
- Backups skip sockets and FIFOs. A FIFO in container data used to block Tailarr forever, and a socket made Remove fail.
  Ctrl+C now stops a long backup copy.
- The Remove confirm shows how much data the backup copies.
- Apply asks for confirmation before it takes any lock. Deploy and Apply release the catalog lock before env prompts.
  A second Tailarr instance no longer times out while the first waits for an answer.
- Env prompts for keys outside the template come in a stable, sorted order.
- Catalog refresh fails at once when git needs a password or an SSH host key confirmation. Git could not read the prompt and waited 5 minutes.
  The error now says to configure a credential helper or SSH agent.
- Catalog refresh refuses to pull when the clone tracks a different repository than `TAILARR_REPO_URL`. It used to keep pulling the old origin.
- Ctrl+C during a batch stops it. The remaining services show `skipped: interrupted` instead of running one by one.
- Failed Deploy, Apply, Stop, Restart, and Remove actions are written to the log.
- A multi-service deploy rejects an unknown stored key name and an invalid pasted key before it starts.
- Configuration edits no longer save `TAILARR_*` environment overrides to the config file. The prompt lists the overridden settings.
- Catalog refresh reports `Catalog is up to date.` when git has nothing new.
- When Restart stops a service but cannot start it again, the error says that the service is stopped now.
- Remove reports each backup it cannot delete, because a backup can hold secrets. The log shows the real count.

## [0.6.0] - 2026-09-23

### Added

- The TUI has a new layout: a header bar with the current screen, bordered menu and details panels, a selection meter, and a key-hint footer.
  The layout follows the terminal size.
- Long service lists in multi-select scroll with the cursor. The panel border shows the visible range.
- Command output opens in its own panel. Long lines wrap. PgUp, PgDn, Home, and End scroll the output.
- Status tokens such as `[ok]`, `[warn]`, `[fail]`, and container health are colored in the output.

### Fixed

- Values typed at a prompt are quoted in `.env`. Compose no longer reads `$` in a password as a variable or a space followed by `#` as a comment.
- Template `.env` lines keep their original quotes, inline comments, and `${VAR}` references when Tailarr rewrites the file.
- Lock creation backs off when another process takes the new lock first. Two processes can no longer hold the same lock.
- A failed first deploy runs `docker compose down` before it deletes the deployment. If that fails, the deployment is kept so Remove can clean up.
- Restart no longer takes a service down. It runs `docker compose stop`, then `docker compose up -d`, so the app waits for a healthy Tailscale sidecar.
  `docker compose restart` restarted both containers at once, and an app with `network_mode: service:tailscale` exited with code 128.
- Stop and Restart work for a user in the `docker` group. They check only the compose files, the managed override, and `.env`, not container data.
  Apply and Remove still check the whole deployment. When container data is unreadable, the error says to run Tailarr as root.
- Ctrl+C during a first deploy no longer leaves the Tailscale sidecar running. The cleanup `docker compose down` runs even after the interrupt.
- Deployed services show their columns again. Tab characters were dropped in the TUI.

### Changed

- Apply no longer merges the backup `.env`. The deployed `.env` already holds the same values.
- Writing the managed override starts one `docker` process instead of two.
- Release binaries are built with the patched Go toolchain that CI scans (`GO_VERSION`), not the `go.mod` floor.
  Binaries built with Go 1.26.0 reached 17 known standard library vulnerabilities.

## [0.5.3] - 2026-09-21

### Security

- Config, deploy, log, and auth key paths must be absolute. Relative paths are rejected.
- The auth key store refuses a symlink file and a symlink parent.
- Apply does not reuse `TS_AUTHKEY` from an older backup. Only the snapshot for that apply is merged.
- Compose drops `COMPOSE_*` from the process environment. The merged `.env` stays authoritative. `DOCKER_*` is kept.
- Deploy and Apply lock the catalog so a refresh cannot rewrite the tree during the copy.

### Fixed

- A log path chosen in first-run setup is validated. A symlink parent is no longer silent.
- Ctrl-C cancels in-flight compose, git, and prompts, then waits so Apply can restore before exit.
- Status, deployed services, and running containers no longer block the TUI while Docker is queried.
- Invalid `.env` lines are errors. Apply no longer drops them on rewrite.
- Upgrade version compare rejects incomplete versions, leading zeroes, and invalid pre-release or build metadata.

### Changed

- Tailarr now requires Go 1.26 or newer (`golang.org/x/sys` v0.48.0 and `golang.org/x/term` v0.46.0).
- CI fails when the release workflow tool pins differ from CI.
- The release workflow tool pins match CI (Go 1.27.1, rumdl 0.2.75).

## [0.5.2] - 2026-08-25

### Security

- `RedactRepoURL` now redacts passwords that contain `/` when URL parse fails.
- Authkeys > Remove is a default-no prompt. `TAILARR_ASSUME_YES` does not delete a stored key.
- `IsManaged` requires the structured Tailarr marker. A substring of `tailarr.managed` is not enough.

### Fixed

- The config parser strips a UTF-8 BOM on every line, not only the first.

### Changed

- The release workflow `rumdl` pin matches CI (`0.2.60`).
- AcquireLock and Backup no longer call a second `chmod` after `EnsureDirMode`.
- Catalog search, authkeys list, and multi-select errors go through `redact.Text`.

## [0.5.1] - 2026-08-21

### Security

- Redaction now covers suffixed keys (`client_secret`, `access_token`),
  multi-word colon values, and slash-containing URL userinfo;
  credential URLs no longer leak on parse failure.
- `ssh://` userinfo redaction preserves username-only URLs so saved config round-trips correctly.
- Auth key store fail-closes on chmod errors and uses `O_NOFOLLOW` when tightening permissions.

### Fixed

- Config env overrides now trim whitespace before validation.
- Env files handle `export FOO=bar`, per-line BOM, invalid keys, and deterministic write ordering.
- ScaleTail git operations use `WaitDelay` for helper cleanup, allow empty-dir clone, and avoid abandoning detached commits.
- Upgrade fetch uses 120s timeout, strict `owner/repo` validation, and semver rejects extra parts (`1.2.3.4`).
- Logging rotation handles symlinked `.1` and validates log path before use; first-run setup no longer runs before logger.
- Prompts check `s.In` TTY correctly; doctor checks symlink ancestry and reports probe errors accurately.
- Deploy locks use 0700, backup partial cleanup is best-effort,
  compose service names have a timeout, Apply preserves all backup
  secrets and tightens confirmation, and TUI fixes double-Enter,
  cancel routing, redacted errors, and SIGINT handling.

## [0.5.0] - 2026-08-15

### Added

- **Services > Apply catalog** copies catalog template files onto a managed
  stack. It then pulls images and starts the containers. It keeps files that
  exist only in that directory, including `.env`.

### Changed

- Deploy creates only. It refuses an existing service directory and tells you
  to use Apply.
- The TUI uses Bubble Tea v2 and Lip Gloss v2.
  Menus, keys, and drop-to-terminal prompts stay the same.
- `TAILARR_ASSUME_YES` auto-accepts default-yes prompts only.
  Default-no prompts (such as remove) still require an answer.
- Tailarr now requires Go 1.25 or newer.
- Rewrote README prose to the project ASD-STE100 and Zinsser writing rule.

### Removed

- Update, repair, and replace are no longer operator verbs.

## [0.4.0] - 2026-08-13

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
[Unreleased]: https://github.com/jackspiering/tailarr/compare/v0.7.0...HEAD
[0.7.0]: https://github.com/jackspiering/tailarr/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/jackspiering/tailarr/compare/v0.5.3...v0.6.0
[0.5.3]: https://github.com/jackspiering/tailarr/compare/v0.5.2...v0.5.3
[0.5.2]: https://github.com/jackspiering/tailarr/compare/v0.5.1...v0.5.2
[0.5.1]: https://github.com/jackspiering/tailarr/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/jackspiering/tailarr/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/jackspiering/tailarr/compare/v0.3.0...v0.4.0
[0.2.0]: https://github.com/jackspiering/tailarr/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/jackspiering/tailarr/releases/tag/v0.1.0
