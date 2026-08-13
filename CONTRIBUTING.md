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
- Keep PRs focused; bootstrap "initial project" PRs may be larger.
- Paste verification output (`go test -race ./...`, and lint if you run it locally).

## Before you push

```bash
go test -race ./...
go test -race -tags integration ./...
go vet ./...
gofmt -l .
rumdl check .
go mod tidy && git diff --exit-code go.mod go.sum
```

`rumdl` is a standalone binary, not a Go tool; install it from its releases
and run `rumdl check .` as shown.

Optional: `golangci-lint run` (version pinned in CI, includes staticcheck and gofmt checks).

## Releases

Releases are cut from `main` only after the release change has been reviewed and
merged. Release metadata must agree before the tag is created:

- `internal/version/version.go` contains the release version.
- The README version badge and `CHANGELOG.md` entry use that version.
- `scripts/install.sh` has the matching installer fallback version.

Use a strict SemVer tag in one of these forms: `vMAJOR.MINOR.PATCH`,
`vMAJOR.MINOR.PATCH-PRERELEASE`, `vMAJOR.MINOR.PATCH+BUILD`, or
`vMAJOR.MINOR.PATCH-PRERELEASE+BUILD`. Suffix identifiers are dot-separated
ASCII alphanumerics or hyphens; numeric prerelease identifiers must not have
leading zeroes. Do not use other tag names or a `v`-less version.

The tag must name a commit already reachable from the default branch (`main`),
not an unmerged branch commit. Creating or pushing a release tag is a separate
operation from creating or publishing a GitHub release. An AI coding agent must
not create or push a tag, dispatch a release, or publish a release without
explicit approval from the repository owner for that action.

After an approved tag is pushed, the release workflow runs the full repository
CI gates before publication. Its verification job builds and checks the
platform binaries, checksums, release metadata, installer fallback, and
extracted release notes, then uploads one bundle containing the validated
artifacts and notes. The publication job downloads that bundle, creates the
provenance attestations for the release assets, and, after the protected
`release` environment gate, creates a draft GitHub release. A human must review
the draft assets, notes, checksums, and attestations, then manually publish the
draft.

The `release` environment and its required reviewers are repository
configuration: an owner must create or select `release` in Settings >
Environments and configure required reviewers (and any other protection rules)
there. Referencing an environment in workflow YAML does not create reviewers
or protection rules. Keep the release draft until that human review is
complete; do not treat a successful tag workflow as permission to publish.

## Documentation

Write `README.md` in ASD-STE100 Simplified Technical English and follow
Zinsser's four principles: simplicity, brevity, clarity, and humanity.
The full rule is [.grok/rules/readme-writing.md](.grok/rules/readme-writing.md).

## Project layout

See [AGENTS.md](AGENTS.md).

## License

By contributing you agree that your contributions are licensed under the MIT
License (see [LICENSE](LICENSE)).
