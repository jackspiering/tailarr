## Summary

<!-- What does this PR change, and why? -->

## Type of change

- [ ] feat
- [ ] fix
- [ ] docs
- [ ] chore
- [ ] test
- [ ] refactor

## Checklist

- [ ] Conventional Commits used (`feat:`, `fix:`, `docs:`, `chore:`, ...)
- [ ] `go test -race -tags integration ./...` passes
- [ ] `go vet ./...`, `gofmt -l .`, and `golangci-lint run` are clean
- [ ] `rumdl check .` passes
- [ ] `go mod tidy` clean (no go.mod/go.sum diff) and `govulncheck ./...` clean
- [ ] New or changed behavior has a test
- [ ] No secrets, real `tskey-*` values, or local `/opt` dumps committed
- [ ] README and the `[Unreleased]` CHANGELOG section updated when behavior changes
- [ ] `internal/version/version.go` unchanged (a release is its own `chore(release): prepare vX.Y.Z` pull request)

## Verification

```text
# paste command output
go test -race ./...
```

## AI agent disclosure (if applicable)

- Model:
- Scope of assistance:
