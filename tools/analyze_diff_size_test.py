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

"""Unit tests for analyze_diff_size tool."""

import argparse
import subprocess
import sys
import unittest
from pathlib import Path
from unittest import mock

_REPO_ROOT = Path(__file__).resolve().parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools import analyze_diff_size  # noqa: E402
from tools.common.changed_files import ChangedFilesError  # noqa: E402
from tools.common.tool_utils import HandledError  # noqa: E402


class AnalyzeDiffSizeTest(unittest.TestCase):
    """Tests for file classification and diff analysis functions."""

    def test_classify_file_generated(self):
        self.assertEqual(analyze_diff_size.classify_file("pkg/api/v1/api.pb.go"), "generated")
        self.assertEqual(analyze_diff_size.classify_file("proto/service_pb2.py"), "generated")
        self.assertEqual(analyze_diff_size.classify_file("proto/service_pb2_grpc.py"), "generated")
        self.assertEqual(analyze_diff_size.classify_file("package-lock.json"), "generated")
        self.assertEqual(analyze_diff_size.classify_file("go.sum"), "generated")
        self.assertEqual(analyze_diff_size.classify_file("MODULE.bazel.lock"), "generated")

    def test_classify_file_test(self):
        self.assertEqual(analyze_diff_size.classify_file("pkg/reconciler/handler_test.go"), "test")
        self.assertEqual(analyze_diff_size.classify_file("tools/build_oci_test.py"), "test")
        self.assertEqual(analyze_diff_size.classify_file("src/app/component.spec.ts"), "test")
        self.assertEqual(analyze_diff_size.classify_file("test/e2e/cluster_test.go"), "test")
        self.assertEqual(analyze_diff_size.classify_file("pkg/testdouble/fixture.go"), "test")

    def test_classify_file_doc(self):
        self.assertEqual(analyze_diff_size.classify_file("README.md"), "doc")
        self.assertEqual(
            analyze_diff_size.classify_file(".agents/rules/core_engineering.md"), "doc"
        )
        self.assertEqual(analyze_diff_size.classify_file("docs/architecture/overview.md"), "doc")
        self.assertEqual(analyze_diff_size.classify_file("docs/guide.rst"), "doc")

    def test_classify_file_prod(self):
        self.assertEqual(analyze_diff_size.classify_file("pkg/reconciler/handler.go"), "prod")
        self.assertEqual(analyze_diff_size.classify_file("cmd/actuator/main.go"), "prod")
        self.assertEqual(analyze_diff_size.classify_file("deploy/helm/values.yaml"), "prod")
        self.assertEqual(analyze_diff_size.classify_file("tools/BUILD.bazel"), "prod")

    @mock.patch.object(analyze_diff_size, "list_untracked_files", return_value=[])
    @mock.patch.object(analyze_diff_size.subprocess, "run")
    def test_get_diff_entries_success(self, mock_run, mock_untracked):
        mock_run.return_value = mock.Mock(
            stdout="100\t20\tpkg/reconciler/handler.go\n50\t10\tpkg/reconciler/handler_test.go\n-\t-\timage.png\n",
            returncode=0,
        )

        entries = analyze_diff_size.get_diff_entries("origin/main...")
        self.assertEqual(len(entries), 2)
        self.assertEqual(entries[0], (100, "pkg/reconciler/handler.go"))
        self.assertEqual(entries[1], (50, "pkg/reconciler/handler_test.go"))
        mock_untracked.assert_called_once_with(analyze_diff_size._REPO_ROOT)

    @mock.patch.object(analyze_diff_size, "list_untracked_files")
    @mock.patch.object(analyze_diff_size.subprocess, "run")
    def test_get_diff_entries_handled_error(self, mock_run, mock_untracked):
        mock_run.side_effect = subprocess.CalledProcessError(
            1, ["git", "diff"], stderr="fatal: not a git repo"
        )
        with self.assertRaises(HandledError):
            analyze_diff_size.get_diff_entries("invalid-branch")
        mock_untracked.assert_not_called()

    @mock.patch.object(analyze_diff_size.Path, "read_bytes", autospec=True)
    @mock.patch.object(analyze_diff_size, "list_untracked_files")
    @mock.patch.object(analyze_diff_size.subprocess, "run")
    def test_get_diff_entries_combines_tracked_and_untracked_without_double_counting(
        self, mock_run, mock_untracked, mock_read_bytes
    ):
        mock_run.return_value = mock.Mock(
            stdout="10\t2\tpkg/reconciler/handler.go\n-\t-\tassets/logo.png\n", returncode=0
        )
        # Paths already in the numstat output (text or binary) must not be re-counted.
        mock_untracked.return_value = [
            "assets/logo.png",
            "pkg/new dir/new file.go",
            "pkg/reconciler/handler.go",
        ]
        mock_read_bytes.return_value = b"a\nb\nc\n"

        entries = analyze_diff_size.get_diff_entries("origin/main")

        self.assertEqual(
            entries, [(10, "pkg/reconciler/handler.go"), (3, "pkg/new dir/new file.go")]
        )
        mock_read_bytes.assert_called_once_with(
            analyze_diff_size._REPO_ROOT / "pkg/new dir/new file.go"
        )

    @mock.patch.object(analyze_diff_size, "list_untracked_files")
    @mock.patch.object(analyze_diff_size.subprocess, "run")
    def test_get_diff_entries_untracked_listing_failure_raises_handled_error(
        self, mock_run, mock_untracked
    ):
        mock_run.return_value = mock.Mock(stdout="", returncode=0)
        mock_untracked.side_effect = ChangedFilesError("`git ls-files` failed: fatal")

        with self.assertRaises(HandledError):
            analyze_diff_size.get_diff_entries("origin/main")

    @mock.patch.object(analyze_diff_size.Path, "read_bytes", autospec=True)
    @mock.patch.object(analyze_diff_size, "list_untracked_files")
    def test_get_untracked_entries_counts_lines_per_file(self, mock_untracked, mock_read_bytes):
        # Ignored files are never listed by `git ls-files --exclude-standard`, so the
        # double only returns the non-ignored ones.
        mock_untracked.return_value = ["docs/with space.md", "pkg/blob.bin", "pkg/empty.go"]
        contents = {
            "with space.md": b"one\ntwo",
            "blob.bin": b"\x89PNG\x00\x01\n\n",
            "empty.go": b"",
        }
        mock_read_bytes.side_effect = lambda path: contents[path.name]

        entries = analyze_diff_size.get_untracked_entries(set(), Path("/fake/repo"))

        self.assertEqual(
            entries, [(2, "docs/with space.md"), (0, "pkg/blob.bin"), (0, "pkg/empty.go")]
        )
        mock_untracked.assert_called_once_with(Path("/fake/repo"))

    @mock.patch.object(analyze_diff_size.Path, "read_bytes", autospec=True)
    def test_count_added_lines(self, mock_read_bytes):
        cases = {
            "trailing_newline": (b"a\nb\n", 2),
            "no_trailing_newline": (b"a\nb", 2),
            "empty": (b"", 0),
            "binary_nul_byte": (b"text\x00more\n", 0),
            "nul_after_sniff_window": (b"x" * 8000 + b"\x00\n", 1),
            "multibyte_utf8": ("ñ\n日本\n".encode(), 2),
        }
        for name, (content, want) in cases.items():
            with self.subTest(name):
                mock_read_bytes.return_value = content
                self.assertEqual(analyze_diff_size.count_added_lines(Path("f")), want)

    @mock.patch.object(analyze_diff_size.Path, "read_bytes", autospec=True)
    def test_count_added_lines_unreadable_file_counts_zero(self, mock_read_bytes):
        mock_read_bytes.side_effect = PermissionError("denied")
        self.assertEqual(analyze_diff_size.count_added_lines(Path("secret")), 0)

    @mock.patch.object(analyze_diff_size.Path, "read_bytes", autospec=True)
    @mock.patch.object(analyze_diff_size, "list_untracked_files")
    @mock.patch.object(analyze_diff_size.subprocess, "run")
    def test_analyze_diff_size_untracked_prod_file_exceeds_prod_threshold(
        self, mock_run, mock_untracked, mock_read_bytes
    ):
        mock_run.return_value = mock.Mock(stdout="", returncode=0)
        mock_untracked.return_value = ["pkg/reconciler/new_handler.go"]
        mock_read_bytes.return_value = b"line\n" * 450

        res = analyze_diff_size.analyze_diff_size(argparse.Namespace(base_ref="origin/main"))

        self.assertEqual(res["metrics"]["prod_code_lines"], 450)
        self.assertTrue(res["threshold_exceeded"])
        self.assertTrue(any("Production code" in r for r in res["exceeded_reasons"]))

    @mock.patch.object(analyze_diff_size, "get_diff_entries")
    def test_analyze_diff_size_within_thresholds(self, mock_diff_entries):
        mock_diff_entries.return_value = [
            (100, "pkg/reconciler/handler.go"),  # prod: 100
            (200, "pkg/reconciler/handler_test.go"),  # test: 200
            (50, "README.md"),  # doc: 50
            (1000, "pkg/api/v1/api.pb.go"),  # generated: 1000 (ignored)
        ]

        args = argparse.Namespace(base_ref="origin/main...")
        res = analyze_diff_size.analyze_diff_size(args)

        self.assertFalse(res["threshold_exceeded"])
        self.assertFalse(res["should_trigger_split_verifier"])
        self.assertEqual(res["metrics"]["prod_code_lines"], 100)
        self.assertEqual(res["metrics"]["test_code_lines"], 200)
        self.assertEqual(res["metrics"]["total_code_lines"], 300)
        self.assertEqual(res["metrics"]["doc_lines"], 50)
        self.assertEqual(res["metrics"]["generated_lines"], 1000)

    @mock.patch.object(analyze_diff_size, "get_diff_entries")
    def test_analyze_diff_size_exceeds_prod_threshold(self, mock_diff_entries):
        mock_diff_entries.return_value = [
            (410, "pkg/reconciler/handler.go"),  # prod: 410 (>= 400)
            (50, "pkg/reconciler/handler_test.go"),  # test: 50
        ]

        args = argparse.Namespace(base_ref="origin/main...")
        res = analyze_diff_size.analyze_diff_size(args)

        self.assertTrue(res["threshold_exceeded"])
        self.assertTrue(res["should_trigger_split_verifier"])
        self.assertTrue(any("Production code" in r for r in res["exceeded_reasons"]))

    @mock.patch.object(analyze_diff_size, "get_diff_entries")
    def test_analyze_diff_size_exceeds_total_code_threshold(self, mock_diff_entries):
        mock_diff_entries.return_value = [
            (350, "pkg/reconciler/handler.go"),  # prod: 350 (< 400)
            (700, "pkg/reconciler/handler_test.go"),  # test: 700
            # total code: 1050 (>= 1000)
        ]

        args = argparse.Namespace(base_ref="origin/main...")
        res = analyze_diff_size.analyze_diff_size(args)

        self.assertTrue(res["threshold_exceeded"])
        self.assertTrue(res["should_trigger_split_verifier"])
        self.assertTrue(any("Total code" in r for r in res["exceeded_reasons"]))

    @mock.patch.object(analyze_diff_size, "get_diff_entries")
    def test_analyze_diff_size_exceeds_doc_threshold(self, mock_diff_entries):
        mock_diff_entries.return_value = [
            (100, "pkg/reconciler/handler.go"),  # prod: 100
            (1010, "docs/large_spec.md"),  # doc: 1010 (>= 1000)
        ]

        args = argparse.Namespace(base_ref="origin/main...")
        res = analyze_diff_size.analyze_diff_size(args)

        self.assertTrue(res["threshold_exceeded"])
        self.assertTrue(res["should_trigger_split_verifier"])
        self.assertTrue(any("Documentation diff" in r for r in res["exceeded_reasons"]))


if __name__ == "__main__":
    unittest.main()
