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

    def test_classify_file_test_helper_packages(self):
        for path in (
            "internal/prototest/modify.go",
            "internal/experiment/experimenttest/builder.go",
            "pkg/components/bridge/helm/helmtest/chart.go",
            "securitytest/server.go",
        ):
            with self.subTest(path=path):
                self.assertEqual(analyze_diff_size.classify_file(path), "test")

    def test_classify_file_test_helper_false_positives_stay_prod(self):
        for path in (
            "deploy/profiles/latest/values.yaml",
            "pkg/attest/signer.go",
            "internal/contest/runner.go",
            "pkg/greatest/hits.go",
            "pkg/test.go",
            "pkg/foo/footest.go",
        ):
            with self.subTest(path=path):
                self.assertEqual(analyze_diff_size.classify_file(path), "prod")

    def test_classify_file_build(self):
        for path in (
            "tools/BUILD.bazel",
            "BUILD.bazel",
            "pkg/foo/BUILD",
            "oci.bzl",
            "internal/prototest/BUILD.bazel",
        ):
            with self.subTest(path=path):
                self.assertEqual(analyze_diff_size.classify_file(path), "build")

    def test_classify_file_lowercase_build_script_stays_prod(self):
        self.assertEqual(analyze_diff_size.classify_file("scripts/build"), "prod")
        self.assertEqual(analyze_diff_size.classify_file("MODULE.bazel"), "prod")

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
        self.assertEqual(analyze_diff_size.classify_file("tools/run_affected_tests.py"), "prod")

    @mock.patch.object(analyze_diff_size.subprocess, "run")
    def test_get_diff_entries_success(self, mock_run):
        mock_run.return_value = mock.Mock(
            stdout="100\t20\tpkg/reconciler/handler.go\n50\t10\tpkg/reconciler/handler_test.go\n-\t-\timage.png\n",
            returncode=0,
        )

        entries = analyze_diff_size.get_diff_entries("origin/main...")
        self.assertEqual(len(entries), 2)
        self.assertEqual(entries[0], (100, "pkg/reconciler/handler.go"))
        self.assertEqual(entries[1], (50, "pkg/reconciler/handler_test.go"))

    @mock.patch.object(analyze_diff_size.subprocess, "run")
    def test_get_diff_entries_handled_error(self, mock_run):
        mock_run.side_effect = subprocess.CalledProcessError(
            1, ["git", "diff"], stderr="fatal: not a git repo"
        )
        with self.assertRaises(HandledError):
            analyze_diff_size.get_diff_entries("invalid-branch")

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

    @mock.patch.object(analyze_diff_size, "get_diff_entries")
    def test_analyze_diff_size_excludes_build_and_helpers_from_prod(self, mock_diff_entries):
        mock_diff_entries.return_value = [
            (300, "pkg/reconciler/handler.go"),  # prod: 300
            (150, "pkg/reconciler/BUILD.bazel"),  # build: 150
            (20, "oci.bzl"),  # build: 20
            (500, "internal/experiment/experimenttest/builder.go"),  # test: 500
            (10, "docs/flows/reconciliation.md"),  # doc: 10
        ]

        res = analyze_diff_size.analyze_diff_size(argparse.Namespace(base_ref="origin/main..."))

        self.assertFalse(res["threshold_exceeded"])
        self.assertEqual(res["metrics"]["prod_code_lines"], 300)
        self.assertEqual(res["metrics"]["test_code_lines"], 500)
        self.assertEqual(res["metrics"]["total_code_lines"], 800)
        self.assertEqual(res["metrics"]["build_lines"], 170)
        self.assertEqual(res["warnings"], [])

    @mock.patch.object(analyze_diff_size, "get_diff_entries")
    def test_analyze_diff_size_warns_on_prod_without_docs(self, mock_diff_entries):
        mock_diff_entries.return_value = [
            (40, "cmd/actuator/main.go"),
            (5, "deploy/helm/values.yaml"),
            (30, "cmd/actuator/main_test.go"),
        ]

        res = analyze_diff_size.analyze_diff_size(argparse.Namespace(base_ref="origin/main..."))

        self.assertFalse(res["threshold_exceeded"])
        self.assertEqual(len(res["warnings"]), 1)
        self.assertIn("(45 lines)", res["warnings"][0])
        self.assertIn("docs/flows/*.md", res["warnings"][0])

    def test_doc_sync_warnings_boundaries(self):
        base = {"prod": 0, "test": 0, "doc": 0, "generated": 0, "build": 0, "doc_sync_prod": 0}
        cases: tuple[tuple[str, dict[str, int], int], ...] = (
            ("no_prod_changes", {}, 0),
            ("docs_updated", {"doc_sync_prod": 10, "doc": 1}, 0),
            ("prod_outside_doc_roots", {"prod": 50}, 0),
            ("single_line_without_docs", {"doc_sync_prod": 1}, 1),
        )
        for name, delta, want in cases:
            with self.subTest(name=name):
                self.assertEqual(len(analyze_diff_size.doc_sync_warnings({**base, **delta})), want)

    def test_tally_lines_counts_doc_sync_roots_only_for_prod(self):
        totals = analyze_diff_size.tally_lines(
            [
                (7, "api/v1/service.proto"),
                (3, "internal/storage/store.go"),
                (11, "tools/run_affected_tests.py"),
                (13, "internal/storage/store_test.go"),
                (17, "internal/storage/BUILD.bazel"),
            ]
        )

        self.assertEqual(totals["prod"], 21)
        self.assertEqual(totals["doc_sync_prod"], 10)
        self.assertEqual(totals["test"], 13)
        self.assertEqual(totals["build"], 17)


if __name__ == "__main__":
    unittest.main()
