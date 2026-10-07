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

"""Unit tests for tools.nightly_tests.verification.verification_report module."""

from __future__ import annotations

import io
import unittest
from unittest import mock

from tools.nightly_tests.verification import verification_report


class VerificationReportTest(unittest.TestCase):
    """Test cases for verification_report.py console formatting helpers."""

    def test_print_banner_mapping(self) -> None:
        with mock.patch("sys.stdout", new_callable=io.StringIO) as mock_stdout:
            verification_report.print_banner(
                "Test Banner",
                {"XID": "123", "Workers": ["w1", "w2"]},
            )
            output = mock_stdout.getvalue()
            self.assertIn("Test Banner", output)
            self.assertIn("XID", output)
            self.assertIn("123", output)
            self.assertIn("['w1', 'w2']", output)

    def test_print_banner_sequence(self) -> None:
        with mock.patch("sys.stdout", new_callable=io.StringIO) as mock_stdout:
            verification_report.print_banner(
                "Test Banner Seq",
                [("Queue", "cpu-queue"), ("Count", 3)],
            )
            output = mock_stdout.getvalue()
            self.assertIn("Test Banner Seq", output)
            self.assertIn("cpu-queue", output)
            self.assertIn("Count", output)

    def test_print_success_summary_mapping(self) -> None:
        with mock.patch("sys.stdout", new_callable=io.StringIO) as mock_stdout:
            verification_report.print_success_summary(
                "VERIFICATION PASSED",
                {"Status": "OK", "Total": 10},
            )
            output = mock_stdout.getvalue()
            self.assertIn("VERIFICATION PASSED", output)
            self.assertIn("Status", output)
            self.assertIn("OK", output)

    def test_print_success_summary_sequence(self) -> None:
        with mock.patch("sys.stdout", new_callable=io.StringIO) as mock_stdout:
            verification_report.print_success_summary(
                "VERIFICATION PASSED SEQ",
                [("Experiment", "experiments/42"), ("Units", ("u1", "u2"))],
            )
            output = mock_stdout.getvalue()
            self.assertIn("VERIFICATION PASSED SEQ", output)
            self.assertIn("experiments/42", output)
            self.assertIn("['u1', 'u2']", output)


if __name__ == "__main__":
    unittest.main()
