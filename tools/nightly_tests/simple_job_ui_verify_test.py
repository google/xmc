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

"""Unit tests for simple_job_ui_verify CLI module."""

from __future__ import annotations

from unittest import mock

from absl import app
from absl.testing import absltest, flagsaver

from tools.nightly_tests import simple_job_ui_verify, ui_entities_validator


class SimpleJobUIVerifyTest(absltest.TestCase):
    """Test suite for simple_job_ui_verify module."""

    def test_verify_simple_job_ui_success(self) -> None:
        mock_session = mock.MagicMock(spec=ui_entities_validator.XMCUIBrowserSession)
        mock_session.screenshots_dir = "/tmp/xmc_screenshots"
        mock_session.assert_experiment_header.return_value = "Demo Workload - 42"
        mock_session_cls = mock.MagicMock()
        mock_session_cls.return_value.__enter__.return_value = mock_session

        with (
            mock.patch.object(ui_entities_validator, "XMCUIBrowserSession", mock_session_cls),
            mock.patch.object(simple_job_ui_verify.common, "print_success_summary") as mock_summary,
        ):
            simple_job_ui_verify.verify_simple_job_ui(
                xid=42,
                ui_base_url="https://xmc.example.com",
                auth_token="test-token",
                dex_url="https://dex.example.com",
                user_email="user@google.com",
                headless=True,
                screenshots_dir="/tmp/xmc_screenshots",
            )

        mock_session_cls.assert_called_once_with(
            ui_base_url="https://xmc.example.com",
            auth_token="test-token",
            user_email="user@google.com",
            dex_url="https://dex.example.com",
            headless=True,
            screenshots_dir="/tmp/xmc_screenshots",
        )
        mock_session.navigate_to_experiment.assert_called_once_with(42)
        mock_session.assert_experiment_header.assert_called_once_with(
            expected_title_pattern="Demo Workload"
        )
        mock_session.take_screenshot.assert_any_call("ui_step2_header_xid_42.png")
        mock_session.assert_work_units_tab.assert_called_once_with(
            min_units=simple_job_ui_verify.EXPECTED_WORK_UNITS,
            expected_names=simple_job_ui_verify.EXPECTED_WORK_UNIT_NAMES,
        )
        mock_session.take_screenshot.assert_any_call("ui_step3_work_units_xid_42.png")
        mock_summary.assert_called_once()

    def test_verify_simple_job_ui_failure_raises_verification_error(self) -> None:
        mock_session = mock.MagicMock(spec=ui_entities_validator.XMCUIBrowserSession)
        mock_session.assert_experiment_header.side_effect = (
            ui_entities_validator.UIVerificationError("Header title missing")
        )
        mock_session_cls = mock.MagicMock()
        mock_session_cls.return_value.__enter__.return_value = mock_session

        with mock.patch.object(ui_entities_validator, "XMCUIBrowserSession", mock_session_cls):
            with self.assertRaises(ui_entities_validator.UIVerificationError):
                simple_job_ui_verify.verify_simple_job_ui(
                    xid=42,
                    ui_base_url="https://xmc.example.com",
                )

    def test_main_rejects_extra_positional_args(self) -> None:
        with self.assertRaises(app.UsageError):
            simple_job_ui_verify.main(["simple_job_ui_verify.py", "extra_arg"])

    @flagsaver.flagsaver(xid=None)
    def test_main_missing_xid_exits_with_error(self) -> None:
        with self.assertRaises(SystemExit) as ctx:
            simple_job_ui_verify.main(["simple_job_ui_verify.py"])
        self.assertEqual(ctx.exception.code, 2)

    @flagsaver.flagsaver(
        xid="experiments/42",
        ui_base_url="https://xmc.example.com",
        auth_token="tok",
        dex_url="https://dex.example.com",
        user_email="user@google.com",
        headless=False,
        screenshots_dir="/tmp/shots",
    )
    def test_main_success_and_failure_exit_codes(self) -> None:
        with mock.patch.object(simple_job_ui_verify, "verify_simple_job_ui") as mock_verify:
            simple_job_ui_verify.main(["simple_job_ui_verify.py"])
            mock_verify.assert_called_once_with(
                xid="42",
                ui_base_url="https://xmc.example.com",
                auth_token="tok",
                dex_url="https://dex.example.com",
                user_email="user@google.com",
                headless=False,
                screenshots_dir="/tmp/shots",
            )

        with mock.patch.object(
            simple_job_ui_verify,
            "verify_simple_job_ui",
            side_effect=ui_entities_validator.UIVerificationError("Verification failed"),
        ):
            with self.assertRaises(SystemExit) as ctx:
                simple_job_ui_verify.main(["simple_job_ui_verify.py"])
            self.assertEqual(ctx.exception.code, 1)


if __name__ == "__main__":
    absltest.main()
