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

"""Unit tests for test_clickhouse_schema.py."""

import unittest
import urllib.error
from unittest.mock import MagicMock, patch

from scripts import test_clickhouse_schema


class TestClickHouseSchema(unittest.TestCase):
    """Test suite for ClickHouse schema helper functions."""

    def test_parse_args_defaults(self) -> None:
        """Test argument parsing default values."""
        args = test_clickhouse_schema.parse_args([])
        self.assertEqual(args.host, "localhost")
        self.assertEqual(args.port, 8123)
        self.assertEqual(args.tcp_port, 9000)
        self.assertFalse(args.cleanup)

    def test_parse_args_custom(self) -> None:
        """Test argument parsing with custom flags."""
        args = test_clickhouse_schema.parse_args(
            [
                "--host",
                "127.0.0.1",
                "--port",
                "8124",
                "--tcp-port",
                "9001",
                "--cleanup",
            ]
        )
        self.assertEqual(args.host, "127.0.0.1")
        self.assertEqual(args.port, 8124)
        self.assertEqual(args.tcp_port, 9001)
        self.assertTrue(args.cleanup)

    @patch("urllib.request.urlopen")
    def test_is_clickhouse_responsive_success(self, mock_urlopen: MagicMock) -> None:
        """Test is_clickhouse_responsive when server returns Ok."""
        mock_resp = MagicMock()
        mock_resp.status = 200
        mock_resp.read.return_value = b"Ok.\n"
        mock_resp.__enter__.return_value = mock_resp
        mock_urlopen.return_value = mock_resp

        self.assertTrue(test_clickhouse_schema.is_clickhouse_responsive("localhost", 8123))

    @patch("urllib.request.urlopen")
    def test_is_clickhouse_responsive_failure(self, mock_urlopen: MagicMock) -> None:
        """Test is_clickhouse_responsive when server fails to respond."""
        mock_urlopen.side_effect = urllib.error.URLError("Connection refused")
        self.assertFalse(test_clickhouse_schema.is_clickhouse_responsive("localhost", 8123))

    @patch("urllib.request.urlopen")
    def test_execute_clickhouse_query(self, mock_urlopen: MagicMock) -> None:
        """Test execute_clickhouse_query sends POST request and reads response."""
        mock_resp = MagicMock()
        mock_resp.read.return_value = b"24.3.1\n"
        mock_resp.__enter__.return_value = mock_resp
        mock_urlopen.return_value = mock_resp

        result = test_clickhouse_schema.execute_clickhouse_query(
            "localhost", 8123, "SELECT version()"
        )
        self.assertEqual(result, "24.3.1")


if __name__ == "__main__":
    unittest.main()
