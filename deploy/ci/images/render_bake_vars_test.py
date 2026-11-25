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

"""Unit tests for deploy.ci.images.render_bake_vars."""

from __future__ import annotations

import copy
import io
import json
import tempfile
import unittest
from pathlib import Path
from unittest import mock

import render_bake_vars

_REPO_VERSIONS_PATH = Path(__file__).resolve().parent / "versions.json"


def _load_baseline_manifest() -> dict[str, object]:
    """Loads the real repository versions.json as a test baseline fixture."""
    return dict(json.loads(_REPO_VERSIONS_PATH.read_text(encoding="utf-8")))


class BuildBakeVariablesTest(unittest.TestCase):
    """Tests manifest-to-Bake variable conversion and validation errors."""

    def test_renders_repository_manifest(self) -> None:
        manifest = _load_baseline_manifest()
        rendered = render_bake_vars.build_bake_variables(manifest)
        variables = rendered["variable"]
        self.assertEqual(variables["BASE_IMAGE"]["default"], manifest["base_image"])
        self.assertEqual(variables["GO_VERSION"]["default"], "1.26.11")
        self.assertEqual(variables["TERRAFORM_VERSION"]["default"], "1.12.2")
        self.assertEqual(variables["PLAYWRIGHT_PYTHON_VERSION"]["default"], "1.61.0")
        self.assertEqual(variables["PLAYWRIGHT_NPM_VERSION"]["default"], "1.61.1")

    def test_rejects_missing_base_image(self) -> None:
        manifest = _load_baseline_manifest()
        manifest["base_image"] = "   "
        with self.assertRaisesRegex(ValueError, "base_image"):
            render_bake_vars.build_bake_variables(manifest)

    def test_rejects_missing_tools_object(self) -> None:
        manifest = _load_baseline_manifest()
        manifest["tools"] = []
        with self.assertRaisesRegex(ValueError, "tools"):
            render_bake_vars.build_bake_variables(manifest)

    def test_rejects_missing_tool_entry(self) -> None:
        manifest = copy.deepcopy(_load_baseline_manifest())
        assert isinstance(manifest["tools"], dict)
        del manifest["tools"]["helm"]
        with self.assertRaisesRegex(ValueError, "helm"):
            render_bake_vars.build_bake_variables(manifest)

    def test_rejects_empty_tool_field(self) -> None:
        manifest = copy.deepcopy(_load_baseline_manifest())
        assert isinstance(manifest["tools"], dict)
        manifest["tools"]["go"]["sha256"] = ""
        with self.assertRaisesRegex(ValueError, "sha256"):
            render_bake_vars.build_bake_variables(manifest)


class RenderBakeVarsCliTest(unittest.TestCase):
    """Tests CLI stdout, file output, and error exit codes."""

    def test_writes_output_file(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            out_path = Path(tmp) / "nested" / "bake-vars.json"
            exit_code = render_bake_vars.main(
                ["--versions", str(_REPO_VERSIONS_PATH), "--output", str(out_path)]
            )
            self.assertEqual(exit_code, 0)
            parsed = json.loads(out_path.read_text(encoding="utf-8"))
            self.assertIn("BASE_IMAGE", parsed["variable"])

    def test_writes_stdout_when_no_output_path(self) -> None:
        buf = io.StringIO()
        with mock.patch("sys.stdout", buf):
            exit_code = render_bake_vars.main(["--versions", str(_REPO_VERSIONS_PATH)])
        self.assertEqual(exit_code, 0)
        parsed = json.loads(buf.getvalue())
        self.assertIn("GO_VERSION", parsed["variable"])

    def test_fails_on_invalid_json_or_non_object_root(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            bad_file = Path(tmp) / "bad.json"
            bad_file.write_text("[]", encoding="utf-8")
            err = io.StringIO()
            with mock.patch("sys.stderr", err):
                exit_code = render_bake_vars.main(["--versions", str(bad_file)])
            self.assertEqual(exit_code, 1)
            self.assertIn("JSON object", err.getvalue())

    def test_fails_on_missing_file(self) -> None:
        err = io.StringIO()
        with mock.patch("sys.stderr", err):
            exit_code = render_bake_vars.main(["--versions", "/nonexistent/versions.json"])
        self.assertEqual(exit_code, 1)
        self.assertIn("error:", err.getvalue())


if __name__ == "__main__":
    unittest.main()
