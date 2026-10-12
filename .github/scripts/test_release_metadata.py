"""Tests for release_metadata.py. Run: python3 -m unittest discover -s .github/scripts"""

import tempfile
import unittest
from pathlib import Path

import release_metadata as rm

VERSION = "1.2.3"

CHANGELOG = """# Changelog

## [Unreleased]

## [1.2.3] - 2026-01-02

### Fixed

- A fix.

## [1.2.2] - 2026-01-01

- Old.

[Unreleased]: https://example.invalid/compare/v1.2.3...HEAD
[1.2.3]: https://example.invalid/compare/v1.2.2...v1.2.3
[1.2.2]: https://example.invalid/releases/tag/v1.2.2
"""


def write_tree(root, changelog=CHANGELOG, version=VERSION, badge=VERSION, default=None):
    root = Path(root)
    (root / "internal/version").mkdir(parents=True)
    (root / "scripts").mkdir()
    (root / "CHANGELOG.md").write_text(changelog, encoding="utf-8")
    (root / "internal/version/version.go").write_text(f'package version\n\nvar Version = "{version}"\n', encoding="utf-8")
    (root / "README.md").write_text(f"![v](https://img.shields.io/badge/version-{badge}-informational)\n", encoding="utf-8")
    (root / "scripts/install.sh").write_text(f'#!/bin/sh\nDEFAULT_VERSION="{default or "v" + VERSION}"\n', encoding="utf-8")


class ReleaseMetadataTest(unittest.TestCase):
    def check(self, **kwargs):
        with tempfile.TemporaryDirectory() as tmp:
            write_tree(tmp, **kwargs)
            return rm.check(VERSION, tmp)

    def test_agreeing_metadata_returns_the_section(self):
        notes = self.check()
        self.assertIn("A fix.", notes)
        self.assertNotIn("Old.", notes)

    def test_duplicate_heading_is_rejected(self):
        with self.assertRaisesRegex(rm.MetadataError, "exactly one CHANGELOG heading"):
            self.check(changelog=CHANGELOG + "\n## [1.2.3] - 2026-01-03\n\n- Again.\n")

    def test_heading_inside_a_code_fence_does_not_count(self):
        fenced = CHANGELOG.replace("## [1.2.3] - 2026-01-02", "```\n## [1.2.3] - 2026-01-02\n```")
        with self.assertRaisesRegex(rm.MetadataError, "found 0"):
            self.check(changelog=fenced)

    def test_missing_link_is_rejected(self):
        broken = CHANGELOG.replace("[1.2.3]: https://example.invalid/compare/v1.2.2...v1.2.3\n", "")
        with self.assertRaisesRegex(rm.MetadataError, "CHANGELOG link"):
            self.check(changelog=broken)

    def test_empty_section_is_rejected(self):
        empty = CHANGELOG.replace("### Fixed\n\n- A fix.\n\n", "")
        with self.assertRaisesRegex(rm.MetadataError, "empty"):
            self.check(changelog=empty)

    def test_version_go_mismatch_is_rejected(self):
        with self.assertRaisesRegex(rm.MetadataError, "version.go"):
            self.check(version="1.2.4")

    def test_badge_without_the_full_url_is_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            write_tree(tmp)
            Path(tmp, "README.md").write_text("badge/version-1.2.3-informational\n", encoding="utf-8")
            with self.assertRaisesRegex(rm.MetadataError, "badge"):
                rm.check(VERSION, tmp)

    def test_installer_default_must_match(self):
        with self.assertRaisesRegex(rm.MetadataError, "DEFAULT_VERSION"):
            self.check(default="v1.2.2")


if __name__ == "__main__":
    unittest.main()
