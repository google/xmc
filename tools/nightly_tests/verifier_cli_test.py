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

"""Unit tests for tools.nightly_tests.verifier_cli module."""

from __future__ import annotations

import unittest
from types import SimpleNamespace
from unittest import mock

from tools.nightly_tests import verifier_cli


class VerifierCliTest(unittest.TestCase):
    """Test cases for verifier_cli.py utilities."""

    def test_get_flag(self) -> None:
        flags_mock = SimpleNamespace(xid="12345", endpoint=None)
        self.assertEqual(verifier_cli.get_flag(flags_mock, "xid"), "12345")
        self.assertEqual(
            verifier_cli.get_flag(flags_mock, "endpoint", "default:443"), "default:443"
        )
        self.assertEqual(verifier_cli.get_flag(flags_mock, "nonexistent", "fallback"), "fallback")
        self.assertEqual(verifier_cli.get_flag(None, "xid", "fallback"), "fallback")

    def test_validate_argv(self) -> None:
        verifier_cli.validate_argv(["script.py"])
        with self.assertRaises(verifier_cli.app.UsageError):
            verifier_cli.validate_argv(["script.py", "extra_arg"])

    def test_parse_xid(self) -> None:
        self.assertEqual(verifier_cli.parse_xid("12345"), "12345")
        self.assertEqual(verifier_cli.parse_xid("experiments/12345"), "12345")
        self.assertEqual(verifier_cli.parse_xid("dashboards/my_dashboard"), "my_dashboard")
        self.assertEqual(verifier_cli.parse_xid("experiments/42"), "42")
        self.assertEqual(verifier_cli.parse_xid("/experiments/42"), "42")
        self.assertEqual(verifier_cli.parse_xid("experiments/test-exp-99"), "test-exp-99")
        self.assertEqual(verifier_cli.parse_xid("/experiments/test-exp-99"), "test-exp-99")
        self.assertEqual(verifier_cli.parse_xid("dashboards/my_slug"), "my_slug")
        self.assertEqual(verifier_cli.parse_xid("/dashboards/my_slug"), "my_slug")
        self.assertEqual(
            verifier_cli.parse_xid("dashboards/test-dashboard-123"), "test-dashboard-123"
        )
        self.assertEqual(
            verifier_cli.parse_xid("/dashboards/test-dashboard-123"), "test-dashboard-123"
        )

        with self.assertRaises(ValueError):
            verifier_cli.parse_xid(None, required=True)
        with self.assertRaises(ValueError):
            verifier_cli.parse_xid("", required=True)
        with self.assertRaises(ValueError):
            verifier_cli.parse_xid("   ", required=True)
        self.assertEqual(verifier_cli.parse_xid(None, required=False), "")
        self.assertEqual(verifier_cli.parse_xid("   ", required=False), "")

    def test_resolve_expected_work_units(self) -> None:
        # Flag takes highest precedence
        with mock.patch.object(verifier_cli, "get_flag", return_value=5):
            self.assertEqual(verifier_cli.resolve_expected_work_units(3), 5)

        # Non-positive or non-integer flag values fallback to env var or default
        with mock.patch.object(verifier_cli, "get_flag", return_value=0):
            self.assertEqual(verifier_cli.resolve_expected_work_units(3), 3)

        with mock.patch.object(verifier_cli, "get_flag", return_value=-5):
            self.assertEqual(verifier_cli.resolve_expected_work_units(3), 3)

        with (
            mock.patch.object(verifier_cli, "get_flag", return_value="invalid"),
            mock.patch.dict("os.environ", {"NUM_WORK_UNITS": "4"}, clear=True),
        ):
            self.assertEqual(verifier_cli.resolve_expected_work_units(3), 4)

        # Fallback to NUM_WORK_UNITS env var when flag is None
        with mock.patch.object(verifier_cli, "get_flag", return_value=None):
            with mock.patch.dict("os.environ", {"NUM_WORK_UNITS": "2"}):
                self.assertEqual(verifier_cli.resolve_expected_work_units(3), 2)

            # Non-positive env var values fallback to default
            with mock.patch.dict("os.environ", {"NUM_WORK_UNITS": "0"}):
                self.assertEqual(verifier_cli.resolve_expected_work_units(3), 3)

            with mock.patch.dict("os.environ", {"NUM_WORK_UNITS": "-5"}):
                self.assertEqual(verifier_cli.resolve_expected_work_units(3), 3)

            # Fallback to default when env var is invalid or unset
            with mock.patch.dict("os.environ", {"NUM_WORK_UNITS": "invalid"}):
                self.assertEqual(verifier_cli.resolve_expected_work_units(3), 3)

            with mock.patch.dict("os.environ", {}, clear=True):
                self.assertEqual(verifier_cli.resolve_expected_work_units(3), 3)

    def test_run_main(self) -> None:
        mock_fn = mock.MagicMock()
        with mock.patch.object(verifier_cli.app, "run") as mock_run:
            verifier_cli.run_main(mock_fn)
            mock_run.assert_called_once_with(mock_fn)


if __name__ == "__main__":
    unittest.main()
