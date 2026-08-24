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
import unittest
from unittest import mock

import analyze_diff_size
from tool_utils import HandledError


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

    @mock.patch("subprocess.run")
    def test_get_git_numstat_success(self, mock_run):
        mock_run.return_value = mock.Mock(
            stdout="100\t20\tpkg/reconciler/handler.go\n50\t10\tpkg/reconciler/handler_test.go\n-\t-\timage.png\n",
            returncode=0,
        )
        entries = analyze_diff_size.get_git_numstat()
        self.assertEqual(len(entries), 3)
        self.assertEqual(entries[0], (100, 20, "pkg/reconciler/handler.go"))
        self.assertEqual(entries[1], (50, 10, "pkg/reconciler/handler_test.go"))
        self.assertEqual(entries[2], (0, 0, "image.png"))

    @mock.patch("subprocess.run")
    def test_get_git_numstat_handled_error(self, mock_run):
        mock_run.side_effect = subprocess.CalledProcessError(
            1, ["git", "diff"], stderr="fatal: not a git repo"
        )
        with self.assertRaises(HandledError):
            analyze_diff_size.get_git_numstat("invalid-branch")

    @mock.patch("analyze_diff_size.get_untracked_files")
    @mock.patch("analyze_diff_size.get_git_numstat")
    def test_analyze_diff_size_within_thresholds(self, mock_numstat, mock_untracked):
        mock_numstat.return_value = [
            (100, 50, "pkg/reconciler/handler.go"),  # prod: 150
            (200, 50, "pkg/reconciler/handler_test.go"),  # test: 250
            (50, 10, "README.md"),  # doc: 60
            (1000, 0, "pkg/api/v1/api.pb.go"),  # generated: 1000 (ignored)
        ]
        mock_untracked.return_value = []

        args = argparse.Namespace(
            base_ref=None,
            prod_threshold=400,
            total_code_threshold=1000,
            doc_threshold=1000,
            include_untracked=True,
        )
        res = analyze_diff_size.analyze_diff_size(args)

        self.assertFalse(res["threshold_exceeded"])
        self.assertFalse(res["should_trigger_split_verifier"])
        self.assertEqual(res["metrics"]["prod_code_lines"], 150)
        self.assertEqual(res["metrics"]["test_code_lines"], 250)
        self.assertEqual(res["metrics"]["total_code_lines"], 400)
        self.assertEqual(res["metrics"]["doc_lines"], 60)
        self.assertEqual(res["metrics"]["generated_lines"], 1000)

    @mock.patch("analyze_diff_size.get_untracked_files")
    @mock.patch("analyze_diff_size.get_git_numstat")
    def test_analyze_diff_size_exceeds_prod_threshold(self, mock_numstat, mock_untracked):
        mock_numstat.return_value = [
            (410, 10, "pkg/reconciler/handler.go"),  # prod: 420 (>= 400)
            (50, 10, "pkg/reconciler/handler_test.go"),  # test: 60
        ]
        mock_untracked.return_value = []

        args = argparse.Namespace(
            base_ref="origin/main",
            prod_threshold=400,
            total_code_threshold=1000,
            doc_threshold=1000,
            include_untracked=True,
        )
        res = analyze_diff_size.analyze_diff_size(args)

        self.assertTrue(res["threshold_exceeded"])
        self.assertTrue(res["should_trigger_split_verifier"])
        self.assertTrue(any("Production code diff" in r for r in res["exceeded_reasons"]))

    @mock.patch("analyze_diff_size.get_untracked_files")
    @mock.patch("analyze_diff_size.get_git_numstat")
    def test_analyze_diff_size_exceeds_total_code_threshold(self, mock_numstat, mock_untracked):
        mock_numstat.return_value = [
            (300, 50, "pkg/reconciler/handler.go"),  # prod: 350 (< 400)
            (600, 100, "pkg/reconciler/handler_test.go"),  # test: 700
            # total code: 1050 (>= 1000)
        ]
        mock_untracked.return_value = []

        args = argparse.Namespace(
            base_ref=None,
            prod_threshold=400,
            total_code_threshold=1000,
            doc_threshold=1000,
            include_untracked=True,
        )
        res = analyze_diff_size.analyze_diff_size(args)

        self.assertTrue(res["threshold_exceeded"])
        self.assertTrue(res["should_trigger_split_verifier"])
        self.assertTrue(any("Total code diff" in r for r in res["exceeded_reasons"]))

    @mock.patch("analyze_diff_size.get_untracked_files")
    @mock.patch("analyze_diff_size.get_git_numstat")
    def test_analyze_diff_size_exceeds_doc_threshold(self, mock_numstat, mock_untracked):
        mock_numstat.return_value = [
            (100, 10, "pkg/reconciler/handler.go"),  # prod: 110
            (950, 60, "docs/large_spec.md"),  # doc: 1010 (>= 1000)
        ]
        mock_untracked.return_value = []

        args = argparse.Namespace(
            base_ref=None,
            prod_threshold=400,
            total_code_threshold=1000,
            doc_threshold=1000,
            include_untracked=True,
        )
        res = analyze_diff_size.analyze_diff_size(args)

        self.assertTrue(res["threshold_exceeded"])
        self.assertTrue(res["should_trigger_split_verifier"])
        self.assertTrue(any("Documentation/markdown diff" in r for r in res["exceeded_reasons"]))


if __name__ == "__main__":
    unittest.main()
