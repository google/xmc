# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Unit tests for tools.lint.check_codeowners_alignment."""

from __future__ import annotations

import tempfile
import unittest
from pathlib import Path
from unittest import mock

from tools.common.tool_utils import HandledError
from tools.lint import check_codeowners_alignment as cli

_DIGEST_A = "a" * 64
_DIGEST_B = "b" * 64
_DEFAULT_LOCKED_YAML = (
    "stable-2026a:\n"
    '  locked_since: "1.0.0"\n'
    f'  chart_digest: "{_DIGEST_A}"\n'
    f'  embedded_charts_digest: "{_DIGEST_B}"\n'
)


class CheckCodeownersAlignmentTest(unittest.TestCase):
    """Tests for CODEOWNERS and OWNERS alignment verification."""

    def setUp(self) -> None:
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.root = Path(self._tmp.name)

    def _write_fixture(
        self,
        owners_content: str,
        codeowners_content: str,
        locked_yaml: str = _DEFAULT_LOCKED_YAML,
    ) -> None:
        (self.root / "OWNERS").write_text(owners_content, encoding="utf-8")
        (self.root / "CODEOWNERS").write_text(codeowners_content, encoding="utf-8")
        lock_file = self.root / "deploy/profiles/locked_profiles.yaml"
        lock_file.parent.mkdir(parents=True, exist_ok=True)
        lock_file.write_text(locked_yaml, encoding="utf-8")

    def _required_paths_for_default_fixture(self) -> list[str]:
        return [
            *cli.STATIC_PROTECTED_PATHS,
            "/deploy/profiles/stable-2026a/",
            "/test/helm/profiles/stable2026a/",
        ]

    def test_CheckedInRepositoryFiles_PassAlignment(self) -> None:
        args = cli.build_parser().parse_args([])
        result = cli.check_codeowners_alignment(args)
        self.assertGreater(result["owners_count"], 0)
        self.assertGreaterEqual(result["configured_rules_count"], result["required_paths_count"])

    def test_ValidAlignmentWithTabsAndAlignedSpaces_Passes(self) -> None:
        owners = "# Reviewers\nalice\nbob # inline comment\n"
        rules = "\n".join(
            f"{path}\t  @alice   @bob # comment"
            for path in self._required_paths_for_default_fixture()
        )
        self._write_fixture(owners, f"# Header\n{rules}\n")
        args = cli.build_parser().parse_args(["--repo-root", str(self.root)])
        result = cli.check_codeowners_alignment(args)
        self.assertEqual(result["owners_count"], 2)

    def test_MultipleLockedProfiles_RequiresRulesForEachLockedProfile(self) -> None:
        two_profiles_yaml = (
            _DEFAULT_LOCKED_YAML
            + "stable-2026b:\n"
            + '  locked_since: "1.1.0"\n'
            + f'  chart_digest: "{_DIGEST_A}"\n'
            + f'  embedded_charts_digest: "{_DIGEST_B}"\n'
        )
        owners = "alice\n"
        # Missing stable-2026b rules initially -> fails
        base_rules = "\n".join(
            f"{path} @alice" for path in self._required_paths_for_default_fixture()
        )
        self._write_fixture(owners, base_rules, locked_yaml=two_profiles_yaml)
        args = cli.build_parser().parse_args(["--repo-root", str(self.root)])
        with self.assertRaises(HandledError) as ctx:
            cli.check_codeowners_alignment(args)
        self.assertIn("/deploy/profiles/stable-2026b/", str(ctx.exception.details))

        # Adding stable-2026b rules -> passes
        full_rules = (
            base_rules
            + "\n/deploy/profiles/stable-2026b/ @alice\n"
            + "/test/helm/profiles/stable2026b/ @alice\n"
        )
        self._write_fixture(owners, full_rules, locked_yaml=two_profiles_yaml)
        result = cli.check_codeowners_alignment(args)
        self.assertEqual(result["required_paths_count"], len(cli.STATIC_PROTECTED_PATHS) + 4)

    def test_AdditionalExistingRepoRule_AllowedWhileNonexistentPath_Fails(self) -> None:
        owners = "alice\n"
        extra_dir = self.root / "deploy/profiles/stable-2026b"
        extra_dir.mkdir(parents=True, exist_ok=True)
        rules_with_existing_extra = (
            "\n".join(f"{path} @alice" for path in self._required_paths_for_default_fixture())
            + "\n/deploy/profiles/stable-2026b/ @alice\n"
        )
        self._write_fixture(owners, rules_with_existing_extra)
        args = cli.build_parser().parse_args(["--repo-root", str(self.root)])
        result = cli.check_codeowners_alignment(args)
        self.assertEqual(
            result["configured_rules_count"],
            result["required_paths_count"] + 1,
        )

        # Nonexistent extra path fails closed
        rules_with_missing_extra = (
            rules_with_existing_extra + "/deploy/profiles/does-not-exist/ @alice\n"
        )
        self._write_fixture(owners, rules_with_missing_extra)
        with self.assertRaises(HandledError) as ctx:
            cli.check_codeowners_alignment(args)
        self.assertIn("nonexistent repository path", ctx.exception.message)

    def test_MissingFiles_ListsExactMissingPath(self) -> None:
        (self.root / "OWNERS").write_text("alice\n", encoding="utf-8")
        args = cli.build_parser().parse_args(["--repo-root", str(self.root)])
        with self.assertRaises(HandledError) as ctx:
            cli.check_codeowners_alignment(args)
        self.assertIn(str(self.root / "CODEOWNERS"), ctx.exception.message)
        self.assertNotIn("True", ctx.exception.message)

    def test_EmptyOwners_RaisesHandledError(self) -> None:
        self._write_fixture("# only comments\n", "/CODEOWNERS @alice\n")
        args = cli.build_parser().parse_args(["--repo-root", str(self.root)])
        with self.assertRaises(HandledError) as ctx:
            cli.check_codeowners_alignment(args)
        self.assertIn("no active reviewer handles", ctx.exception.message)

    def test_MismatchedHandleOrMissingRule_RaisesHandledError(self) -> None:
        owners = "alice\nbob\n"
        rules = "\n".join(f"{path} @alice" for path in self._required_paths_for_default_fixture())
        self._write_fixture(owners, rules)
        args = cli.build_parser().parse_args(["--repo-root", str(self.root)])
        with self.assertRaises(HandledError) as ctx:
            cli.check_codeowners_alignment(args)
        self.assertIn("does not align", ctx.exception.message)

    def test_WildcardOrDuplicateRule_RaisesHandledError(self) -> None:
        owners = "alice\n"
        base = "\n".join(f"{path} @alice" for path in self._required_paths_for_default_fixture())
        self._write_fixture(owners, base + "\n* @alice\n")
        args = cli.build_parser().parse_args(["--repo-root", str(self.root)])
        with self.assertRaises(HandledError) as ctx:
            cli.check_codeowners_alignment(args)
        self.assertIn("wildcard rule", ctx.exception.message)

        self._write_fixture(owners, base + "\n/CODEOWNERS @alice\n")
        with self.assertRaises(HandledError) as ctx:
            cli.check_codeowners_alignment(args)
        self.assertIn("Duplicate path pattern", ctx.exception.message)

    def test_MainEntrypoint_ExitsZeroOnValidRepo(self) -> None:
        owners = "alice\n"
        rules = "\n".join(f"{path} @alice" for path in self._required_paths_for_default_fixture())
        self._write_fixture(owners, rules)
        with mock.patch(
            "sys.argv",
            ["check_codeowners_alignment.py", "--repo-root", str(self.root)],
        ):
            with self.assertRaises(SystemExit) as ctx:
                cli.main()
            self.assertEqual(ctx.exception.code, 0)


if __name__ == "__main__":
    unittest.main()
