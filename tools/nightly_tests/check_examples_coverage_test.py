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
    """Writes all 3 verification wiring entries (script, BUILD.bazel, workflow) for `name`."""
    examples_dir, tests_dir, workflows_dir = _seed_repo(root)
    (examples_dir / name).mkdir(parents=True, exist_ok=True)
    (tests_dir / f"{name}_verify.py").write_text("# verifier\n", encoding="utf-8")
    (tests_dir / "BUILD.bazel").write_text(
        f'py_binary(name = "{name}_verify", srcs = ["{name}_verify.py"])\n',
        encoding="utf-8",
    )
    (workflows_dir / "nightly-examples-e2e.yml").write_text(
        f"example_path: 'examples/{name}'\nverify_module: 'tools.nightly_tests.{name}_verify'\n",
        encoding="utf-8",
    )


class CheckExamplesCoverageTest(unittest.TestCase):
    """Tests for example E2E verification coverage gate."""

    def test_default_exempt_registry_is_valid(self) -> None:
        """EXEMPT_EXAMPLES contains only the expected non-GKE example directories."""
        self.assertEqual(
            check_examples_coverage.EXEMPT_EXAMPLES,
            ("components", "xmc_lite_job"),
        )

    def test_covered_example_passes(self) -> None:
        """An example with verifier script, BUILD.bazel target, and workflow entry passes."""
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            _wire_example(root, "simple_job")
            report = check_examples_coverage.audit_examples_coverage(root, exempt_examples=())
            self.assertEqual(report.covered, ["simple_job"])
            self.assertEqual(report.exempt, [])
            self.assertEqual(report.violations, [])

    def test_missing_all_artifacts_without_opt_out_fails(self) -> None:
        """An example without verification artifacts or opt-out produces a violation."""
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            examples_dir, _, _ = _seed_repo(root)
            (examples_dir / "untested_job").mkdir()
            report = check_examples_coverage.audit_examples_coverage(root, exempt_examples=())
            self.assertEqual(len(report.violations), 1)
            self.assertIn("examples/untested_job is missing a nightly E2E", report.violations[0])

    def test_partial_wiring_fails_even_if_in_exempt_registry(self) -> None:
        """Partial wiring (e.g. script exists without BUILD/workflow) always fails."""
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            examples_dir, tests_dir, _ = _seed_repo(root)
            (examples_dir / "partial_job").mkdir()
            (tests_dir / "partial_job_verify.py").write_text("# verifier\n", encoding="utf-8")
            report = check_examples_coverage.audit_examples_coverage(
                root, exempt_examples=("partial_job",)
            )
            self.assertEqual(len(report.violations), 1)
            self.assertIn("partial nightly verification wiring", report.violations[0])

    def test_exempt_registry_entry_passes(self) -> None:
        """An example listed in EXEMPT_EXAMPLES is marked exempt."""
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            examples_dir, _, _ = _seed_repo(root)
            (examples_dir / "local_only_job").mkdir()
            report = check_examples_coverage.audit_examples_coverage(
                root, exempt_examples=("local_only_job",)
            )
            self.assertEqual(report.exempt, ["local_only_job"])
            self.assertEqual(report.violations, [])

    def test_redundant_exempt_registry_entry_on_covered_example_fails(self) -> None:
        """A fully wired example must not retain a redundant EXEMPT_EXAMPLES entry."""
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            _wire_example(root, "covered_job")
            report = check_examples_coverage.audit_examples_coverage(
                root, exempt_examples=("covered_job",)
            )
            self.assertEqual(len(report.violations), 1)
            self.assertIn(
                "remove redundant 'covered_job' from EXEMPT_EXAMPLES", report.violations[0]
            )

    def test_stale_exempt_registry_entry_fails(self) -> None:
        """An EXEMPT_EXAMPLES entry for a non-existent directory under examples/ fails."""
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            _seed_repo(root)
            report = check_examples_coverage.audit_examples_coverage(
                root, exempt_examples=("deleted_job",)
            )
            self.assertEqual(len(report.violations), 1)
            self.assertIn(
                "stale exemption for non-existent example 'deleted_job'", report.violations[0]
            )

    def test_missing_examples_dir_fails(self) -> None:
        """Passing a repository root without an examples/ directory produces a violation."""
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            report = check_examples_coverage.audit_examples_coverage(root, exempt_examples=())
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
            report = check_examples_coverage.audit_examples_coverage(root, exempt_examples=())
            self.assertEqual(report.covered, ["single_quote_job"])
            self.assertEqual(report.violations, [])

    def test_cli_main_exit_codes(self) -> None:
        """main() returns 0 when compliant and writes error JSON to stderr when violations exist."""
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            _wire_example(root, "valid_job")
            with (
                mock.patch.object(check_examples_coverage, "EXEMPT_EXAMPLES", ()),
                mock.patch("sys.stdout", new_callable=io.StringIO),
            ):
                self.assertEqual(check_examples_coverage.main(["--repo-root", str(root)]), 0)
            (root / "examples" / "missing_job").mkdir()
            with (
                mock.patch.object(check_examples_coverage, "EXEMPT_EXAMPLES", ()),
                mock.patch("sys.stdout", new_callable=io.StringIO) as mock_out,
                mock.patch("sys.stderr", new_callable=io.StringIO) as mock_err,
            ):
                self.assertEqual(check_examples_coverage.main(["--repo-root", str(root)]), 1)
                self.assertEqual(mock_out.getvalue(), "")
                self.assertIn('"status": "error"', mock_err.getvalue())


if __name__ == "__main__":
    unittest.main()
