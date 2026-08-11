# Parity checklist (Bash Tailarr -> Go)

Track feature parity with the original Bash tool spirit. Items marked done
are implemented with tests and/or working CLI paths in this tree.

## Core

- [x] Config + env overrides (plain KEY=VALUE, atomic write)
- [x] Path/service-name validation + symlink/boundary helpers
- [x] Secret redaction in logs (basic patterns)
- [x] Auth key store (mode 600, validate `tskey-auth-*`, redacted UI/CLI)
- [x] Doctor (commands, paths, docker/compose reachability; no privilege escalation)
- [x] Catalog discovery (`list`) against local ScaleTail-like tree
- [x] `deployed` listing
- [x] TUI shell (main menu, navigation, quit; plain help if not a TTY)
- [x] `version` / `tailarr version`
- [x] Per-service locks (file locks) + repo lock for git refresh
- [x] Backups under `.tailarr_backups` (move/copy) + restore data dirs helper
- [x] Deploy/repair/update/stop/restart/remove skeleton wired to Docker Compose
- [x] ScaleTail clone/pull + optional ref pin + protocol hardening (`protocol.ext/file.allow=never`)
- [ ] Deploy with interactive env merge/prompt for missing values
- [ ] Repair keeps local secrets (partial: preserves `.env` file bytes)
- [ ] Status: managed vs manual ScaleTail vs other (partial: managed marker + deployed tag)
- [ ] `--yes` only auto-confirms default-yes style prompts (flag exists; prompts incomplete)
- [ ] Full Tailarr compose labels on services for runtime status
- [ ] Log rotation polish and multi-select TUI flows
- [ ] Integration tests (`//go:build integration`) against real Docker

## Explicit non-goals (for now)

- Web dashboard
- Homebrew/apt packaging
- Encrypting auth keys at rest
- Changing ownership of `/opt` with sudo
- Bash runtime dependency
