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
tailarr version    # expect: tailarr 0.1.0 (or newer)
tailarr doctor
tailarr            # interactive TUI when stdout is a TTY
```

If `tailarr version` does not look like that (or you see a Python-style
`usage: tailarr [-h]` / a "Packet Wizard" TUI), another older `tailarr` is
earlier on your `PATH` (often `~/.local/bin/tailarr` ahead of
`/usr/local/bin/tailarr`). Check with `type -a tailarr`.

The installer prefers to **replace the first `tailarr` on PATH** when that
directory is writable, so a re-run of the one-liner upgrades the binary you
actually invoke. You can also:

```bash
# use the Go binary explicitly
/usr/local/bin/tailarr doctor

# or retire a legacy user install
mv ~/.local/bin/tailarr ~/.local/bin/tailarr.legacy
hash -r
```

### Upgrade an existing install

When installed from a release binary, Tailarr can upgrade itself:

```bash
tailarr upgrade           # check for a newer release, verify SHA256, replace the binary
tailarr upgrade --check   # only report whether an upgrade is available
tailarr upgrade --yes     # confirm automatically (for scripts)
```

`--force` reinstalls even when already on the latest release. Point
`TAILARR_UPGRADE_REPO` (or `--repo`) at a fork when needed. The check compares
SemVer; the running binary is replaced atomically only after the release
asset's SHA256 matches the published `SHA256SUMS`. Installs from `go install`
are not upgraded in place; rebuild instead.

### Manual binary

Download a release asset for your OS/arch from
[Releases](https://github.com/jackspiering/tailarr/releases), then:

```bash
chmod +x tailarr-linux-amd64   # example
sudo mv tailarr-linux-amd64 /usr/local/bin/tailarr
tailarr doctor
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
./bin/tailarr version
./bin/tailarr doctor
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
| Deploy env | Interactive prompts for empty/placeholder env values; `--authkey <name>` for stores |
| Safety | Name checks, symlink refusal, backups, mode-600 secrets, path bounds, ownership-aware locks |
| Doctor | Dependencies, paths, Docker/Compose reachability (safe write probe) |
| UI | Hierarchical Bubble Tea menus (Status / Services / Keys / Config / Maintenance); multi-select batch ops |

## Usage

### Interactive TUI

```bash
tailarr
```

Main menu: **Status**, **Services**, **Tailscale Authentication Keys**,
**Configuration**, **Maintenance**. Arrow keys / `j` `k` move, Enter selects,
`q` or Esc backs out / quits. Number keys jump to items. Multi-select deploy
and lifecycle actions use space to toggle, `a` for all, then Run.

First interactive run can create and optionally edit the config file.

### CLI

```bash
tailarr list
tailarr deploy {SERVICE}
tailarr update {SERVICE}
tailarr stop {SERVICE}
tailarr restart {SERVICE}
tailarr remove {SERVICE}
tailarr repair {SERVICE}
tailarr deployed
tailarr running
tailarr doctor
tailarr config
tailarr authkeys list
tailarr logs
tailarr version
```

| Command | Purpose |
| --- | --- |
| `list` | Available ScaleTail services |
| `deployed` | Local deployments |
| `running` | Running Compose project names (best effort) |
| `deploy <service>` | Deploy (`--force` replaces managed; `--authkey <name>` fills empty `TS_AUTHKEY`) |
| `repair <service>` | Refresh templates; keep local `.env` when possible |
| `update` / `stop` / `restart` / `remove` | Lifecycle on **managed** deploys only (`remove` fails closed if compose down fails; `--volumes` drops volumes) |
| `authkeys` | List/add/rename/remove stored keys (values never on flags) |
| `logs` | Print log file path |
| `config` | Interactive edit (TTY), or `config show` / `config write` |
| `doctor` | Host and path checks |
| `version` | Print version |

Global options (before the command):

```bash
tailarr --no-refresh list
tailarr --deploy-path /srv/stacks deployed
tailarr --repo-ref v1.2.3 list
tailarr --yes remove {SERVICE}
```

| Option | Meaning |
| --- | --- |
| `--config <path>` | Config file path |
| `--repo-url <url>` | ScaleTail git URL |
| `--repo-path <path>` | Local ScaleTail clone |
| `--deploy-path <path>` | Deployment root |
| `--log-path <path>` | Log file |
| `--authkeys-path <path>` | Auth keys file |
| `--repo-ref <ref>` | Pin ScaleTail to a branch, tag, or commit |
| `--no-refresh` | Skip ScaleTail clone/pull for list/deploy/repair |
| `--yes` | Auto-confirm default-yes prompts (when prompts exist) |

Secrets are never accepted on CLI flags. Enter them at prompts or pipe a file
to stdin for `authkeys add`.

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
| `TAILARR_REPO_REF` | Pinned ScaleTail ref |
| `TAILARR_LOG_MAX_BYTES` | Log rotation size (default `5242880`) |
| `TAILARR_ASSUME_YES` | `1` = same as `--yes` |

Precedence: defaults, then config file, then environment, then CLI flags.

Safety notes:

- Service names: `^[A-Za-z0-9][A-Za-z0-9_.-]*$` without `..`
- Refuse symlink config/auth/deploy/template write boundaries
- Atomic writes for config, auth keys, and `.env` (secrets mode `600`)
- Backups before destructive replace/repair/remove
- Per-service locks and a repo lock for git refresh
- ScaleTail clone is **trusted input** (Compose runs on your host)

</details>

## Development

See [CONTRIBUTING.md](CONTRIBUTING.md), [AGENTS.md](AGENTS.md), and
[docs/development.md](docs/development.md). Feature checklist:
[docs/parity.md](docs/parity.md).

```bash
go test ./...
go vet ./...
```

Commits use [Conventional Commits](https://www.conventionalcommits.org/).

## License

MIT. See [LICENSE](LICENSE). Copyright (c) 2026 Jack Spiering.

## Thanks

- [ScaleTail](https://github.com/tailscale-dev/ScaleTail) for Compose templates
- [Bubble Tea](https://github.com/charmbracelet/bubbletea) and
  [Cobra](https://github.com/spf13/cobra)
