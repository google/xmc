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

"""Unit tests for init_step sidecar example."""

import unittest
from unittest.mock import mock_open, patch

import init_step


class TestInitStep(unittest.TestCase):
    """Test suite for init_step container initialization logic."""

    @patch("builtins.open", new_callable=mock_open)
    @patch("os.makedirs")
    def test_main_creates_directory_and_writes_flag(
        self, mock_makedirs: unittest.mock.MagicMock, mock_file: unittest.mock.MagicMock
    ) -> None:
        """Verifies that main defensively creates parent directory before opening flag file."""
        init_step.main()

        mock_makedirs.assert_called_once_with("/dev/disk-scratch", exist_ok=True)
        mock_file.assert_called_once_with(
            "/dev/disk-scratch/init_ready.flag", "w", encoding="utf-8"
        )
        mock_file().write.assert_called_once_with("Initialized by init-setup on unknown-pod\n")


if __name__ == "__main__":
    unittest.main()
