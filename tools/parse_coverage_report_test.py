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

"""Unit tests for tools/parse_coverage_report.py."""

import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.resolve()))

from parse_coverage_report import (
    build_parser,
    calculate_file_metrics,
    main,
    matches_package,
    matches_target_file,
    normalize_package_path,
    parse_lcov_content,
    process_coverage_data,
)
from tool_utils import HandledError


class ParseCoverageReportTest(unittest.TestCase):
    """Test suite for LCOV coverage report parser functions and CLI main."""

    def test_normalize_package_path(self) -> None:
        """Tests package path normalization for various input formats."""
        self.assertEqual(normalize_package_path("//pkg/auth/oauth2/..."), "pkg/auth/oauth2")
        self.assertEqual(normalize_package_path("pkg/auth/oauth2/..."), "pkg/auth/oauth2")
        self.assertEqual(normalize_package_path("//pkg/auth/oauth2"), "pkg/auth/oauth2")
        self.assertEqual(normalize_package_path("./pkg/auth/oauth2/"), "pkg/auth/oauth2")
        self.assertEqual(normalize_package_path("cmd/xid_service"), "cmd/xid_service")

    def test_matches_package(self) -> None:
        """Tests matching source file paths against normalized package prefixes."""
        norm_pkg = "pkg/auth/oauth2"
        self.assertTrue(matches_package("pkg/auth/oauth2/service.go", norm_pkg))
        self.assertTrue(matches_package("./pkg/auth/oauth2/sub/util.go", norm_pkg))
        self.assertTrue(matches_package("/workspace/pkg/auth/oauth2/service.go", norm_pkg))
        self.assertFalse(matches_package("pkg/auth/other/service.go", norm_pkg))
        self.assertFalse(matches_package("pkg/auth/oauth2_extra/service.go", norm_pkg))

    def test_matches_target_file(self) -> None:
        """Tests matching source file paths against target file filters."""
        self.assertTrue(matches_target_file("pkg/auth/oauth2/service.go", "service.go"))
        self.assertTrue(
            matches_target_file("pkg/auth/oauth2/service.go", "pkg/auth/oauth2/service.go")
        )
        self.assertFalse(matches_target_file("pkg/auth/oauth2/service.go", "other.go"))

    def test_parse_lcov_100_percent_coverage(self) -> None:
        """Tests parsing LCOV data with 100% coverage."""
        lcov_data = """TN:
SF:pkg/auth/oauth2/service.go
FN:10,Login
FNDA:1,Login
DA:10,1
DA:11,5
DA:12,2
end_of_record
"""
        file_map = parse_lcov_content(lcov_data)
        self.assertIn("pkg/auth/oauth2/service.go", file_map)
        metrics = calculate_file_metrics(
            "pkg/auth/oauth2/service.go", file_map["pkg/auth/oauth2/service.go"]
        )
        self.assertEqual(metrics["total_lines"], 3)
        self.assertEqual(metrics["covered_lines"], 3)
        self.assertEqual(metrics["uncovered_lines"], 0)
        self.assertEqual(metrics["coverage_percentage"], 100.0)
        self.assertEqual(metrics["uncovered_line_numbers"], [])

    def test_parse_lcov_partial_coverage(self) -> None:
        """Tests parsing LCOV data with partial coverage and uncovered lines."""
        lcov_data = """TN:
SF:pkg/auth/oauth2/service.go
DA:10,1
DA:11,0
DA:12,1
DA:13,0
DA:14,0
DA:15,1
end_of_record
"""
        file_map = parse_lcov_content(lcov_data)
        metrics = calculate_file_metrics(
            "pkg/auth/oauth2/service.go", file_map["pkg/auth/oauth2/service.go"]
        )
        self.assertEqual(metrics["total_lines"], 6)
        self.assertEqual(metrics["covered_lines"], 3)
        self.assertEqual(metrics["uncovered_lines"], 3)
        self.assertEqual(metrics["coverage_percentage"], 50.0)
        self.assertEqual(metrics["uncovered_line_numbers"], [11, 13, 14])

    def test_parse_lcov_zero_coverage(self) -> None:
        """Tests parsing LCOV data with 0% coverage."""
        lcov_data = """TN:
SF:pkg/auth/oauth2/service.go
DA:1,0
DA:2,0
DA:3,0
end_of_record
"""
        file_map = parse_lcov_content(lcov_data)
        metrics = calculate_file_metrics(
            "pkg/auth/oauth2/service.go", file_map["pkg/auth/oauth2/service.go"]
        )
        self.assertEqual(metrics["total_lines"], 3)
        self.assertEqual(metrics["covered_lines"], 0)
        self.assertEqual(metrics["uncovered_lines"], 3)
        self.assertEqual(metrics["coverage_percentage"], 0.0)
        self.assertEqual(metrics["uncovered_line_numbers"], [1, 2, 3])

    def test_parse_lcov_empty_report(self) -> None:
        """Tests parsing empty LCOV report content."""
        file_map = parse_lcov_content("")
        result = process_coverage_data(file_map)
        self.assertEqual(result["total_lines"], 0)
        self.assertEqual(result["covered_lines"], 0)
        self.assertEqual(result["uncovered_lines"], 0)
        self.assertEqual(result["coverage_percentage"], 0.0)
        self.assertEqual(result["files"], [])

    def test_multiple_records_same_file_merging(self) -> None:
        """Tests merging line counts across multiple records for the same source file."""
        lcov_data = """SF:pkg/auth/oauth2/service.go
DA:10,1
DA:11,0
end_of_record
SF:pkg/auth/oauth2/service.go
DA:11,2
DA:12,0
end_of_record
"""
        file_map = parse_lcov_content(lcov_data)
        metrics = calculate_file_metrics(
            "pkg/auth/oauth2/service.go", file_map["pkg/auth/oauth2/service.go"]
        )
        self.assertEqual(metrics["total_lines"], 3)
        self.assertEqual(metrics["covered_lines"], 2)  # lines 10 and 11
        self.assertEqual(metrics["uncovered_lines"], 1)  # line 12
        self.assertEqual(metrics["uncovered_line_numbers"], [12])

    def test_missing_report_file(self) -> None:
        """Tests that HandledError is raised when the report file does not exist."""
        parser = build_parser()
        args = parser.parse_args(["--report-file", "non_existent_report_file.dat"])
        with self.assertRaises(HandledError) as ctx:
            main(args)
        self.assertIn("Coverage report file not found", ctx.exception.message)

    def test_min_coverage_threshold_enforcement(self) -> None:
        """Tests threshold enforcement with passing and failing coverage percentages."""
        lcov_data = """SF:pkg/auth/oauth2/service.go
DA:10,1
DA:11,1
DA:12,1
DA:13,0
end_of_record
"""
        # 3 covered out of 4 total = 75.0% coverage
        with tempfile.NamedTemporaryFile("w+", delete=False, suffix=".dat") as tmp:
            tmp.write(lcov_data)
            tmp_path = tmp.name

        try:
            parser = build_parser()

            # Passing threshold: 70.0% min coverage
            args_pass = parser.parse_args(["--report-file", tmp_path, "--min-coverage", "70.0"])
            res_pass = main(args_pass)
            self.assertEqual(res_pass["coverage_percentage"], 75.0)

            # Failing threshold: 80.0% min coverage
            args_fail = parser.parse_args(["--report-file", tmp_path, "--min-coverage", "80.0"])
            with self.assertRaises(HandledError) as ctx:
                main(args_fail)
            self.assertIn("below minimum threshold", ctx.exception.message)
        finally:
            Path(tmp_path).unlink(missing_ok=True)

    def test_package_and_file_filtering(self) -> None:
        """Tests package path and target file filtering on multi-file LCOV reports."""
        lcov_data = """SF:pkg/auth/oauth2/service.go
DA:1,1
DA:2,1
end_of_record
SF:pkg/auth/oauth2/handler.go
DA:1,0
DA:2,1
end_of_record
SF:pkg/user/profile.go
DA:1,1
DA:2,1
end_of_record
"""
        with tempfile.NamedTemporaryFile("w+", delete=False, suffix=".dat") as tmp:
            tmp.write(lcov_data)
            tmp_path = tmp.name

        try:
            parser = build_parser()

            # Filter by package
            args_pkg = parser.parse_args(
                ["--report-file", tmp_path, "--package", "//pkg/auth/oauth2/..."]
            )
            res_pkg = main(args_pkg)
            self.assertEqual(len(res_pkg["files"]), 2)
            file_names = [f["file"] for f in res_pkg["files"]]
            self.assertIn("pkg/auth/oauth2/service.go", file_names)
            self.assertIn("pkg/auth/oauth2/handler.go", file_names)
            self.assertNotIn("pkg/user/profile.go", file_names)

            # Filter by target file
            args_file = parser.parse_args(
                ["--report-file", tmp_path, "--target-file", "handler.go"]
            )
            res_file = main(args_file)
            self.assertEqual(len(res_file["files"]), 1)
            self.assertEqual(res_file["files"][0]["file"], "pkg/auth/oauth2/handler.go")
        finally:
            Path(tmp_path).unlink(missing_ok=True)


if __name__ == "__main__":
    unittest.main()
