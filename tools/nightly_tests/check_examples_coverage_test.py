#!/usr/bin/env python3
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

"""Unit tests for tools.nightly_tests.check_examples_coverage."""

from __future__ import annotations

import io
import pathlib
import tempfile
import unittest
from unittest import mock

from tools.nightly_tests import check_examples_coverage


def _seed_repo(root: pathlib.Path) -> tuple[pathlib.Path, pathlib.Path, pathlib.Path]:
    """Creates minimal examples/, tools/nightly_tests/, and .github/workflows/ directories."""
    examples_dir = root / "examples"
    tests_dir = root / "tools" / "nightly_tests"
    workflows_dir = root / ".github" / "workflows"
    examples_dir.mkdir(parents=True, exist_ok=True)
    tests_dir.mkdir(parents=True, exist_ok=True)
    workflows_dir.mkdir(parents=True, exist_ok=True)
    return examples_dir, tests_dir, workflows_dir


def _wire_example(root: pathlib.Path, name: str) -> None:
    """Writes all 3 verification artifacts (script, BUILD.bazel, workflow) for `name`."""
    examples_dir, tests_dir, workflows_dir = _seed_repo(root)
    (examples_dir / name).mkdir(parents=True, exist_ok=True)
    (tests_dir / f"{name}_verify.py").write_text("# verifier\n", encoding="utf-8")
    build_path = tests_dir / "BUILD.bazel"
    existing_build = build_path.read_text(encoding="utf-8") if build_path.is_file() else ""
    build_path.write_text(
        existing_build + f'\npy_binary(name = "{name}_verify", srcs = ["{name}_verify.py"])\n',
        encoding="utf-8",
    )
    wf_path = workflows_dir / "nightly-examples-e2e.yml"
    existing_wf = wf_path.read_text(encoding="utf-8") if wf_path.is_file() else ""
    wf_path.write_text(
        existing_wf
        + f"\nexample_path: 'examples/{name}'\nverify_module: 'tools.nightly_tests.{name}_verify'\n",
        encoding="utf-8",
    )


class CheckExamplesCoverageTest(unittest.TestCase):
    """Tests for example E2E verification coverage gate."""

    def test_default_exempt_registry_is_valid(self) -> None:
        """All entries in EXEMPT_EXAMPLES must have a non-empty justification."""
        violations = check_examples_coverage.validate_exempt_registry(
            check_examples_coverage.EXEMPT_EXAMPLES
        )
        self.assertEqual(violations, [])
        self.assertEqual(
            set(check_examples_coverage.EXEMPT_EXAMPLES),
            {"components", "xmc_lite_job"},
        )

    def test_covered_example_passes(self) -> None:
        """An example with verifier script, BUILD.bazel target, and workflow entry passes."""
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            _wire_example(root, "simple_job")
            report = check_examples_coverage.audit_examples_coverage(root, exempt_examples={})
            self.assertEqual(report.covered, ["simple_job"])
            self.assertEqual(report.exempt, [])
            self.assertEqual(report.violations, [])

    def test_missing_all_artifacts_without_opt_out_fails(self) -> None:
        """An example without verification artifacts or opt-out produces a violation."""
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            examples_dir, _, _ = _seed_repo(root)
            (examples_dir / "untested_job").mkdir()
            report = check_examples_coverage.audit_examples_coverage(root, exempt_examples={})
            self.assertEqual(len(report.violations), 1)
            self.assertIn("examples/untested_job is missing a nightly E2E", report.violations[0])

    def test_partial_wiring_fails_even_if_in_exempt_registry(self) -> None:
        """Partial wiring (e.g. script exists without BUILD/workflow) always fails."""
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            examples_dir, tests_dir, _ = _seed_repo(root)
            (examples_dir / "partial_job").mkdir()
            (tests_dir / "partial_job_verify.py").write_text("# verifier\n", encoding="utf-8")
            exempt = {"partial_job": "Temporarily exempt while under development."}
            report = check_examples_coverage.audit_examples_coverage(root, exempt_examples=exempt)
            self.assertEqual(len(report.violations), 1)
            self.assertIn("partial nightly verification wiring", report.violations[0])

    def test_opt_out_marker_with_valid_justification_passes(self) -> None:
        """An example with a non-empty .no_nightly_e2e justification is marked exempt."""
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            examples_dir, _, _ = _seed_repo(root)
            job_dir = examples_dir / "local_only_job"
            job_dir.mkdir()
            (job_dir / ".no_nightly_e2e").write_text(
                "# Comment header\nRuns only against local docker-compose sandbox.\n",
                encoding="utf-8",
            )
            report = check_examples_coverage.audit_examples_coverage(root, exempt_examples={})
            self.assertEqual(report.exempt, ["local_only_job"])
            self.assertEqual(report.violations, [])

    def test_opt_out_marker_with_empty_or_short_reason_fails(self) -> None:
        """An empty or comment-only .no_nightly_e2e file is rejected."""
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            examples_dir, _, _ = _seed_repo(root)
            job_dir = examples_dir / "empty_opt_out"
            job_dir.mkdir()
            (job_dir / ".no_nightly_e2e").write_text("# comment only\nshort\n", encoding="utf-8")
            report = check_examples_coverage.audit_examples_coverage(root, exempt_examples={})
            self.assertEqual(len(report.violations), 1)
            self.assertIn("lacks a valid justification", report.violations[0])

    def test_redundant_opt_out_marker_on_covered_example_fails(self) -> None:
        """A fully wired example must not retain a redundant .no_nightly_e2e file."""
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            _wire_example(root, "covered_job")
            (root / "examples" / "covered_job" / ".no_nightly_e2e").write_text(
                "Obsolete opt-out reason that should be removed.\n",
                encoding="utf-8",
            )
            report = check_examples_coverage.audit_examples_coverage(root, exempt_examples={})
            self.assertEqual(len(report.violations), 1)
            self.assertIn("remove redundant", report.violations[0])

    def test_exempt_registry_supports_before_and_after_coverage(self) -> None:
        """An EXEMPT_EXAMPLES entry passes as exempt before wiring and covered after wiring."""
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            examples_dir, _, _ = _seed_repo(root)
            (examples_dir / "staged_job").mkdir()
            exempt = {"staged_job": "Pending nightly verification suite in stacked PR."}
            before = check_examples_coverage.audit_examples_coverage(root, exempt_examples=exempt)
            self.assertEqual(before.exempt, ["staged_job"])
            self.assertEqual(before.violations, [])
            _wire_example(root, "staged_job")
            after = check_examples_coverage.audit_examples_coverage(root, exempt_examples=exempt)
            self.assertEqual(after.covered, ["staged_job"])
            self.assertEqual(after.violations, [])

    def test_invalid_exempt_registry_reason_fails(self) -> None:
        """Short or empty EXEMPT_EXAMPLES reasons are flagged as violations."""
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            examples_dir, _, _ = _seed_repo(root)
            (examples_dir / "bad_entry").mkdir()
            report = check_examples_coverage.audit_examples_coverage(
                root, exempt_examples={"bad_entry": "too short"}
            )
            self.assertEqual(len(report.violations), 1)
            self.assertIn("EXEMPT_EXAMPLES['bad_entry']", report.violations[0])

    def test_stale_exempt_registry_entry_fails(self) -> None:
        """An EXEMPT_EXAMPLES entry for a non-existent directory under examples/ fails."""
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            _seed_repo(root)
            report = check_examples_coverage.audit_examples_coverage(
                root,
                exempt_examples={"deleted_job": "Valid justification for a deleted directory."},
            )
            self.assertEqual(len(report.violations), 1)
            self.assertIn(
                "stale exemption for non-existent example 'deleted_job'", report.violations[0]
            )

    def test_missing_examples_dir_fails(self) -> None:
        """Passing a repository root without an examples/ directory produces a violation."""
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            report = check_examples_coverage.audit_examples_coverage(root, exempt_examples={})
            self.assertEqual(len(report.violations), 1)
            self.assertIn("examples/ directory not found", report.violations[0])

    def test_single_quoted_build_target_passes(self) -> None:
        """Single-quoted py_binary name in BUILD.bazel is accepted."""
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            _wire_example(root, "single_quote_job")
            build_path = root / "tools" / "nightly_tests" / "BUILD.bazel"
            build_path.write_text(
                "py_binary(name = 'single_quote_job_verify', srcs = ['single_quote_job_verify.py'])\n",
                encoding="utf-8",
            )
            report = check_examples_coverage.audit_examples_coverage(root, exempt_examples={})
            self.assertEqual(report.covered, ["single_quote_job"])
            self.assertEqual(report.violations, [])

    def test_cli_main_exit_codes(self) -> None:
        """main() returns 0 when compliant and writes error JSON to stderr when violations exist."""
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            _wire_example(root, "valid_job")
            with (
                mock.patch.dict(check_examples_coverage.EXEMPT_EXAMPLES, {}, clear=True),
                mock.patch("sys.stdout", new_callable=io.StringIO),
            ):
                self.assertEqual(check_examples_coverage.main(["--repo-root", str(root)]), 0)
            (root / "examples" / "missing_job").mkdir()
            with (
                mock.patch.dict(check_examples_coverage.EXEMPT_EXAMPLES, {}, clear=True),
                mock.patch("sys.stdout", new_callable=io.StringIO) as mock_out,
                mock.patch("sys.stderr", new_callable=io.StringIO) as mock_err,
            ):
                self.assertEqual(check_examples_coverage.main(["--repo-root", str(root)]), 1)
                self.assertEqual(mock_out.getvalue(), "")
                self.assertIn('"status": "error"', mock_err.getvalue())


if __name__ == "__main__":
    unittest.main()
