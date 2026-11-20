# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Unit tests for tools.common.workspace_status."""

from __future__ import annotations

import io
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from tools.common import workspace_status


class WorkspaceStatusTest(unittest.TestCase):
    def test_read_version_reads_repo_version_file(self) -> None:
        version = workspace_status.read_version()
        self.assertRegex(version, r"^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$")
        self.assertLessEqual(len(f"xmc-{version}"), 63)
        self.assertNotEqual(version, workspace_status.FALLBACK_VERSION)

    def test_read_version_strips_surrounding_newlines(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            vf = Path(tmpdir) / "VERSION"
            vf.write_text("  2.3.4\n", encoding="utf-8")
            self.assertEqual(workspace_status.read_version(vf), "2.3.4")

    def test_read_version_rejects_empty_or_multiline(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            for content in ("", "   \n", "1.0.0\n2.0.0\n", "1.0.0 beta"):
                vf = Path(tmpdir) / "VERSION"
                vf.write_text(content, encoding="utf-8")
                with self.assertRaises(ValueError):
                    workspace_status.read_version(vf)

    def test_main_prints_stable_app_version(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            vf = Path(tmpdir) / "VERSION"
            vf.write_text("1.2.3\n", encoding="utf-8")
            with mock.patch("sys.stdout", new_callable=io.StringIO) as out:
                workspace_status.main(vf)
            self.assertEqual(out.getvalue(), "STABLE_APP_VERSION 1.2.3\n")

    def test_main_falls_back_when_version_file_missing(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            missing = Path(tmpdir) / "VERSION"
            with (
                mock.patch("sys.stdout", new_callable=io.StringIO) as out,
                mock.patch("sys.stderr", new_callable=io.StringIO) as err,
            ):
                workspace_status.main(missing)
            self.assertEqual(
                out.getvalue(), f"STABLE_APP_VERSION {workspace_status.FALLBACK_VERSION}\n"
            )
            self.assertIn("Error reading version file", err.getvalue())


if __name__ == "__main__":
    unittest.main()
