# Security Policy

## Supported versions

Security fixes target the latest release on `main` and the most recent
tagged SemVer release when releases exist.

## Reporting a vulnerability

Please report security issues privately. Do not open a public GitHub issue
for vulnerabilities that could put operators at risk.

Preferred contact: open a private security advisory on the GitHub repository
if available, or contact the maintainer listed on the GitHub profile for
[jackspiering/tailarr](https://github.com/jackspiering/tailarr).

Include:

- Tailarr version
- A clear description of the issue and impact
- Steps to reproduce (without real secrets)
- Whether a public PoC already exists

## Operator guidance

- Never put Tailscale auth keys or other secrets on flags or prompts that echo
  input. Enter them at hidden prompts (TUI/terminal) or from a file.
- Never put credentials in `TAILARR_REPO_URL` (https userinfo is rejected).
- Keep `authkeys.conf`, config, and deployment `.env` files mode `600`.
- Treat the ScaleTail git clone as trusted input: Compose files and images
  run on your host.
- Review backups under `.tailarr_backups` (they may contain secrets).
- Only Tailarr-managed deployments (with `.tailarr.compose.yaml` marker) are
  updated/stopped/removed.
- Prefer a pinned install script URL or `TAILARR_VERSION=...` when using
  `curl | sh`. The install script verifies release asset checksums.

## Project rules

- Config and env files are parsed as plain text only (never `source` / `eval`
  of user content).
- Secrets are redacted in log output where practical.
- CI must not require or print real credentials.
