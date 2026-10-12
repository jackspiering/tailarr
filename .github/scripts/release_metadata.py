#!/usr/bin/env python3
"""Check that the four release metadata locations agree on one version.

The tag workflow and the release workflow both run this script, so a version
that passes tagging cannot fail the release metadata gate afterwards.

Usage: release_metadata.py VERSION [--root DIR] [--notes-out FILE]

VERSION is the bare version, for example 0.8.0. The script checks, under DIR
(default: the current directory):

- CHANGELOG.md has exactly one heading and one link definition for VERSION
  outside code fences, and a non-empty section.
- internal/version/version.go declares Version exactly once as VERSION.
- README.md has exactly one version badge for VERSION.
- scripts/install.sh sets DEFAULT_VERSION exactly once to vVERSION and parses.

With --notes-out, the CHANGELOG section is written to FILE.
"""

import argparse
import re
import subprocess
import sys
from pathlib import Path

FENCE_RE = re.compile(r"^ {0,3}(`{3,}|~{3,})")


class MetadataError(Exception):
    """The release metadata does not agree."""


def outside_fence(lines):
    """Yield (line, outside) for each line, where outside is False inside a code fence."""
    in_fence = False
    fence_char = ""
    fence_length = 0
    for line in lines:
        match = FENCE_RE.match(line)
        if match:
            marker = match.group(1)
            if not in_fence:
                in_fence = True
                fence_char = marker[0]
                fence_length = len(marker)
            elif marker[0] == fence_char and len(marker) >= fence_length:
                in_fence = False
        yield line, not in_fence


def changelog_notes(changelog_lines, version):
    """Return the CHANGELOG section for version, after checking heading and link."""
    escaped = re.escape(version)
    heading_re = re.compile(rf"^## \[{escaped}\](?: - [0-9]{{4}}-[0-9]{{2}}-[0-9]{{2}})?$")
    link_re = re.compile(rf"^\[{escaped}\]:\s+(\S+)\s*$")
    visible = list(outside_fence(changelog_lines))
    headings = [i for i, (line, outside) in enumerate(visible) if outside and heading_re.fullmatch(line)]
    links = [line for line, outside in visible if outside and link_re.fullmatch(line)]
    if len(headings) != 1:
        raise MetadataError(f"expected exactly one CHANGELOG heading for [{version}], found {len(headings)}")
    if len(links) != 1:
        raise MetadataError(f"expected exactly one CHANGELOG link for [{version}], found {len(links)}")

    notes = []
    for line, outside in visible[headings[0] + 1:]:
        if outside and re.match(r"^## ", line):
            break
        notes.append(line)
    text = "\n".join(notes).strip()
    if not text:
        raise MetadataError(f"release notes for [{version}] are empty or whitespace-only")
    return text


def check(version, root="."):
    """Verify all four locations under root and return the release notes text."""
    root = Path(root)
    changelog = (root / "CHANGELOG.md").read_text(encoding="utf-8").splitlines()
    readme = (root / "README.md").read_text(encoding="utf-8")
    version_go = (root / "internal/version/version.go").read_text(encoding="utf-8")
    installer = (root / "scripts/install.sh").read_text(encoding="utf-8")

    notes = changelog_notes(changelog, version)

    declarations = re.findall(r'^var Version = "([^"]+)"\s*$', version_go, re.MULTILINE)
    if declarations != [version]:
        raise MetadataError(f"internal/version/version.go must declare Version exactly once as {version!r}")

    badges = re.findall(r"https://img\.shields\.io/badge/version-([^)]*)-informational", readme)
    if badges != [version]:
        raise MetadataError(f"README must contain exactly one version badge for {version}")

    defaults = re.findall(r'^DEFAULT_VERSION="([^"]+)"\s*$', installer, re.MULTILINE)
    if defaults != [f"v{version}"]:
        raise MetadataError(f"scripts/install.sh DEFAULT_VERSION must be exactly v{version}")
    subprocess.run(["sh", "-n", str(root / "scripts/install.sh")], check=True)
    return notes


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("version")
    parser.add_argument("--root", default=".")
    parser.add_argument("--notes-out")
    args = parser.parse_args(argv)
    try:
        notes = check(args.version, args.root)
    except MetadataError as err:
        print(f"error: {err}", file=sys.stderr)
        return 1
    if args.notes_out:
        Path(args.notes_out).write_text(notes + "\n", encoding="utf-8")
    print(f"Release metadata agrees on {args.version}.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
