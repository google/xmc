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

"""Unit tests for ui_diagnostics module."""

import logging
import os
import tempfile
import unittest
from pathlib import Path
from unittest.mock import MagicMock, patch

from test.e2e.lib.ui_diagnostics import (
    get_screenshots_dir,
    save_screenshot,
    setup_logging,
)


class TestUIDiagnostics(unittest.TestCase):
    """Tests for screenshot and diagnostic trace utilities."""

    def test_get_screenshots_dir_with_env(self):
        with tempfile.TemporaryDirectory() as tmp_dir:
            with patch.dict(os.environ, {"XMC_SCREENSHOTS_DIR": tmp_dir}):
                res = get_screenshots_dir()
                self.assertEqual(res, Path(tmp_dir))
                self.assertTrue(res.is_dir())

    def test_get_screenshots_dir_default(self):
        with patch.dict(os.environ, {}, clear=True):
            res = get_screenshots_dir()
            self.assertIn("xmc_e2e_screenshots", str(res))
            self.assertTrue(res.is_dir())

    def test_setup_logging_creates_file_handler(self):
        with tempfile.TemporaryDirectory() as tmp_dir:
            handler = setup_logging(Path(tmp_dir))
            try:
                self.assertIsInstance(handler, logging.FileHandler)
                trace_file = Path(tmp_dir) / "playwright_e2e_trace.txt"
                self.assertTrue(trace_file.exists())
            finally:
                handler.close()
                logging.getLogger().removeHandler(handler)

    def test_save_screenshot_success(self):
        with tempfile.TemporaryDirectory() as tmp_dir:
            mock_page = MagicMock()
            path = save_screenshot(mock_page, Path(tmp_dir), "test_shot")
            expected_path = Path(tmp_dir) / "test_shot.png"
            self.assertEqual(path, str(expected_path))
            mock_page.screenshot.assert_called_once_with(path=str(expected_path), full_page=True)

    def test_save_screenshot_handles_exception(self):
        with tempfile.TemporaryDirectory() as tmp_dir:
            mock_page = MagicMock()
            mock_page.screenshot.side_effect = RuntimeError("Playwright error")
            path = save_screenshot(mock_page, Path(tmp_dir), "error_shot")
            self.assertEqual(path, "")


if __name__ == "__main__":
    unittest.main()
