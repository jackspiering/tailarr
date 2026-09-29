# Tailarr

Tailarr deploys and manages [ScaleTail](https://github.com/tailscale-dev/ScaleTail)
Compose services from a TUI.

[![CI](https://github.com/jackspiering/tailarr/actions/workflows/ci.yml/badge.svg)](https://github.com/jackspiering/tailarr/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white)](go.mod)
[![Version](https://img.shields.io/badge/version-0.8.0-informational)](CHANGELOG.md)

## Quick start

### One-liner (recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/jackspiering/tailarr/main/scripts/install.sh | sh
```

Options:

```bash
# Pin the binary version (default: latest GitHub release)
curl -fsSL https://raw.githubusercontent.com/jackspiering/tailarr/main/scripts/install.sh | TAILARR_VERSION=v0.8.0 sh

# Install without root (default falls back here if /usr/local/bin is not writable)
curl -fsSL https://raw.githubusercontent.com/jackspiering/tailarr/main/scripts/install.sh | INSTALL_DIR="$HOME/.local/bin" sh
```

Set these variables on `sh`, not on `curl`.
A variable set before `curl` does not reach the installer.

The script detects your OS and architecture.
It downloads the matching release asset.
It verifies `SHA256SUMS`.
When the GitHub CLI (`gh`) is installed and logged in, it also verifies the GitHub build attestation.
It then installs `tailarr` in this order:

- The directory of the first `tailarr` on `PATH`, if that directory is writable
  (this replaces a legacy install)
- `/usr/local/bin`
- `~/.local/bin`

Run the script again to upgrade an existing install.

Then start the TUI:

```bash
tailarr
```

### Another tailarr on PATH

If you see a Python-style `usage: tailarr [-h]` or a "Packet Wizard" TUI, another `tailarr` is first on your `PATH`.
The older binary is often `~/.local/bin/tailarr`, ahead of `/usr/local/bin/tailarr`.
Run `type -a tailarr` to list each one.

To retire the older binary, move it aside.
Then run Tailarr by its full path:

```bash
mv ~/.local/bin/tailarr ~/.local/bin/tailarr.legacy
hash -r
/usr/local/bin/tailarr
```

## Upgrade

Run the install one-liner again. See [Quick start](#quick-start).
The script replaces an existing install in place.

A release-binary install can also upgrade from the TUI.
Open the **System** tab and press `U`.
Tailarr checks GitHub for a newer release (SemVer).
It verifies the release asset SHA256 against the published `SHA256SUMS`.
When `gh` is installed and logged in, it also verifies the GitHub build attestation and stops if that check fails.
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
  The **System** tab runs doctor checks on the host, paths, and
  Docker/Compose.
- The install does not need root. Set `INSTALL_DIR` to choose the install path.
  See [Quick start](#quick-start).
- Deploy, Apply, Stop, and Restart work for a user in the `docker` group.
  Remove copies and deletes container data.
  Containers such as the Tailscale sidecar write that data as root.
  Run Tailarr as root for Remove.

## Features

| Area | Description |
| --- | --- |
| Catalog | Lists ScaleTail services that have a Compose file and a `.env` file. Names: `compose.yaml`, `compose.yml`, `docker-compose.yml`, `docker-compose.yaml`. |
| Lifecycle | Deploy, apply, stop, restart, and remove. Compose runs the actions. Tailarr asks for confirmation. It makes backups. Compose output shows in the TUI. |
| Auth keys | A named `TS_AUTHKEY` store. You can add, rename, replace, or remove a key. The file mode is `600`. Listings are redacted. |
| Status | Shows each deployed service with its health, containers, and status. The list refreshes every 4 seconds. |
| Deploy env | Tailarr prompts for empty or placeholder env values. You can reuse a stored auth key on more than one service. |
| Safety | Includes name checks, symlink refusal, backups, mode-600 secrets, path bounds, and ownership-bound locks. |
| Doctor | Checks the host, paths, Docker/Compose reachability, and the TUN device. |
| UI | Tabs: Services, Catalog, Keys, System. You can pick more than one row for batch deploy and lifecycle actions. Prompts open inside the TUI. |

## Usage

```bash
tailarr
```

Tailarr is a TUI-only program. Run it inside a terminal.
Without a TTY, Tailarr prints
`Tailarr is interactive; run inside a terminal.`
to stderr.
It then exits 1.

The TUI has four tabs:

| Tab | What it shows | Keys |
| --- | --- | --- |
| Services | Each deployment, with a health dot, running containers, and status | `r` restart, `s` stop, `A` apply, `X` remove, `enter` actions, `d` catalog |
| Catalog | ScaleTail templates, with the image, port, and the env values that deploy asks for | `enter` deploy, `/` filter, `r` refresh catalog |
| Keys | Stored auth keys (redacted) | `a` add, `e` rename, `p` replace, `x` remove |
| System | Paths, version, and doctor checks | `e` edit config, `d` run doctor, `U` upgrade |

The Services tab opens first.
The details panel on the right shows the row under the cursor.
The details panel shows when the terminal is at least 92 columns wide.
Command output opens in an output panel at the bottom.
Prompts open in a panel above the footer.
The footer lists the keys for the current tab.

Keys on every tab:

- Tab and Shift+Tab switch tabs. Number keys `1` to `4` open a tab.
- Arrow keys or `j` and `k` move. `g` and `G` jump to the first or last row.
- PgUp and PgDn scroll the output panel. Home and End jump to the top or bottom.
- Esc clears the filter, then the output.
- Ctrl+C cancels a running action. When no action runs, Ctrl+C or `q` quits.

On the Services and Catalog tabs, you can act on more than one service:

- Space picks a row. `a` picks all rows. `n` clears the picks.
- `/` filters the list as you type. Picked rows stay in the list.
- The action runs on the picked rows.
  When no row is picked, the action runs on the row under the cursor.

In a prompt, Enter submits and Esc cancels.
When a prompt shows a default, typing replaces it.
Backspace edits the default instead.
Tailarr shows a secret, such as `TS_AUTHKEY`, as dots only.

**Apply** copies catalog template files onto a managed service directory.
It then pulls images and starts the containers.
It keeps your `.env` values and adds the keys that the template adds.
It keeps files that exist only in that directory.
Only **Deploy** creates a new service directory.

Tailarr shows a service as stopped when all its containers exited, for example after Stop.
It shows a service as exited when a container restarts in a loop, or exits while another container runs.

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
| `TAILARR_ASSUME_YES` | `1` or `true` auto-confirms default-yes prompts |

Precedence, from lowest to highest:

1. Built-in defaults
2. Config file
3. Environment

Tailarr creates a missing directory when it first needs it.
A user that is not root cannot create directories under `/opt`.
In that case, create `/opt/tailarr` and `/opt/docker/stacks` and give your user write access.
Or set the path overrides above.

Safety:

- A service name must match `^[A-Za-z0-9][A-Za-z0-9_.-]*$`.
- A service name must not contain `..`.
- Tailarr refuses a write that crosses a symlink at a config, auth, deploy, or
  template boundary.
- Config, deploy, log, and auth key paths must be absolute.
- Tailarr writes config, auth keys, and `.env` files atomically.
- Secret files use mode `600`.
- Before apply, Tailarr saves the files that apply changes: template files, `.env`, and the managed override.
  A failed apply puts them back in place and starts the service again. Container data is not copied or moved.
- Before remove, Tailarr copies the whole deployment to `.tailarr_backups`. The copy keeps modes and times, and owners when
  Tailarr runs as root. It skips sockets and FIFOs.
- Each service has an ownership-bound lock.
- Git refresh uses a repo lock.
- Treat the ScaleTail clone as trusted input. Compose runs on your host.

</details>

## Development

[CONTRIBUTING.md](CONTRIBUTING.md) lists the setup and the checks to run before you push.
[AGENTS.md](AGENTS.md) describes the architecture and the conventions.
Commits use [Conventional Commits](https://www.conventionalcommits.org/).

## License

MIT. See [LICENSE](LICENSE). Copyright (c) 2026 Jack Spiering.
