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
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

_REPO_ROOT = Path(__file__).resolve().parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools import analyze_diff_size  # noqa: E402
from tools.common.changed_files import ChangedFilesError  # noqa: E402
from tools.common.tool_utils import HandledError  # noqa: E402

# Repository root that the _find_repo_root double reports.
_FAKE_ROOT = Path("/work/xmc")


def _write_file(root: Path, relative_path: str, content: bytes) -> Path:
    """Writes content to root/relative_path, creating parents, and returns the path."""
    path = root / relative_path
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(content)
    return path


def _patch_repo_root(test: unittest.TestCase) -> None:
    """Makes _find_repo_root return _FAKE_ROOT for the rest of the test."""
    patcher = mock.patch.object(analyze_diff_size, "_find_repo_root", return_value=_FAKE_ROOT)
    patcher.start()
    test.addCleanup(patcher.stop)


class AnalyzeDiffSizeTest(unittest.TestCase):
    """Tests for file classification and diff analysis functions."""

    def setUp(self):
        super().setUp()
        _patch_repo_root(self)

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
        mock_run.assert_called_once_with(
            ["git", "-c", "core.quotePath=false", "diff", "--numstat", "origin/main..."],
            capture_output=True,
            text=True,
            check=True,
            cwd=_FAKE_ROOT,
        )
        mock_untracked.assert_called_once_with(_FAKE_ROOT)

    @mock.patch.object(analyze_diff_size, "list_untracked_files")
    @mock.patch.object(analyze_diff_size.subprocess, "run")
    def test_get_diff_entries_handled_error(self, mock_run, mock_untracked):
        mock_run.side_effect = subprocess.CalledProcessError(
            1, ["git", "diff"], stderr="fatal: not a git repo"
        )
        with self.assertRaises(HandledError):
            analyze_diff_size.get_diff_entries("invalid-branch")
        mock_untracked.assert_not_called()

    @mock.patch.object(analyze_diff_size, "count_added_lines", autospec=True, return_value=3)
    @mock.patch.object(analyze_diff_size, "list_untracked_files")
    @mock.patch.object(analyze_diff_size.subprocess, "run")
    def test_get_diff_entries_combines_tracked_and_untracked_without_double_counting(
        self, mock_run, mock_untracked, mock_count
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

        entries = analyze_diff_size.get_diff_entries("origin/main")

        self.assertEqual(
            entries, [(10, "pkg/reconciler/handler.go"), (3, "pkg/new dir/new file.go")]
        )
        mock_count.assert_called_once_with(_FAKE_ROOT / "pkg/new dir/new file.go")

    @mock.patch.object(analyze_diff_size, "count_added_lines", autospec=True, return_value=3)
    @mock.patch.object(analyze_diff_size, "list_untracked_files")
    @mock.patch.object(analyze_diff_size.subprocess, "run")
    def test_get_diff_entries_dedupes_non_ascii_paths(self, mock_run, mock_untracked, _):
        # With core.quotePath=false numstat prints multibyte paths verbatim, as ls-files -z does.
        mock_run.return_value = mock.Mock(stdout="5\t1\tdocs/ñew.md\n", returncode=0)
        mock_untracked.return_value = ["docs/ñew.md", "pkg/日本.go"]

        entries = analyze_diff_size.get_diff_entries("origin/main")

        self.assertEqual(entries, [(5, "docs/ñew.md"), (3, "pkg/日本.go")])

    @mock.patch.object(analyze_diff_size, "list_untracked_files")
    @mock.patch.object(analyze_diff_size.subprocess, "run")
    def test_get_diff_entries_untracked_listing_failure_raises_handled_error(
        self, mock_run, mock_untracked
    ):
        mock_run.return_value = mock.Mock(stdout="", returncode=0)
        mock_untracked.side_effect = ChangedFilesError("`git ls-files` failed: fatal")

        with self.assertRaises(HandledError):
            analyze_diff_size.get_diff_entries("origin/main")

    @mock.patch.object(analyze_diff_size, "list_untracked_files")
    def test_get_untracked_entries_counts_lines_per_file(self, mock_untracked):
        # Ignored files are never listed by `git ls-files --exclude-standard`, so the
        # double only returns the non-ignored ones.
        paths = ["docs/with space.md", "pkg/blob.bin", "pkg/empty.go"]
        mock_untracked.return_value = paths
        contents = [b"one\ntwo", b"\x89PNG\x00\x01\n\n", b""]
        with tempfile.TemporaryDirectory() as tmp:
            repo_root = Path(tmp)
            for path, content in zip(paths, contents, strict=True):
                _write_file(repo_root, path, content)

            entries = analyze_diff_size.get_untracked_entries(set(), repo_root)

            mock_untracked.assert_called_once_with(repo_root)
        self.assertEqual(
            entries, [(2, "docs/with space.md"), (0, "pkg/blob.bin"), (0, "pkg/empty.go")]
        )


class FindRepoRootTest(unittest.TestCase):
    """Tests _find_repo_root against canned git output."""

    @mock.patch.object(analyze_diff_size.subprocess, "run")
    def test_find_repo_root_returns_toplevel(self, mock_run):
        mock_run.return_value = mock.Mock(stdout="/work/xmc\n", returncode=0)

        self.assertEqual(analyze_diff_size._find_repo_root(), Path("/work/xmc"))
        mock_run.assert_called_once_with(
            ["git", "rev-parse", "--show-toplevel"], capture_output=True, text=True, check=True
        )

    @mock.patch.object(analyze_diff_size.subprocess, "run")
    def test_find_repo_root_outside_git_raises_handled_error(self, mock_run):
        mock_run.side_effect = subprocess.CalledProcessError(
            128, ["git", "rev-parse"], stderr="fatal: not a git repository\n"
        )

        with self.assertRaises(HandledError) as ctx:
            analyze_diff_size._find_repo_root()

        self.assertEqual(
            ctx.exception.message,
            "Failed to find the git repository root: fatal: not a git repository",
        )


class CountAddedLinesTest(unittest.TestCase):
    """Tests count_added_lines against real files in a temporary directory.

    Real files exercise the actual open/read path instead of a mocked file object.
    """

    def setUp(self):
        super().setUp()
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.root = Path(tmp.name)

    def test_count_added_lines(self):
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
                path = _write_file(self.root, name, content)
                self.assertEqual(analyze_diff_size.count_added_lines(path), want)

    @mock.patch.object(analyze_diff_size, "_LINE_COUNT_CHUNK_BYTES", 3)
    def test_count_added_lines_spanning_multiple_chunks(self):
        head = b"a" * 7999 + b"\n"  # Exactly fills the binary sniff window.
        cases = {
            "trailing_newline": (head + b"ab\ncd\nef\n", 4),
            "no_trailing_newline": (head + b"ab\ncd\nefg", 4),
            "newline_ends_chunk_boundary": (head + b"ab\n", 2),
            "nul_after_sniff_window": (head + b"\x00b\nc", 3),
        }
        for name, (content, want) in cases.items():
            with self.subTest(name):
                path = _write_file(self.root, name, content)
                self.assertEqual(analyze_diff_size.count_added_lines(path), want)

    def test_count_added_lines_missing_file_counts_zero(self):
        self.assertEqual(analyze_diff_size.count_added_lines(self.root / "missing"), 0)

    def test_count_added_lines_unreadable_file_counts_zero(self):
        path = _write_file(self.root, "secret", b"a\n")
        with mock.patch.object(
            analyze_diff_size.Path, "open", side_effect=PermissionError("denied")
        ) as mock_open:
            self.assertEqual(analyze_diff_size.count_added_lines(path), 0)
        mock_open.assert_called_once()

    def test_count_added_lines_symlink_counts_one(self):
        target = _write_file(self.root, "target", b"a\nb\nc\n")
        link = self.root / "link"
        link.symlink_to(target)
        self.assertEqual(analyze_diff_size.count_added_lines(link), 1)

    @unittest.skipUnless(hasattr(os, "mkfifo"), "requires os.mkfifo")
    def test_count_added_lines_fifo_counts_zero_without_blocking(self):
        fifo = self.root / "fifo"
        os.mkfifo(fifo)
        self.assertEqual(analyze_diff_size.count_added_lines(fifo), 0)


class AnalyzeDiffSizeThresholdTest(unittest.TestCase):
    """Tests threshold evaluation in analyze_diff_size."""

    def setUp(self):
        super().setUp()
        _patch_repo_root(self)

    @mock.patch.object(analyze_diff_size, "count_added_lines", autospec=True, return_value=450)
    @mock.patch.object(analyze_diff_size, "list_untracked_files")
    @mock.patch.object(analyze_diff_size.subprocess, "run")
    def test_analyze_diff_size_untracked_prod_file_exceeds_prod_threshold(
        self, mock_run, mock_untracked, _
    ):
        mock_run.return_value = mock.Mock(stdout="", returncode=0)
        mock_untracked.return_value = ["pkg/reconciler/new_handler.go"]

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
