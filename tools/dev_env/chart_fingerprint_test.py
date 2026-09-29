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

"""Unit tests for chart_fingerprint."""

import contextlib
import io
import os
import tempfile
import unittest
from pathlib import Path

from tools.dev_env.chart_fingerprint import compute_chart_content_hash, main

_BASELINE_FILES = {
    "Chart.yaml": "name: xmanager-cloud\n",
    "values.yaml": "imageTag: latest\n",
    "templates/ingestion-gateway/rbac.yaml": "kind: Role\n",
    "templates/xid/deployment.yaml": "kind: Deployment\n",
}


class ComputeChartContentHashTest(unittest.TestCase):
    """Tests for compute_chart_content_hash."""

    def setUp(self) -> None:
        super().setUp()
        self.tmp_dir = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp_dir.name)

    def tearDown(self) -> None:
        self.tmp_dir.cleanup()
        super().tearDown()

    def _make_chart(self, name: str, files: dict[str, str]) -> Path:
        chart_dir = self.root / name
        for rel_path, content in files.items():
            path = chart_dir / rel_path
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(content)
        chart_dir.mkdir(parents=True, exist_ok=True)
        return chart_dir

    def test_stable_across_runs_and_creation_order(self) -> None:
        forward = self._make_chart("forward", _BASELINE_FILES)
        reverse = self._make_chart("reverse", dict(reversed(_BASELINE_FILES.items())))

        digest = compute_chart_content_hash(forward)

        self.assertRegex(digest, r"^[0-9a-f]{64}$")
        self.assertEqual(digest, compute_chart_content_hash(forward))
        self.assertEqual(digest, compute_chart_content_hash(reverse))

    def test_changes_on_content_mutations(self) -> None:
        baseline = compute_chart_content_hash(self._make_chart("baseline", _BASELINE_FILES))
        removed = dict(_BASELINE_FILES)
        del removed["templates/ingestion-gateway/rbac.yaml"]
        renamed = dict(removed)
        renamed["templates/ingestion-gateway/role.yaml"] = "kind: Role\n"
        mutations = {
            "added": {**_BASELINE_FILES, "templates/new.yaml": "kind: Service\n"},
            "modified": {**_BASELINE_FILES, "values.yaml": "imageTag: v2\n"},
            "removed": removed,
            "renamed": renamed,
        }
        for label, files in mutations.items():
            with self.subTest(mutation=label):
                self.assertNotEqual(
                    baseline, compute_chart_content_hash(self._make_chart(label, files))
                )

    def test_ignores_empty_directories(self) -> None:
        chart_dir = self._make_chart("chart", _BASELINE_FILES)
        baseline = compute_chart_content_hash(chart_dir)
        (chart_dir / "templates" / "empty").mkdir()

        self.assertEqual(baseline, compute_chart_content_hash(chart_dir))

    def test_missing_directory_raises(self) -> None:
        with self.assertRaises(FileNotFoundError):
            compute_chart_content_hash(self.root / "missing")


class MainTest(unittest.TestCase):
    """Tests for the chart_fingerprint command-line entry point."""

    def test_prints_hash_of_given_chart(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            chart_dir = Path(tmp)
            (chart_dir / "Chart.yaml").write_text("name: xmanager-cloud\n")
            expected = compute_chart_content_hash(chart_dir)
            stdout = io.StringIO()

            with contextlib.redirect_stdout(stdout):
                exit_code = main(["chart_fingerprint.py", str(chart_dir)])

        self.assertEqual(exit_code, 0)
        self.assertEqual(stdout.getvalue(), expected + "\n")

    def test_defaults_to_repo_chart(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            chart_dir = Path(tmp) / "deploy" / "helm"
            chart_dir.mkdir(parents=True)
            (chart_dir / "Chart.yaml").write_text("name: xmanager-cloud\n")
            expected = compute_chart_content_hash(chart_dir)
            stdout = io.StringIO()
            cwd = os.getcwd()
            os.chdir(tmp)
            try:
                with contextlib.redirect_stdout(stdout):
                    main(["chart_fingerprint.py"])
            finally:
                os.chdir(cwd)

        self.assertEqual(stdout.getvalue(), expected + "\n")


if __name__ == "__main__":
    unittest.main()
