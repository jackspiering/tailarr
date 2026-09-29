# Contributing

Thanks for helping with Tailarr.

## Development setup

Requirements: Go (see `go.mod`) and Git. The integration tests also need
Docker with Compose v2; they skip without it.

```bash
git clone https://github.com/jackspiering/tailarr.git
cd tailarr
go test ./...
go build -o bin/tailarr ./cmd/tailarr
./bin/tailarr
```

Tailarr is TUI-only; run `./bin/tailarr` inside a terminal.

## Branching and commits

- Branch from latest `main`: `feat/`, `fix/`, `docs/`, or `chore/` plus a short label.
- Use [Conventional Commits](https://www.conventionalcommits.org/):
  - `feat: add catalog refresh to the TUI`
  - `fix: reject symlink deploy roots`
  - `docs: clarify binary install`
  - `chore: pin golangci-lint`
- One logical change per commit.
- Do not force-push unless a maintainer asks.
- Never commit secrets, real `tskey-*` values, or local `/opt` dumps.

## Pull requests

- Fill out the PR template.
- Keep PRs focused.
- Paste verification output (`go test -race ./...` at least).

## Before you push

CI runs each of these. Run them locally first:

```bash
go test -race ./...
go test -race -tags integration ./...
go vet ./...
gofmt -l .
rumdl check .
golangci-lint run
go mod tidy && git diff --exit-code go.mod go.sum
govulncheck ./...
```

`rumdl` is a standalone binary, not a Go tool; install it from its releases.
CI pins the versions of `golangci-lint`, `govulncheck`, and `rumdl` in
`.github/workflows/ci.yml`.

## Releases

A release is a reviewed and merged `chore(release): prepare vX.Y.Z` pull
request. It sets the same version in four places:

- `internal/version/version.go`
- the README version badge
- a `CHANGELOG.md` entry with its link (move the `[Unreleased]` notes under it)
- `DEFAULT_VERSION` in `scripts/install.sh`

Do not change `version.go` in any other pull request.

When that pull request merges, the Tag release workflow
(`.github/workflows/tag.yml`) checks that the four places agree, refuses a
version that is not newer than the latest tag, tags the head of `main`, and
starts the release workflow. Dependency, CI, and docs changes never touch
`version.go`, so they never tag. To tag a version that is already on `main`,
run the Tag release workflow by hand.

Tags are strict SemVer with a `v` prefix: `vMAJOR.MINOR.PATCH`, optionally
followed by `-PRERELEASE` and `+BUILD`. Suffix identifiers are dot-separated
ASCII alphanumerics or hyphens, and numeric prerelease identifiers have no
leading zeroes. A tag must name a commit reachable from `main`.

The release workflow (`.github/workflows/release.yml`) runs the CI gates
again, builds and checks the linux and darwin binaries for amd64 and arm64,
writes `SHA256SUMS`, and extracts the release notes from `CHANGELOG.md`. After
the protected `release` environment approves the publish job, it attests the
assets and creates a draft GitHub release. The owner or an agent reviews the
draft assets, notes, checksums, and attestations, then publishes the draft.
Never publish a draft whose workflow run failed. AI coding agents may tag,
dispatch, and publish once the release pull request is merged; see
[AGENTS.md](AGENTS.md).

The `release` environment and its required reviewers are repository settings:
an owner creates `release` in Settings > Environments and adds reviewers
there. Naming an environment in workflow YAML does not create reviewers or
protection rules.

## Documentation

Write `README.md` in ASD-STE100 Simplified Technical English and follow
Zinsser's four principles: simplicity, brevity, clarity, and humanity.
The full rule is [.grok/rules/readme-writing.md](.grok/rules/readme-writing.md).

## Project layout

See [AGENTS.md](AGENTS.md).

## License

By contributing you agree that your contributions are licensed under the MIT
License (see [LICENSE](LICENSE)).
