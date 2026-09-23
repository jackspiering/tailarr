# Tailarr Functional Check - 2026-09-23

**Commit checked:** `4bcab24` (`ci: parallelize gates, ship patched Go, and audit workflows (#48)`), branch `main`, version `0.5.3`.

**Host:** Linux amd64, Docker Engine 29.8.1, Docker Compose v5.5.1, git 2.54.0, `/dev/net/tun` available.
Local Go 1.26.0 (the `go.mod` floor) for the gates. Go 1.27.1 (the CI `GO_VERSION`) for govulncheck and the tested binary.

**Method:** Ran every gate from AGENTS.md. Then drove the real binary in tmux against real Docker and the live ScaleTail catalog.
All paths lived under a scratch root through `TAILARR_CONFIG_PATH` and first-run setup. Deployments used a reusable, pre-approved
Tailscale auth key with a locked-down tag, so the sidecars joined a real tailnet and reached `healthy`. Two services were used:
`cyberchef` and `it-tools` (small, stateless). All test containers, networks, directories, and the key file were removed afterwards.

**Operator modes:** The TUI ran first as a non-root user in the `docker` group (the README states root is not required), then as
root inside a `docker:cli` container with the host socket and identical bind paths. Root was needed to finish the lifecycle tests,
see F2.

**Overall:** Deploy, Apply, Remove, backups, rollback, locking, secret handling, and validation work well against real Docker and a
real tailnet. No secret reached the log, the TUI, or any file with a mode wider than 600. Two defects break core lifecycle actions
for common setups: **Restart takes a running ScaleTail service down** (F1), and **lifecycle actions fail for a non-root
operator** (F2). One defect leaves a running sidecar and a stale deployment after Ctrl+C during a first deploy (F3).

## Summary table

| # | Severity | Location | Finding |
| --- | --- | --- | --- |
| F1 | High | internal/deploy/deploy.go:542-553 | Restart uses `compose restart`; the app loses the sidecar network namespace and stays exited |
| F2 | High | internal/deploy/deploy.go:670, 102 | Stop, Restart, Apply, Remove fail for a non-root operator: symlink walk hits root-owned `ts/state` |
| F3 | Medium | internal/deploy/deploy.go:115-124, compose.go:121 | Ctrl+C during first deploy: cleanup `down` inherits the canceled context; sidecar keeps running |
| F4 | Medium | internal/ui/app.go:1057-1100 | Multi-select has no scrolling: 122 catalog rows, Run/Cancel off-screen, blind toggles hit wrong rows |
| F5 | Low | internal/ui/app.go:581-596 | Search has no query and prints all 122 names; a 50-row terminal shows about 29 |
| F6 | Low | internal/ui/app.go:549 | Deployed services view drops tab separators: `cyberchefmanaged[healthy]` |
| F7 | Low | internal/ui/app.go:1040, internal/deploy | A failed compose shows only `exit status 1`; the cause scrolls away; failures are not logged |
| F8 | Low | internal/ui/app.go:981-997 | Refresh on an up-to-date clone prints `Catalog refreshed.` then `Already up to date.` |
| F9 | Info | internal/ui/app.go:354-364, 390-400 | `q`, `esc`, and `0` quit from the main menu with no prompt; help line says `q/esc back` |
| F10 | Info | internal/ui/app.go:272 | "Docker and config summary" promises Docker access details but shows overview plus config only |
| F11 | Info | go.mod | govulncheck on the Go 1.26.0 floor reports 17 stdlib advisories; clean on 1.27.1 |
| F12 | Info | GitHub releases, scripts/install.sh:21 | Latest published release is v0.4.0; v0.5.0/v0.5.1 are drafts; no v0.5.3 tag exists |

## Gate results

| Gate | Result |
| --- | --- |
| `go test -race ./...` | Pass, 14 packages |
| `go test -race -tags integration ./...` | Pass |
| `go vet ./...` | Pass |
| `gofmt -l .` | Clean |
| `go build -o bin/tailarr ./cmd/tailarr` | Pass |
| `rumdl check .` | Pass, 10 files |
| `golangci-lint run` | 0 issues |
| `go mod tidy && git diff --exit-code go.mod go.sum` | Clean |
| `govulncheck ./...` (Go 1.26.0) | 17 stdlib advisories, all fixed in 1.26.x patch releases (F11) |
| `govulncheck ./...` (Go 1.27.1) | No vulnerabilities found |

## Functional results

| Area | Check | Result |
| --- | --- | --- |
| Entry | No TTY (`</dev/null`, piped stdin, `--help`) | Pass: prints `Tailarr is interactive; run inside a terminal.`, exit 1 |
| First run | Create config, edit defaults, save | Pass: config written mode 600 with all six keys |
| Config | View | Pass |
| Config | Edit with `file:///etc` repo URL | Pass: rejected, nothing saved |
| Config | Edit with credential URL `https://user:pass@...` | Pass: rejected, credential not echoed |
| Config | Edit with relative deploy path | Pass: rejected, nothing saved |
| Doctor | All checks on a fresh root | Pass: git, docker, compose, daemon ok; missing dirs and catalog warned |
| Catalog | Refresh (clone) | Pass: 122 services discovered |
| Catalog | Refresh (pull, already current) | Pass, wording nit (F8) |
| Catalog | Search | Works, see F5 |
| Auth keys | Add with valid key via secret prompt | Pass: no echo, store mode 600, list shows `[redacted]` |
| Auth keys | Add with value not starting `tskey-auth-` | Pass: rejected |
| Auth keys | Rename missing key / invalid name / valid name | Pass: `not found` / charset error / renamed |
| Auth keys | Replace value | Pass |
| Auth keys | Remove, answer No | Pass: canceled, key kept |
| Deploy | Two services, one shared stored key | Pass: both sidecars joined the tailnet, both apps `healthy` |
| Deploy | Files on disk | Pass: `.env` 600 with key, managed override with `tailarr.*` labels, log 600 |
| Deploy | Already deployed service | Pass: `service already deployed: cyberchef (use Apply)` |
| Deploy | Pasted value not starting `tskey-auth-` | Pass: rejected before any file or container exists |
| Deploy | Well-formed but invalid key | Pass: sidecar unhealthy, `up` fails, containers and directory removed; message weak (F7) |
| Deploy | Ctrl+C while compose waits on the sidecar | **Fail** (F3) |
| Status | Overview, Deployed, Running, Summary | Pass: counts and health correct, exited state shown after Stop; F6 cosmetic |
| Stop | Non-root operator | **Fail** (F2) |
| Stop | Root | Pass: both containers exited, log event written |
| Restart | Stopped service (root) | **Fail** (F1): app exits 128 |
| Restart | Running, healthy service (root) | **Fail** (F1): app exits 128, service left down |
| Apply | Two services, confirm each | Pass: backups created, both apps back to `healthy`, no key prompt, node kept its tailnet IP |
| Apply | Backup contents | Pass: backup dir 700, `.env` 600, Tailscale state included |
| Apply | Forced failure (image does not exist) | Pass: pull fails, running containers untouched, files restored byte-identical, event logged |
| Remove | Answer No | Pass: `remove canceled`, containers untouched |
| Remove | Answer Yes, then delete backups | Pass: `down` ran, directory and backups deleted, events logged |
| Remove | Clean up the stale deployment left by F3 | Pass |
| Locking | Second Tailarr instance stops a service mid-deploy | Pass: waits about 30 s, then `another Tailarr process holds the lock` |
| Upgrade | Maintenance > Upgrade | Pass: `Already up to date (0.5.3)` (published latest is v0.4.0, see F12) |
| Installer | `INSTALL_DIR=<tmp> sh scripts/install.sh` | Pass: resolves latest v0.4.0, checksum OK |
| Secrets | Log file, TUI output, files on disk | Pass: no key material outside `.env` and the key store (see note below) |

Note on the test driver: once, the tmux driver fell out of step with the prompts and typed the auth key into the plain-text
`New name:` prompt of Rename key. The terminal echoed it, as a line prompt should. The rename did not complete (the TUI showed
`Canceled.`) and the key store was unchanged.
This was a driver error, not a Tailarr defect. The tmux session and its scrollback were destroyed afterwards.

## Findings

### F1 - High - Restart takes a running ScaleTail service down

`Manager.Restart` runs `docker compose -p <project> restart`. 119 of 122 ScaleTail templates run the app with
`network_mode: service:tailscale`. Compose restarts both containers. The app then tries to join the network namespace of the
old sidecar and fails:

```text
app-it-tools        Exited (128)
tailscale-it-tools  Up (healthy)
cannot join network namespace of a non running container: container tailscale-it-tools is exited
```

Reproduced on a running, healthy service and on a stopped service, three times in total. The TUI reports
`docker compose failed ... restart: exit status 1` and leaves the app down. Apply (`up -d`) brings it back.

Suggested fix: replace `restart` with `up -d --force-recreate` (or `stop` followed by `up -d`). This lets compose honor
`depends_on: condition: service_healthy` and re-attach the app to the new sidecar namespace. The unit tests fake `composeFn`,
so they cannot catch this. An integration test with a two-container `network_mode: service:` fixture would.

### F2 - High - Lifecycle actions fail for a non-root operator

As a non-root user in the `docker` group, Stop, Restart, Apply, and Remove all fail with:

```text
error: open /tmp/fc/root/stacks/cyberchef/ts/state: permission denied
```

`requireManagedDeploy` (deploy.go:670) and the pre-deploy check (deploy.go:102) call `paths.ContainsSymlinks(dest)`. It walks
the whole deployment tree, including container-owned data. The Tailscale sidecar creates `ts/state` as root with mode 700, so
the walk fails on the first lifecycle action after any deploy. The README Requirements section states "Root is not required".
That is true for install and Deploy, but not for anything after Deploy.

Related risk, not reproduced: the same walk runs over all app data (media libraries, databases) on every Stop and Restart. It
also refuses the action if any container writes a symlink into its own data directory, which many images do.

Options for the owner, from smallest to largest change:

1. Document that lifecycle actions need root, or ownership of the deployment tree.
2. Limit the symlink walk to the paths Tailarr writes (template files, `.env`, override), not container data.
3. Treat `fs.ErrPermission` inside the tree as "not inspectable" and skip that subtree, keeping the top-level checks.

### F3 - Medium - Ctrl+C during a first deploy leaves a running sidecar

Steps: deploy a service, and press Ctrl+C while compose shows `Container tailscale-<svc> Waiting`. Tailarr exits (exit code 0).
The log shows:

```text
warning: compose down after failed deploy of it-tools: operation interrupted: docker compose ... down --remove-orphans: context canceled
```

`DeployWith` runs the cleanup `down` through `defaultCompose`, which derives its context from `interrupt.Context()`. That context
is already canceled, so the cleanup exits at once. The result:

- The sidecar keeps running with `restart: always`. With a bad key it retries forever. With a good key it is a live tailnet node.
- The app container stays in `Created`.
- The deployment directory, including `.env` with the auth key, stays on disk.
- The error text says `kept ... so Remove can clean up`, but the TUI has already quit, so the operator never sees it.

Remove on the next run cleans up correctly. Suggested fix: run the cleanup `down` on a fresh context with a short timeout
(for example `context.WithoutCancel` plus `context.WithTimeout`), so it runs even after an interrupt. Apply is not affected in
the same way: its restore is a file rename and does not need compose.

### F4 - Medium - Multi-select has no scrolling

The Deploy picker lists all 122 catalog services, then `Run on selection` and `Cancel`. The view has no viewport. On a
160x50 terminal it shows rows 1 to 45. The cursor, the selection marks below row 45, the Run and Cancel rows, and the status
line are all off-screen. Digits only reach rows 1 to 9.

During this check a blind navigation landed one row short of Run. Enter then toggled `xwiki` on instead of starting the deploy.
An operator can deploy a service they did not intend. Suggested fix: a scrolling window around the cursor, a count such as
`2 selected`, and a type-to-filter prompt (which would also cover F5).

### F5 - Low - Search does not search

Services > Search prints every catalog name with no query. On a 50-row terminal about 29 of 122 names are visible and the
list cannot scroll.

### F6 - Low - Deployed services view loses its columns

`fmt.Fprintf(&b, "  - %s\t%s\t[%s]\n", ...)` renders as `cyberchefmanaged[healthy]`. The tab characters do not survive the
Bubble Tea v2 renderer. Use spaces or fixed-width padding.

### F7 - Low - Compose failures lack a cause and are not logged

With a bad key, compose prints `dependency failed to start: container tailscale-it-tools is unhealthy` while the terminal is
released. When the TUI returns, the result shows only
`docker compose failed: docker compose ... up -d --remove-orphans: exit status 1`. There is no hint to check the auth key.

The log file records successes (`deployed service`, `stopped service`) and Apply restores. It does not record failed Deploy,
Stop, Restart, or Remove. An operator cannot reconstruct a failed session from the log.

### F8 - Low - Refresh wording

When the clone is current, git prints `Already up to date.`, so `runCatalogRefresh` returns
`Catalog refreshed.` followed by `Already up to date.`. The `Catalog is up to date.` branch only runs when git prints
nothing.

### F9 - Info - Quit keys on the main menu

`q`, `esc`, and `0` exit Tailarr at once from the main menu. The help line reads `q/esc back`. During this check a second `esc`
closed the app. Consider a confirm prompt, or changing the help text to `q/esc back or quit`.

### F10 - Info - Summary label

The Status item "Docker and config summary" is described as "Review Docker access and configuration". It shows the overview
plus the config. It does not report Docker access. The doctor check covers that.

### F11 - Info - Go floor and stdlib advisories

`go.mod` declares `go 1.26.0`. govulncheck on 1.26.0 reports 17 reachable stdlib advisories in `crypto/tls`, `crypto/x509`,
`net/http`, `net/url`, `net/textproto`, `net`, `encoding/asn1`, and `os`. All are fixed in 1.26.x patch releases. Release
binaries use 1.27.1 and are clean. People who build from source on the floor toolchain get the vulnerable stdlib. Consider
raising the floor to the newest 1.26 patch.

### F12 - Info - Release state

- Published "Latest" release: v0.4.0. v0.5.0 and v0.5.1 are drafts. No v0.5.2 or v0.5.3 tag exists.
- `main` declares 0.5.3 in `version.go`, the README badge, CHANGELOG, and `install.sh` `DEFAULT_VERSION`.
- The installer resolves the latest release through the API, so it installs v0.4.0 today. If the API call fails, it falls
  back to v0.5.3, which returns 404.
- Operators on v0.4.0 do not receive the 0.5.x fixes until a release is published.

No action was taken. Publishing is an owner decision.

## Not covered

- Self-upgrade that replaces the binary (no newer published release exists).
- Symlink-refusal paths against the live binary (covered by unit tests only).
- Apply failure after `up` has partly recreated containers. Only the pull-failure path was exercised.
- macOS and arm64 builds.
- Terminal resize and color output (tests ran with `NO_COLOR=1`).
