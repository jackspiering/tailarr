# Feature parity checklist

Track planned vs implemented operator features. Items marked done are
implemented with tests and/or working CLI paths in this tree.

## Core

- [x] Config + env overrides (plain KEY=VALUE, atomic write)
- [x] Path/service-name validation + symlink/boundary helpers
- [x] Secret redaction in logs (basic patterns)
- [x] Auth key store (mode 600, validate `tskey-auth-*`, redacted UI/CLI)
- [x] Auth key rename / replace / remove (CLI + TUI)
- [x] Doctor (commands, paths, docker/compose reachability; no privilege escalation)
- [x] Catalog discovery (`list`) against local ScaleTail-like tree
- [x] `deployed` listing with managed tag
- [x] Status overview (counts, health, running ScaleTail-style names)
- [x] Hierarchical TUI (Status / Services / Keys / Config / Maintenance)
- [x] Multi-select batch deploy / remove / update / stop / restart / repair
- [x] `version` / `tailarr version`
- [x] Per-service locks (file locks) + repo lock for git refresh
- [x] Backups under `.tailarr_backups` (move/copy) + restore data dirs helper
- [x] Deploy/repair/update/stop/restart/remove wired to Docker Compose
- [x] ScaleTail clone/pull + optional ref pin (branch/tag/commit) + protocol hardening
- [x] Force redeploy preserves `.env` secrets from backup; restores previous deploy on failure
- [x] Remove fails closed if `compose down` fails; only managed deployments are lifecycle targets
- [x] Remove offers to delete retained backups (interactive)
- [x] Deploy resolves empty `TS_AUTHKEY` via `--authkey <name>` store lookup; interactive paste/store
- [x] Interactive prompts for empty/placeholder env values (PUID/PGID/DNS/TZ defaults)
- [x] Confirm prompts for replace/remove; `--yes` / `TAILARR_ASSUME_YES` auto-confirm
- [x] First-run config create/edit on interactive start
- [x] Interactive `config` / `config edit`
- [x] Compose override labels (`tailarr.managed`, `tailarr.service`, `tailarr.version`)
- [x] Log rotation checks size on every event
- [ ] Image-diff "check for updates" without always pulling (currently update always pulls)
- [ ] Integration tests (`//go:build integration`) against real Docker

## Explicit non-goals (for now)

- Web dashboard
- Homebrew/apt packaging
- Encrypting auth keys at rest
- Changing ownership of `/opt` with sudo
