# Tailarr

Tailarr deploys and manages [ScaleTail](https://github.com/tailscale-dev/ScaleTail)
Compose services from a TUI.

[![CI](https://github.com/jackspiering/tailarr/actions/workflows/ci.yml/badge.svg)](https://github.com/jackspiering/tailarr/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white)](go.mod)
[![Version](https://img.shields.io/badge/version-0.5.0-informational)](CHANGELOG.md)

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

The script detects your OS and architecture.
It downloads the matching release asset.
It verifies `SHA256SUMS`.
It then installs `tailarr` in this order:

- The directory of the first `tailarr` on `PATH`, if that directory is writable
  (this replaces a legacy install)
- `/usr/local/bin`
- `~/.local/bin`

Run the script again to upgrade an existing install.

Then:

```bash
tailarr    # interactive TUI (requires a TTY)
```

If you see a Python-style `usage: tailarr [-h]`, another `tailarr` is first on
your `PATH`.
If you see a "Packet Wizard" TUI, the same problem applies.
The older binary is often `~/.local/bin/tailarr`, ahead of
`/usr/local/bin/tailarr`.
Check with `type -a tailarr`.

The installer replaces the first `tailarr` on `PATH` when that directory is
writable.
Run the one-liner again to upgrade the binary that you invoke.

To retire a legacy install, move it aside.
Then run the Go binary by full path:

```bash
mv ~/.local/bin/tailarr ~/.local/bin/tailarr.legacy
hash -r
/usr/local/bin/tailarr
```

## Upgrade

Run the install one-liner again. See [Quick start](#quick-start).
The script replaces an existing install in place.

A release-binary install can also upgrade from the TUI.
Open **Maintenance > Upgrade Tailarr**.
Tailarr checks GitHub for a newer release (SemVer).
It verifies the release asset SHA256 against the published `SHA256SUMS`.
It then replaces the running binary with an atomic write.

`go install` builds do not upgrade in place.
Rebuild them. See [Other install methods](#other-install-methods).

## Other install methods

### Manual release binary

Download a release asset for your OS and architecture from
[releases](https://github.com/jackspiering/tailarr/releases). Then:

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

- The TUI needs a terminal. Tailarr does not run without a TTY.
- Deploy and lifecycle actions need Docker with Compose v2.
  **Maintenance > Run doctor checks** verifies the host, paths, and
  Docker/Compose.
- Root is not required. Set `INSTALL_DIR` to choose the install path.
  See [Quick start](#quick-start).

## Features

| Area | Description |
| --- | --- |
| Catalog | Lists ScaleTail services that have a Compose file and a `.env` file. Names: `compose.yaml`, `compose.yml`, `docker-compose.yml`, `docker-compose.yaml`. |
| Lifecycle | Deploy, apply, stop, restart, and remove. Compose runs the actions. Tailarr asks for confirmation. It makes backups. |
| Auth keys | A named `TS_AUTHKEY` store. You can add, rename, replace, or remove a key. The file mode is `600`. Listings are redacted. |
| Status | Shows deployed services, running services, Docker, and config. |
| Deploy env | Tailarr prompts for empty or placeholder env values. You can reuse a stored auth key on more than one service. |
| Safety | Includes name checks, symlink refusal, backups, mode-600 secrets, path bounds, and ownership-bound locks. |
| Doctor | Checks the host, paths, and Docker/Compose reachability. |
| UI | Menus: Status, Services, Tailscale Authentication Keys, Configuration, Maintenance. You can multi-select for batch deploy and lifecycle actions. |

## Usage

```bash
tailarr
```

Tailarr is a TUI-only program. Run it inside a terminal.
Without a TTY, Tailarr prints
`Tailarr is interactive; run inside a terminal.`
to stderr.
It then exits 1.

Main menu:

- **Status**
- **Services**
- **Tailscale Authentication Keys**
- **Configuration**
- **Maintenance**

Keys:

- Arrow keys move.
- Enter selects.
- `q` or Esc goes back or quits.
- Number keys jump to an item.

For multi-select deploy and lifecycle actions:

- Space toggles a row.
- `a` selects all.
- Run starts the action.

**Apply** copies catalog template files onto a managed service directory.
It then pulls images and starts the containers.
It keeps files that exist only in that directory, including `.env`.
**Deploy** only creates a new service directory.

Catalog and maintenance:

- **Services > Search available services** lists the ScaleTail catalog.
- **Services > Refresh catalog** clones or pulls the ScaleTail templates.
- **Maintenance > Run doctor checks** verifies the host, paths, and
  Docker/Compose.
- **Maintenance > Upgrade Tailarr** upgrades a release binary.

On first run, Tailarr offers to create a config file if none exists.
You can edit that file before you continue.

## Configuration

<details>
<summary>Paths, environment variables, permissions</summary>

Config is plain text (`KEY=VALUE`).
The parser reads lines.
It never runs a shell.
It never evaluates the file.

Default paths:

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

Precedence, from lowest to highest:

1. Built-in defaults
2. Config file
3. Environment

On first real deploy, create the directories under `/opt/tailarr` and
`/opt/docker/stacks`.
Or set the path overrides above.

Safety:

- A service name must match `^[A-Za-z0-9][A-Za-z0-9_.-]*$`.
- A service name must not contain `..`.
- Tailarr refuses a write that crosses a symlink at a config, auth, deploy, or
  template boundary.
- Tailarr writes config, auth keys, and `.env` files atomically.
- Secret files use mode `600`.
- Tailarr makes a backup before apply or remove.
- Each service has an ownership-bound lock.
- Git refresh uses a repo lock.
- Treat the ScaleTail clone as trusted input. Compose runs on your host.

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
