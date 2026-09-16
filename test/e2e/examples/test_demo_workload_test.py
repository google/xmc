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

"""Unit tests for test_demo_workload module."""

from __future__ import annotations

import unittest
from pathlib import Path
from unittest.mock import MagicMock, patch

from test.e2e.examples.example_runner import ExampleLaunchResult
from test.e2e.examples.test_demo_workload import (
    _launch_demo_workload,
    _verify_demo_workload_backend,
    _verify_demo_workload_ui,
    main,
    test_demo_workload_lifecycle,
)


class TestDemoWorkload(unittest.TestCase):
    """Unit test suite for test_demo_workload module."""

    @patch("test.e2e.examples.test_demo_workload.execute_example_launch")
    def test_launch_demo_workload(self, mock_execute: MagicMock) -> None:
        mock_execute.return_value = ExampleLaunchResult(
            xid="123",
            wid="1",
            raw_output="Created Work Unit successfully: experiments/123/workUnits/1",
        )
        mock_page = MagicMock()
        mock_auth = MagicMock()
        screenshots_dir = Path("/tmp/screenshots")

        result = _launch_demo_workload(mock_page, mock_auth, screenshots_dir)

        mock_auth.authenticate.assert_called_once()
        mock_execute.assert_called_once()
        self.assertEqual(result.xid, "123")

    @patch("tools.nightly_tests.demo_workload_verify.main")
    @patch("tools.nightly_tests.demo_workload_verify.FLAGS")
    def test_verify_demo_workload_backend(
        self, mock_flags: MagicMock, mock_verify_main: MagicMock
    ) -> None:
        _verify_demo_workload_backend("42", endpoint="127.0.0.1:50051")
        mock_flags.assert_called_once_with(
            [
                "demo_workload_verify",
                "--xid=42",
                "--endpoint=127.0.0.1:50051",
                "--wait_for_completion=false",
            ],
            known_only=True,
        )
        mock_verify_main.assert_called_once_with(["demo_workload_verify"])

    @patch("test.e2e.examples.test_demo_workload.save_screenshot")
    def test_verify_demo_workload_ui(self, mock_save_screenshot: MagicMock) -> None:
        mock_page = MagicMock()
        mock_loc = MagicMock()
        mock_page.get_by_text.return_value.first = mock_loc
        screenshots_dir = Path("/tmp/screenshots")

        _verify_demo_workload_ui(mock_page, "42", screenshots_dir)

        mock_page.goto.assert_called_once()
        self.assertEqual(mock_save_screenshot.call_count, 2)

    @patch("test.e2e.examples.test_demo_workload._verify_demo_workload_ui")
    @patch("test.e2e.examples.test_demo_workload._verify_demo_workload_backend")
    @patch("test.e2e.examples.test_demo_workload._launch_demo_workload")
    def test_demo_workload_lifecycle(
        self,
        mock_launch: MagicMock,
        mock_verify_backend: MagicMock,
        mock_verify_ui: MagicMock,
    ) -> None:
        mock_launch.return_value = ExampleLaunchResult(xid="100", wid="1")
        mock_page = MagicMock()
        mock_auth = MagicMock()
        screenshots_dir = Path("/tmp/screenshots")

        res = test_demo_workload_lifecycle(mock_page, screenshots_dir, mock_auth)

        mock_launch.assert_called_once()
        mock_verify_backend.assert_called_once_with("100")
        mock_verify_ui.assert_called_once_with(
            page=mock_page, xid="100", screenshots_dir=screenshots_dir
        )
        self.assertEqual(res["xid"], "100")
        self.assertEqual(res["status"], "PASSED")

    def test_main_success(self) -> None:
        mock_playwright_mod = MagicMock()
        mock_p = MagicMock()
        mock_browser = MagicMock()
        mock_context = MagicMock()
        mock_page = MagicMock()

        mock_playwright_mod.sync_playwright.return_value.__enter__.return_value = mock_p
        mock_p.chromium.launch.return_value.__enter__.return_value = mock_browser
        mock_browser.new_context.return_value.__enter__.return_value = mock_context
        mock_context.new_page.return_value = mock_page

        with (
            patch.dict("sys.modules", {"playwright.sync_api": mock_playwright_mod}),
            patch(
                "test.e2e.examples.test_demo_workload.test_demo_workload_lifecycle"
            ) as mock_lifecycle,
        ):
            main()
            mock_lifecycle.assert_called_once()

    def test_main_failure(self) -> None:
        mock_playwright_mod = MagicMock()
        mock_p = MagicMock()
        mock_browser = MagicMock()
        mock_context = MagicMock()
        mock_page = MagicMock()

        mock_playwright_mod.sync_playwright.return_value.__enter__.return_value = mock_p
        mock_p.chromium.launch.return_value.__enter__.return_value = mock_browser
        mock_browser.new_context.return_value.__enter__.return_value = mock_context
        mock_context.new_page.return_value = mock_page

        with (
            patch.dict("sys.modules", {"playwright.sync_api": mock_playwright_mod}),
            patch(
                "test.e2e.examples.test_demo_workload.test_demo_workload_lifecycle",
                side_effect=RuntimeError("Simulation error"),
            ),
        ):
            with self.assertRaises(SystemExit) as ctx:
                main()
            self.assertEqual(ctx.exception.code, 1)


if __name__ == "__main__":
    unittest.main()
