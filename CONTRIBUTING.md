# Contributing

Thanks for helping with Tailarr.

## Development setup

Requirements: Go (see `go.mod`), Git, and optionally Docker Compose v2 for
manual integration checks.

```bash
git clone https://github.com/jackspiering/tailarr.git
cd tailarr
go test ./...
go build -o bin/tailarr ./cmd/tailarr
./bin/tailarr version
./bin/tailarr doctor
```

## Branching and commits

- Branch from latest `main`: `feat/`, `fix/`, `docs/`, or `chore/` plus a short label.
- Use [Conventional Commits](https://www.conventionalcommits.org/):
  - `feat: add list command`
  - `fix: reject symlink deploy roots`
  - `docs: clarify binary install`
  - `chore: pin golangci-lint`
- One logical change per commit.
- Do not force-push unless a maintainer asks.
- Never commit secrets, real `tskey-*` values, or local `/opt` dumps.

## Pull requests

- Fill out the PR template.
- Keep PRs focused; bootstrap "initial project" PRs may be larger.
- Paste verification output (`go test ./...`, and lint if you run it locally).

## Before you push

```bash
go test ./...
go vet ./...
gofmt -l .
```

Optional: `golangci-lint run` (version pinned in CI, includes staticcheck and gofmt checks).

## Project layout

See [AGENTS.md](AGENTS.md) and [docs/development.md](docs/development.md).

## License

By contributing you agree that your contributions are licensed under the MIT
License (see [LICENSE](LICENSE)).
