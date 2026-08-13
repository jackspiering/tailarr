# Tailarr

Deploy and manage [ScaleTail](https://github.com/tailscale-dev/ScaleTail) Docker
Compose services from a TUI.

[![CI](https://github.com/jackspiering/tailarr/actions/workflows/ci.yml/badge.svg)](https://github.com/jackspiering/tailarr/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go&logoColor=white)](go.mod)
[![Version](https://img.shields.io/badge/version-0.4.0-informational)](CHANGELOG.md)

## Quick start

### One-liner (recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/jackspiering/tailarr/main/scripts/install.sh | sh
```

Options:

```bash
# Pin the binary version (default: latest GitHub release)
TAILARR_VERSION=v0.2.0 curl -fsSL https://raw.githubusercontent.com/jackspiering/tailarr/main/scripts/install.sh | sh

# Install without root (default falls back here if /usr/local/bin is not writable)
INSTALL_DIR="$HOME/.local/bin" curl -fsSL https://raw.githubusercontent.com/jackspiering/tailarr/main/scripts/install.sh | sh
```

The script detects OS/arch, downloads the matching release asset, verifies
`SHA256SUMS`, and installs `tailarr` into the directory of the first `tailarr`
on `PATH` when writable (replacing a legacy install), else `/usr/local/bin`,
else `~/.local/bin`. Re-running it upgrades an existing install.

Then:

```bash
tailarr    # interactive TUI (requires a TTY)
```

If you see a Python-style `usage: tailarr [-h]` or a "Packet Wizard" TUI
instead, another older `tailarr` is earlier on your `PATH` (often
`~/.local/bin/tailarr` ahead of `/usr/local/bin/tailarr`). Check with
`type -a tailarr`. The installer replaces the first `tailarr` on `PATH` when
its directory is writable, so re-running the one-liner upgrades the binary you
actually invoke. To retire a legacy install and invoke the Go binary by full
path:

```bash
mv ~/.local/bin/tailarr ~/.local/bin/tailarr.legacy
hash -r
/usr/local/bin/tailarr
```

## Upgrade

Re-run the install one-liner from [Quick start](#quick-start); it replaces an
existing install in place.

Release-binary installs can also self-upgrade from the TUI. **Maintenance >
Upgrade Tailarr** checks GitHub for a newer release (SemVer comparison),
verifies the release asset's SHA256 against the published `SHA256SUMS`, then
atomically replaces the running binary. Installs from `go install` are not
upgraded in place; rebuild instead (see
[Other install methods](#other-install-methods)).

## Other install methods

### Manual release binary

Download a release asset for your OS/arch from
[releases](https://github.com/jackspiering/tailarr/releases), then:

```bash
chmod +x tailarr-linux-amd64   # example
sudo mv tailarr-linux-amd64 /usr/local/bin/tailarr
tailarr
```

### go install

```bash
go install github.com/jackspiering/tailarr/cmd/tailarr@latest
```

### Build from source

```bash
git clone https://github.com/jackspiering/tailarr.git
cd tailarr
go test ./...
go build -o bin/tailarr ./cmd/tailarr
./bin/tailarr
```

## Requirements

- Interactive TUI: needs a terminal; the binary refuses to run without a TTY.
- Deploy and lifecycle operations: Docker with Compose (Docker Compose v2);
  **Maintenance > Run doctor checks** verifies host, paths, and
  Docker/Compose reachability.
- No root required: `INSTALL_DIR` overrides the install location (see
  [Quick start](#quick-start)).

## Features

| Area | Description |
| --- | --- |
| Catalog | Lists ScaleTail services with `compose.yaml`, `compose.yml`, `docker-compose.yml`, or `docker-compose.yaml` plus `.env` |
| Lifecycle | Deploy, update, stop, restart, repair, remove (Compose-backed, confirmations, backups) |
| Auth keys | Named `TS_AUTHKEY` store (add/rename/replace/remove, mode `600`, redacted listings) |
| Status | Overview, deployed services, running services, Docker and config summary |
| Deploy env | Interactive prompts for empty/placeholder env values; reusable stored keys for multi-service deploys |
| Safety | Name checks, symlink refusal, backups, mode-600 secrets, path bounds, ownership-bound locks |
| Doctor | Host, paths, Docker/Compose reachability |
| UI | Hierarchical TUI menus (Status / Services / Tailscale Authentication Keys / Configuration / Maintenance); multi-select batch ops |

## Usage

```bash
tailarr
```

Tailarr is a TUI-only application: run it inside a terminal. Without a TTY it
prints `Tailarr is interactive; run inside a terminal.` to stderr and exits 1.

Main menu: **Status**, **Services**, **Tailscale Authentication Keys**,
**Configuration**, **Maintenance**. Arrow keys move, Enter selects, `q` or Esc
backs out or quits. Number keys jump to items. Multi-select deploy and
lifecycle actions use space to toggle, `a` for all, then Run.

- **Services > Search available services** lists the ScaleTail catalog.
- **Services > Refresh catalog** clones or pulls the ScaleTail templates.
- **Maintenance > Run doctor checks** verifies host, paths, and
  Docker/Compose reachability.
- **Maintenance > Upgrade Tailarr** self-upgrades a release binary.

On first run, when no config file exists, Tailarr offers to create one from
defaults and optionally edit it.

## Configuration

<details>
<summary>Paths, environment variables, permissions</summary>

Config is plain text (`KEY=VALUE`). The parser reads lines; it never shells
or evaluates the file. Conventional default paths:

| Path | Default |
| --- | --- |
| Config | `/opt/tailarr/tailarr.conf` |
| Auth keys | `/opt/tailarr/authkeys.conf` |
| ScaleTail clone | `/opt/tailarr/scaletail` |
| Deployments | `/opt/docker/stacks` |
| Backups | `/opt/docker/stacks/.tailarr_backups` |
| Log | `/opt/tailarr/logs/tailarr.log` |

| Environment variable | Overrides |
| --- | --- |
| `TAILARR_CONFIG_PATH` | Config file path |
| `TAILARR_REPO_URL` | ScaleTail URL |
| `TAILARR_REPO_PATH` | ScaleTail clone path |
| `TAILARR_DEPLOY_PATH` | Deployment root |
| `TAILARR_LOG_PATH` | Log file |
| `TAILARR_AUTHKEYS_PATH` | Auth keys file |
| `TAILARR_LOG_MAX_BYTES` | Log rotation size (default `5242880`, 5 MiB) |
| `TAILARR_ASSUME_YES` | `1` = auto-confirm default-yes prompts |

Precedence: defaults, then config file, then environment.

On first real deploy, create the directories under `/opt/tailarr` and
`/opt/docker/stacks`, or override the paths above.

Safety notes:

- Service names: `^[A-Za-z0-9][A-Za-z0-9_.-]*$` without `..`
- Refuse symlink config/auth/deploy/template write boundaries
- Atomic writes for config, auth keys, and `.env` (secrets mode `600`)
- Backups before destructive replace/repair/remove
- Ownership-bound per-service locks and a repo lock for git refresh
- ScaleTail clone is **trusted input** (Compose runs on your host)

</details>

## Development

See [CONTRIBUTING.md](CONTRIBUTING.md) and [AGENTS.md](AGENTS.md).

```bash
go test ./...
go vet ./...
```

Commits use [Conventional Commits](https://www.conventionalcommits.org/).

## License

MIT. See [LICENSE](LICENSE). Copyright (c) 2026 Jack Spiering.
