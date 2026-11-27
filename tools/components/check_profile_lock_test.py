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

"""Unit tests for tools/components/check_profile_lock.py."""

from __future__ import annotations

import argparse
import os
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from tools.common.tool_utils import HandledError
from tools.components import check_profile_lock

_DIGEST_A = "a" * 64
_DIGEST_B = "b" * 64
_DIGEST_C = "c" * 64
_DIGEST_D = "d" * 64

_SAMPLE_LOCKED_YAML = f"""\
# Comment line
stable-2026a:
  locked_since: "1.0.0"
  chart_digest: "{_DIGEST_A}"
  embedded_charts_digest: "{_DIGEST_B}"
"""


class ParseLockedProfilesTest(unittest.TestCase):
    """Tests for YAML parsing of deploy/profiles/locked_profiles.yaml."""

    def test_parse_block_and_inline_mappings_and_quoted_keys(self) -> None:
        yaml_text = (
            "# Header comment\n"
            '"stable-2026a":\n'
            '  "locked_since": "1.0.0" # inline comment\n'
            f'  chart_digest: "{_DIGEST_A}"\n'
            f"  'embedded_charts_digest': '{_DIGEST_B}'\n"
            f'stable-2026b: {{locked_since: "1.1.0#rc1", chart_digest: \'{_DIGEST_C}\', embedded_charts_digest: "{_DIGEST_D}"}} # trailing comment\n'
        )
        got = check_profile_lock.parse_locked_profiles_yaml(yaml_text)
        self.assertEqual(
            got,
            {
                "stable-2026a": check_profile_lock.LockedProfile(
                    name="stable-2026a",
                    locked_since="1.0.0",
                    chart_digest=_DIGEST_A,
                    embedded_charts_digest=_DIGEST_B,
                ),
                "stable-2026b": check_profile_lock.LockedProfile(
                    name="stable-2026b",
                    locked_since="1.1.0#rc1",
                    chart_digest=_DIGEST_C,
                    embedded_charts_digest=_DIGEST_D,
                ),
            },
        )

    def test_parse_empty_or_comments_only(self) -> None:
        self.assertEqual(check_profile_lock.parse_locked_profiles_yaml(""), {})
        self.assertEqual(check_profile_lock.parse_locked_profiles_yaml("# only comment\n---\n"), {})

    def test_parse_rejects_malformed_inputs(self) -> None:
        cases = [
            ("no-colon-line", "Invalid line"),
            ("  orphan_indent: 1.0.0", "Unexpected indentation"),
            ("stable-2026a: bad_scalar", "Unexpected scalar value"),
            ("stable-2026a: {bad_entry}", "Malformed inline mapping"),
            ("stable-2026a: {}", "missing 'locked_since'"),
            (f"stable-2026a:\n  chart_digest: {_DIGEST_A}\n", "missing 'locked_since'"),
            (
                f'stable-2026a:\n  locked_since: "1.0.0"\n  chart_digest: "not-a-sha256"\n  embedded_charts_digest: "{_DIGEST_B}"\n',
                "invalid 'chart_digest'",
            ),
            (
                f'stable-2026a:\n  locked_since: "1.0.0"\n  chart_digest: "{_DIGEST_A}"\n  embedded_charts_digest: "UPPER{"A" * 59}"\n',
                "invalid 'embedded_charts_digest'",
            ),
            (
                f'stable-2026a:\n  locked_since: "1.0.0"\n  chart_digest: "{_DIGEST_A}"\n  embedded_charts_digest: "{_DIGEST_B}"\n  extra_key: "1"\n',
                "unknown key(s): extra_key",
            ),
            (
                f"stable-2026a: {{locked_since: '1.0.0', chart_digest: '{_DIGEST_A}', embedded_charts_digest: '{_DIGEST_B}'}}\n"
                f"'stable-2026a': {{locked_since: '1.0.0', chart_digest: '{_DIGEST_A}', embedded_charts_digest: '{_DIGEST_B}'}}\n",
                "duplicate profile key 'stable-2026a'",
            ),
            (
                f'stable-2026a:\n  locked_since: "1.0.0"\n  "locked_since": "1.0.1"\n  chart_digest: "{_DIGEST_A}"\n  embedded_charts_digest: "{_DIGEST_B}"\n',
                "duplicate key 'locked_since'",
            ),
        ]
        for raw, want_substr in cases:
            with self.subTest(raw=raw):
                with self.assertRaises(HandledError) as ctx:
                    check_profile_lock.parse_locked_profiles_yaml(raw)
                self.assertIn(want_substr, ctx.exception.message)

    def test_checked_in_locked_profiles_yaml_contract(self) -> None:
        repo_root = check_profile_lock._REPO_ROOT
        for candidate in (
            Path(__file__).absolute().parents[2],
            Path(__file__).resolve().parents[2],
        ):
            if (candidate / check_profile_lock.LOCKED_PROFILES_REL_PATH).is_file():
                repo_root = candidate
                break
        lock_path = repo_root / check_profile_lock.LOCKED_PROFILES_REL_PATH
        self.assertTrue(lock_path.is_file(), f"Missing {lock_path}")
        profiles = check_profile_lock.parse_locked_profiles_yaml(
            lock_path.read_text(encoding="utf-8")
        )
        self.assertIn("stable-2026a", profiles)
        for name, profile in profiles.items():
            self.assertTrue((repo_root / "deploy/profiles" / name).is_dir())
            self.assertRegex(profile.chart_digest, r"^[0-9a-f]{64}$")
            self.assertRegex(profile.embedded_charts_digest, r"^[0-9a-f]{64}$")


class EvaluateProfileLocksUnitTest(unittest.TestCase):
    """Hermetic unit tests for path classification, trailers, and lock evaluation."""

    def setUp(self) -> None:
        self.locked = check_profile_lock.parse_locked_profiles_yaml(_SAMPLE_LOCKED_YAML)

    def test_no_change(self) -> None:
        violations, overrides = check_profile_lock.evaluate_profile_locks([], self.locked, [])
        self.assertEqual(violations, [])
        self.assertEqual(overrides, {})

    def test_change_outside_locked_profile_and_unlocked_profile(self) -> None:
        changed = [
            "README.md",
            "deploy/profiles/locked_profiles.yaml",
            "deploy/profiles/BUILD.bazel",
            "deploy/profiles/stable-2026b/Chart.yaml",
            "deploy/profiles/stable-2026b/components/hpo/values.yaml",
        ]
        violations, overrides = check_profile_lock.evaluate_profile_locks(changed, self.locked, [])
        self.assertEqual(violations, [])
        self.assertEqual(overrides, {})

    def test_change_to_locked_file_without_override_fails(self) -> None:
        changed = [
            "deploy/profiles/stable-2026a/Chart.yaml",
            "deploy/profiles/stable-2026a/components/redpanda/values.yaml",
        ]
        violations, overrides = check_profile_lock.evaluate_profile_locks(
            changed,
            self.locked,
            ["feat(profiles): add hpo to stable-2026a"],
        )
        self.assertEqual(overrides, {})
        self.assertEqual(
            violations,
            [
                check_profile_lock.LockViolation(
                    profile="stable-2026a",
                    locked_since="1.0.0",
                    files=(
                        "deploy/profiles/stable-2026a/Chart.yaml",
                        "deploy/profiles/stable-2026a/components/redpanda/values.yaml",
                    ),
                )
            ],
        )

    def test_override_trailer_present_allows_locked_profile_edit(self) -> None:
        changed = ["deploy/profiles/stable-2026a/Chart.yaml"]
        messages = [
            "fix(profiles): bump stable-2026a patch version to 1.0.1\n\n"
            "PROFILE-LOCK-OVERRIDE: stable-2026a security patch for rod chart\n"
        ]
        violations, overrides = check_profile_lock.evaluate_profile_locks(
            changed,
            self.locked,
            messages,
        )
        self.assertEqual(violations, [])
        self.assertEqual(overrides, {"stable-2026a": "security patch for rod chart"})

    def test_override_trailer_without_reason_or_for_wrong_profile_rejected(self) -> None:
        changed = ["deploy/profiles/stable-2026a/values.yaml"]
        messages = [
            "PROFILE-LOCK-OVERRIDE: stable-2026a   \n"
            "PROFILE-LOCK-OVERRIDE: stable-2026b valid reason for different profile\n"
        ]
        violations, overrides = check_profile_lock.evaluate_profile_locks(
            changed,
            self.locked,
            messages,
        )
        self.assertEqual(overrides, {})
        self.assertEqual(len(violations), 1)
        self.assertEqual(violations[0].profile, "stable-2026a")

    def test_multi_profile_partial_override_reports_only_unoverridden_profile(self) -> None:
        two_locked = check_profile_lock.parse_locked_profiles_yaml(
            _SAMPLE_LOCKED_YAML
            + f'stable-2026b:\n  locked_since: "1.1.0"\n  chart_digest: "{_DIGEST_C}"\n  embedded_charts_digest: "{_DIGEST_D}"\n'
        )
        changed = [
            "deploy/profiles/stable-2026a/Chart.yaml",
            "deploy/profiles/stable-2026b/Chart.yaml",
        ]
        messages = ["PROFILE-LOCK-OVERRIDE: stable-2026a patch release 1.0.1"]
        violations, overrides = check_profile_lock.evaluate_profile_locks(
            changed,
            two_locked,
            messages,
        )
        self.assertEqual(overrides, {"stable-2026a": "patch release 1.0.1"})
        self.assertEqual(len(violations), 1)
        self.assertEqual(violations[0].profile, "stable-2026b")


class GitIntegrationProfileLockTest(unittest.TestCase):
    """End-to-end tests against a temporary Git repository for staged and branch diffs."""

    def setUp(self) -> None:
        self.temp_dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp_dir.cleanup)
        self.repo_root = Path(self.temp_dir.name)
        env_clean = {
            k: v
            for k, v in os.environ.items()
            if k not in ("PROFILE_LOCK_OVERRIDE", "GITHUB_BASE_REF")
        }
        env_clean["GIT_CONFIG_GLOBAL"] = os.devnull
        env_clean["GIT_CONFIG_NOSYSTEM"] = "1"
        self._env_patcher = patch.dict(os.environ, env_clean, clear=True)
        self._env_patcher.start()
        self.addCleanup(self._env_patcher.stop)

        self._git("init", "-b", "main")
        self._git("config", "user.email", "test@example.com")
        self._git("config", "user.name", "Test User")
        self._git("config", "commit.gpgsign", "false")

        self._write_file("deploy/profiles/locked_profiles.yaml", _SAMPLE_LOCKED_YAML)
        self._write_file("deploy/profiles/stable-2026a/Chart.yaml", "version: 1.0.0\n")
        self._write_file(
            "deploy/profiles/stable-2026a/components/redpanda/values.yaml",
            "replicas: 1\n",
        )
        self._write_file("deploy/profiles/stable-2026b/Chart.yaml", "version: 0.1.0\n")
        self._git("add", ".")
        self._git("commit", "-m", "initial commit")

    def _git(self, *args: str) -> str:
        proc = subprocess.run(
            ["git", *args],
            cwd=self.repo_root,
            capture_output=True,
            text=True,
            check=True,
        )
        return proc.stdout

    def _write_file(self, rel_path: str, content: str) -> Path:
        full_path = self.repo_root / rel_path
        full_path.parent.mkdir(parents=True, exist_ok=True)
        full_path.write_text(content, encoding="utf-8")
        return full_path

    def _parse_args(self, *cli_args: str) -> argparse.Namespace:
        return check_profile_lock.build_parser().parse_args(
            ["--repo-root", str(self.repo_root), *cli_args]
        )

    def test_invalid_repo_root_raises_handled_error(self) -> None:
        bad_args = check_profile_lock.build_parser().parse_args(
            ["--repo-root", str(self.repo_root / "does-not-exist")]
        )
        with self.assertRaises(HandledError) as ctx:
            check_profile_lock.check_profile_lock(bad_args)
        self.assertIn("Invalid git repository root", ctx.exception.message)

    def test_staged_no_changes_and_unlocked_profile_changes_pass(self) -> None:
        res = check_profile_lock.check_profile_lock(self._parse_args("--staged"))
        self.assertEqual(res["changed_files_count"], 0)

        self._write_file("deploy/profiles/stable-2026b/Chart.yaml", "version: 0.2.0\n")
        self._git("add", "deploy/profiles/stable-2026b/Chart.yaml")
        res = check_profile_lock.check_profile_lock(self._parse_args("--staged"))
        self.assertEqual(res["changed_files_count"], 1)
        self.assertEqual(res["overrides"], {})

    def test_staged_locked_file_edit_fails_with_clear_procedure(self) -> None:
        self._write_file("deploy/profiles/stable-2026a/Chart.yaml", "version: 1.0.1\n")
        self._git("add", "deploy/profiles/stable-2026a/Chart.yaml")
        with self.assertRaises(HandledError) as ctx:
            check_profile_lock.check_profile_lock(self._parse_args("--staged"))
        self.assertIn("stable-2026a", ctx.exception.message)
        self.assertIn("deploy/profiles/stable-2026a/Chart.yaml", ctx.exception.message)
        self.assertIn("PROFILE-LOCK-OVERRIDE: stable-2026a <", ctx.exception.instruction)

    def test_staged_locked_file_delete_and_rename_fail(self) -> None:
        self._git(
            "mv",
            "deploy/profiles/stable-2026a/components/redpanda/values.yaml",
            "deploy/profiles/stable-2026b/redpanda-values.yaml",
        )
        with self.assertRaises(HandledError) as ctx:
            check_profile_lock.check_profile_lock(self._parse_args("--staged"))
        self.assertIn(
            "deploy/profiles/stable-2026a/components/redpanda/values.yaml",
            ctx.exception.message,
        )

    def test_staged_env_override_passes(self) -> None:
        self._write_file("deploy/profiles/stable-2026a/Chart.yaml", "version: 1.0.1\n")
        self._git("add", "deploy/profiles/stable-2026a/Chart.yaml")
        with patch.dict(
            os.environ,
            {"PROFILE_LOCK_OVERRIDE": "stable-2026a bump chart patch version"},
        ):
            res = check_profile_lock.check_profile_lock(self._parse_args("--staged"))
        self.assertEqual(res["overrides"], {"stable-2026a": "bump chart patch version"})

    def test_staged_with_default_base_ref_blocks_removing_profile_from_lock_file(self) -> None:
        self._write_file("deploy/profiles/locked_profiles.yaml", "# emptied\n")
        self._git("add", "deploy/profiles/locked_profiles.yaml")
        with self.assertRaises(HandledError) as ctx:
            check_profile_lock.check_profile_lock(self._parse_args("--staged"))
        self.assertIn("Cannot remove GA-locked profile 'stable-2026a'", ctx.exception.message)

    def test_staged_digest_mutation_requires_override_trailer(self) -> None:
        mutated_yaml = _SAMPLE_LOCKED_YAML.replace(_DIGEST_A, _DIGEST_C)
        self._write_file("deploy/profiles/locked_profiles.yaml", mutated_yaml)
        self._git("add", "deploy/profiles/locked_profiles.yaml")
        with self.assertRaises(HandledError) as ctx:
            check_profile_lock.check_profile_lock(self._parse_args("--staged"))
        self.assertIn("deploy/profiles/locked_profiles.yaml", ctx.exception.message)

        with patch.dict(
            os.environ,
            {"PROFILE_LOCK_OVERRIDE": "PROFILE-LOCK-OVERRIDE: stable-2026a refresh digest"},
        ):
            res = check_profile_lock.check_profile_lock(self._parse_args("--staged"))
        self.assertEqual(res["overrides"], {"stable-2026a": "refresh digest"})

    def test_staged_during_merge_ignores_clean_merge_head_changes(self) -> None:
        self._git("checkout", "-b", "feat/unrelated")
        self._write_file("README.md", "feature change\n")
        self._git("add", "README.md")
        self._git("commit", "-m", "docs: update readme")

        self._git("checkout", "main")
        self._write_file("deploy/profiles/stable-2026a/README.md", "upstream doc update\n")
        self._git("add", "deploy/profiles/stable-2026a/README.md")
        self._git("commit", "-m", "docs: upstream stable-2026a readme")

        self._git("checkout", "feat/unrelated")
        subprocess.run(
            ["git", "merge", "--no-commit", "--no-ff", "main"],
            cwd=self.repo_root,
            check=True,
            capture_output=True,
        )
        res = check_profile_lock.check_profile_lock(self._parse_args("--staged"))
        self.assertEqual(res["changed_files_count"], 0)

    def test_branch_diff_locked_file_without_trailer_fails(self) -> None:
        self._git("checkout", "-b", "feat/edit-locked")
        self._git("rm", "deploy/profiles/stable-2026a/components/redpanda/values.yaml")
        self._git("commit", "-m", "feat: delete redpanda values from stable-2026a")
        with self.assertRaises(HandledError) as ctx:
            check_profile_lock.check_profile_lock(self._parse_args("--base-ref", "main"))
        self.assertIn(
            "deploy/profiles/stable-2026a/components/redpanda/values.yaml",
            ctx.exception.message,
        )

    def test_branch_diff_with_override_trailer_in_commit_passes(self) -> None:
        self._git("checkout", "-b", "fix/patch-locked")
        self._write_file("deploy/profiles/stable-2026a/Chart.yaml", "version: 1.0.1\n")
        self._git("add", "deploy/profiles/stable-2026a/Chart.yaml")
        self._git(
            "commit",
            "-m",
            "fix(profiles): patch stable-2026a\n\nPROFILE-LOCK-OVERRIDE: stable-2026a patch 1.0.1",
        )
        res = check_profile_lock.check_profile_lock(self._parse_args("--base-ref", "main"))
        self.assertEqual(res["overrides"], {"stable-2026a": "patch 1.0.1"})

    def test_branch_diff_cannot_bypass_by_removing_profile_from_lock_file(self) -> None:
        self._git("checkout", "-b", "feat/bypass-attempt")
        self._write_file("deploy/profiles/locked_profiles.yaml", "# emptied lock file\n")
        self._write_file("deploy/profiles/stable-2026a/Chart.yaml", "version: 2.0.0\n")
        self._git("add", ".")
        self._git(
            "commit",
            "-m",
            "feat: try to unlock\n\nPROFILE-LOCK-OVERRIDE: stable-2026a try unlock",
        )
        with self.assertRaises(HandledError) as ctx:
            check_profile_lock.check_profile_lock(self._parse_args("--base-ref", "main"))
        self.assertIn("Cannot remove GA-locked profile 'stable-2026a'", ctx.exception.message)

    def test_head_ref_and_base_ref_resolution(self) -> None:
        main_sha = self._git("rev-parse", "HEAD").strip()
        self._git("update-ref", "refs/remotes/origin/main", main_sha)
        self._git("update-ref", "refs/remotes/origin/release-1.0", main_sha)
        self.assertEqual(
            check_profile_lock._resolve_effective_base_ref(self.repo_root, ""),
            "origin/main",
        )
        self.assertEqual(
            check_profile_lock._resolve_effective_base_ref(
                self.repo_root, check_profile_lock._ZERO_SHA
            ),
            "origin/main",
        )
        with patch.dict(os.environ, {"GITHUB_BASE_REF": "release-1.0"}):
            self.assertEqual(
                check_profile_lock._resolve_effective_base_ref(self.repo_root, ""),
                "origin/release-1.0",
            )
        self.assertEqual(
            check_profile_lock._resolve_effective_base_ref(self.repo_root, "release-1.0"),
            "origin/release-1.0",
        )

        # Verify explicit --head-ref reads locked_profiles.yaml from that commit
        self._git("checkout", "-b", "feat/head-ref-test")
        self._write_file("deploy/profiles/stable-2026a/Chart.yaml", "version: 1.0.2\n")
        self._git("add", "deploy/profiles/stable-2026a/Chart.yaml")
        self._git(
            "commit",
            "-m",
            "fix(profiles): patch\n\nPROFILE-LOCK-OVERRIDE: stable-2026a patch 1.0.2",
        )
        branch_sha = self._git("rev-parse", "HEAD").strip()
        self._git("checkout", "main")
        res = check_profile_lock.check_profile_lock(
            self._parse_args("--base-ref", main_sha, "--head-ref", branch_sha)
        )
        self.assertEqual(res["overrides"], {"stable-2026a": "patch 1.0.2"})

    def test_nonexistent_profile_directory_in_registry_fails(self) -> None:
        bad_yaml = (
            _SAMPLE_LOCKED_YAML
            + f'missing-prof:\n  locked_since: "1.0.0"\n  chart_digest: "{_DIGEST_C}"\n  embedded_charts_digest: "{_DIGEST_D}"\n'
        )
        self._write_file("deploy/profiles/locked_profiles.yaml", bad_yaml)
        with self.assertRaises(HandledError) as ctx:
            check_profile_lock.load_locked_profiles(self.repo_root)
        self.assertIn(
            "no matching directory at deploy/profiles/missing-prof", ctx.exception.message
        )

    def test_main_entrypoint_and_missing_registry_error(self) -> None:
        with patch(
            "sys.argv",
            ["check_profile_lock.py", "--repo-root", str(self.repo_root), "--staged"],
        ):
            with self.assertRaises(SystemExit) as ctx:
                check_profile_lock.main()
            self.assertEqual(ctx.exception.code, 0)

        (self.repo_root / "deploy/profiles/locked_profiles.yaml").unlink()
        with self.assertRaises(HandledError):
            check_profile_lock.load_locked_profiles(self.repo_root)


if __name__ == "__main__":
    unittest.main()
