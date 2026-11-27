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

"""Unit tests for deploy.ci.images.verify_image."""

from __future__ import annotations

import io
import json
import subprocess
import tempfile
import unittest
from pathlib import Path
from typing import Any
from unittest import mock

import verify_image

_REPO_VERSIONS_PATH = Path(__file__).resolve().parent / "versions.json"


def _load_manifest() -> dict[str, Any]:
    """Loads the repository versions.json baseline fixture."""
    return dict(json.loads(_REPO_VERSIONS_PATH.read_text(encoding="utf-8")))


def _fake_runner_factory(
    overrides: dict[str, subprocess.CompletedProcess[str]] | None = None,
) -> Any:
    """Creates a hermetic subprocess runner returning version-matching outputs."""
    manifest = _load_manifest()
    tools = manifest["tools"]
    defaults: dict[str, str] = {
        "python3": f"Python {tools['python']['version']}",
        "node": f"v{tools['node']['version']}",
        "git": "git version 2.43.0\n*",
        "jq": "jq-1.7",
        "curl": "curl 8.5.0",
    }

    def _run(argv: list[str], **_kwargs: Any) -> subprocess.CompletedProcess[str]:
        cmd = argv[0]
        if overrides and cmd in overrides:
            return overrides[cmd]
        return subprocess.CompletedProcess(argv, 0, defaults.get(cmd, ""), "")

    return _run


class RunSmokeChecksTest(unittest.TestCase):
    """Tests smoke check logic for base role and failure modes."""

    def test_base_role_passes(self) -> None:
        failures = verify_image.run_smoke_checks(
            "base", _load_manifest(), runner=_fake_runner_factory()
        )
        self.assertEqual(failures, [])

    def test_command_failures_nonzero_version_mismatch_and_oserror(self) -> None:
        bad_runner = _fake_runner_factory(
            {
                "python3": subprocess.CompletedProcess(["python3"], 1, "", "boom"),
                "node": subprocess.CompletedProcess(["node"], 0, "v18.0.0", ""),
            }
        )
        failures = verify_image.run_smoke_checks("base", _load_manifest(), runner=bad_runner)
        self.assertTrue(any("exited 1" in f for f in failures))
        self.assertTrue(any("expected 'v22." in f for f in failures))

        def _raising_runner(argv: list[str], **_kwargs: Any) -> subprocess.CompletedProcess[str]:
            raise FileNotFoundError(argv[0])

        err = verify_image._run_command_expectation(
            verify_image.CommandExpectation("missing", ("nope",)), _raising_runner
        )
        self.assertIsNotNone(err)
        assert err is not None
        self.assertIn("failed to execute nope", err)

        empty_err = verify_image._run_command_expectation(
            verify_image.CommandExpectation("empty", ()), _raising_runner
        )
        self.assertIsNotNone(empty_err)
        assert empty_err is not None
        self.assertIn("argv is empty", empty_err)

        none_output_runner = _fake_runner_factory(
            {"node": subprocess.CompletedProcess(["node"], 0, None, None)}
        )
        none_err = verify_image._run_command_expectation(
            verify_image.CommandExpectation("node", ("node", "--version"), ("None",)),
            none_output_runner,
        )
        self.assertIsNotNone(none_err)
        assert none_err is not None
        self.assertIn("expected 'None'", none_err)

    def test_rejects_unsupported_role_and_invalid_manifest(self) -> None:
        with self.assertRaises(ValueError):
            verify_image.run_smoke_checks("invalid", _load_manifest())
        with self.assertRaises(ValueError):
            verify_image._tool_version({}, "python")
        with self.assertRaises(ValueError):
            verify_image._tool_version({"tools": {}}, "python")


class VerifyImageCliTest(unittest.TestCase):
    """Tests CLI exit codes and error reporting."""

    def test_cli_returns_zero_on_pass(self) -> None:
        out = io.StringIO()
        with (
            mock.patch.object(verify_image, "run_smoke_checks", return_value=[]),
            mock.patch("sys.stdout", out),
        ):
            rc = verify_image.main(["--role", "base", "--versions", str(_REPO_VERSIONS_PATH)])
        self.assertEqual(rc, 0)
        self.assertIn("PASS:", out.getvalue())

    def test_cli_returns_one_on_failures(self) -> None:
        err = io.StringIO()
        with (
            mock.patch.object(verify_image, "run_smoke_checks", return_value=["node: bad version"]),
            mock.patch("sys.stderr", err),
        ):
            rc = verify_image.main(["--role", "base", "--versions", str(_REPO_VERSIONS_PATH)])
        self.assertEqual(rc, 1)
        self.assertIn("FAIL: node: bad version", err.getvalue())

    def test_cli_returns_one_on_non_object_manifest(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            bad = Path(tmp) / "bad.json"
            bad.write_text("123", encoding="utf-8")
            err = io.StringIO()
            with mock.patch("sys.stderr", err):
                rc = verify_image.main(["--role", "base", "--versions", str(bad)])
            self.assertEqual(rc, 1)
            self.assertIn("SMOKE TEST ERROR", err.getvalue())


if __name__ == "__main__":
    unittest.main()
