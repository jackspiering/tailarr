# Tailarr

Deploy and manage [ScaleTail](https://github.com/tailscale-dev/ScaleTail) Docker
Compose services from a TUI.

[![CI](https://github.com/jackspiering/tailarr/actions/workflows/ci.yml/badge.svg)](https://github.com/jackspiering/tailarr/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go&logoColor=white)](go.mod)
[![Version](https://img.shields.io/badge/version-0.2.0-informational)](CHANGELOG.md)

## Quick start

### One-liner (recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/jackspiering/tailarr/main/scripts/install.sh | sh
```

Options:

```bash
# Pin the binary version (default: latest GitHub release)
TAILARR_VERSION=v0.1.0 curl -fsSL https://raw.githubusercontent.com/jackspiering/tailarr/main/scripts/install.sh | sh

# Install without root (default falls back here if /usr/local/bin is not writable)
INSTALL_DIR="$HOME/.local/bin" curl -fsSL https://raw.githubusercontent.com/jackspiering/tailarr/main/scripts/install.sh | sh
```

The script detects OS/arch, downloads the matching release asset, verifies
`SHA256SUMS`, and installs `tailarr` to `/usr/local/bin` when writable, else
`~/.local/bin`. Re-running it also upgrades an existing install.

Then:

```bash
tailarr    # interactive TUI (requires a TTY)
```

If you see a Python-style `usage: tailarr [-h]` / a "Packet Wizard" TUI instead,
another older `tailarr` is earlier on your `PATH` (often
`~/.local/bin/tailarr` ahead of `/usr/local/bin/tailarr`). Check with
`type -a tailarr`.

The installer prefers to **replace the first `tailarr` on PATH** when that
directory is writable, so a re-run of the one-liner upgrades the binary you
actually invoke. You can also:

```bash
# use the Go binary explicitly
/usr/local/bin/tailarr

# or retire a legacy user install
mv ~/.local/bin/tailarr ~/.local/bin/tailarr.legacy
hash -r
```

### Upgrade an existing install

When installed from a release binary, Tailarr can upgrade itself from the
**Maintenance** menu (**Upgrade Tailarr**): it checks for a newer release,
verifies the SHA256, and replaces the running binary. The check compares
SemVer; the binary is replaced atomically only after the release asset's
SHA256 matches the published `SHA256SUMS`. Installs from `go install` are not
upgraded in place; rebuild instead.

### Manual binary

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

On first real deploy, create dirs under `/opt/tailarr` and `/opt/docker/stacks`
(or override paths). See [Configuration](#configuration).

## Features

| Area | Description |
| --- | --- |
| Catalog | Lists ScaleTail services (`compose.yaml` / `compose.yml` / `docker-compose.y{a,}ml` + `.env`) |
| Lifecycle | Deploy, update, stop, restart, repair, remove (Compose-backed, confirmations, backups) |
| Auth keys | Named `TS_AUTHKEY` store (add/rename/replace/remove, mode `600`, redacted listings) |
| Status | Overview, managed/other counts, container health, running ScaleTail-style names |
| Deploy env | Interactive prompts for empty/placeholder env values; reusable stored keys for multi-service deploys |
| Safety | Name checks, symlink refusal, backups, mode-600 secrets, path bounds, ownership-aware locks |
| Doctor | Dependencies, paths, Docker/Compose reachability (safe write probe) |
| UI | Hierarchical Bubble Tea menus (Status / Services / Keys / Config / Maintenance); multi-select batch ops |

## Usage

### Interactive TUI

```bash
tailarr
```

Tailarr is a TUI-only application: run it inside a terminal. It exits with an
error otherwise.

Main menu: **Status**, **Services**, **Tailscale Authentication Keys**,
**Configuration**, **Maintenance**. Arrow keys move, Enter selects,
`q` or Esc backs out / quits. Number keys jump to items. Multi-select deploy
and lifecycle actions use space to toggle, `a` for all, then Run.

- **Services > Search available services** lists the ScaleTail catalog.
- **Services > Refresh catalog** clones or pulls the ScaleTail templates.
- **Maintenance > Run doctor checks** verifies the host, paths, and Docker.
- **Maintenance > Upgrade Tailarr** self-upgrades a release binary.

First interactive run can create and optionally edit the config file.

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
| `TAILARR_LOG_MAX_BYTES` | Log rotation size (default `5242880`) |
| `TAILARR_ASSUME_YES` | `1` = auto-confirm default-yes prompts |

Precedence: defaults, then config file, then environment.

Safety notes:

- Service names: `^[A-Za-z0-9][A-Za-z0-9_.-]*$` without `..`
- Refuse symlink config/auth/deploy/template write boundaries
- Atomic writes for config, auth keys, and `.env` (secrets mode `600`)
- Backups before destructive replace/repair/remove
- Per-service locks and a repo lock for git refresh
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
