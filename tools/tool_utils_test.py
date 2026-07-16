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

"""Unit tests for tool_utils module."""

import io
import json
import sys
import unittest
from unittest.mock import MagicMock, patch

from tool_utils import HandledError, run_tool


class TestToolUtils(unittest.TestCase):
    """Test suite for tool_utils helper functions."""

    def test_run_tool_success(self):
        """Test successful execution of tool runner."""
        parser = MagicMock()
        mock_args = MagicMock()
        parser.parse_args.return_value = mock_args

        main_fn = MagicMock(return_value={"result": "ok"})

        captured_stdout = io.StringIO()
        with (
            patch("sys.stdout", captured_stdout),
            self.assertRaises(SystemExit) as exit_ctx,
        ):
            run_tool(parser, main_fn)

        # Assert exit code 0
        self.assertEqual(exit_ctx.exception.code, 0)

        # Verify parser and main_fn were called
        parser.parse_args.assert_called_once()
        main_fn.assert_called_once_with(mock_args)

        # Verify stdout json
        output = json.loads(captured_stdout.getvalue())
        self.assertEqual(output["status"], "success")
        self.assertEqual(output["result"], {"result": "ok"})

    def test_run_tool_handled_error(self):
        """Test tool runner with a HandledError exception."""
        parser = MagicMock()
        mock_args = MagicMock()
        parser.parse_args.return_value = mock_args

        def raise_handled_error(args):
            raise HandledError("something went wrong", "please fix it")

        main_fn = MagicMock(side_effect=raise_handled_error)

        captured_stderr = io.StringIO()
        with (
            patch("sys.stderr", captured_stderr),
            self.assertRaises(SystemExit) as exit_ctx,
        ):
            run_tool(parser, main_fn)

        # Assert exit code 1
        self.assertEqual(exit_ctx.exception.code, 1)

        # Verify stderr json
        output = json.loads(captured_stderr.getvalue())
        self.assertEqual(output["status"], "error")
        self.assertEqual(output["error_type"], "handled")
        self.assertEqual(output["message"], "something went wrong")
        self.assertEqual(output["instruction"], "please fix it")

    def test_run_tool_unhandled_error(self):
        """Test tool runner with an unexpected Exception."""
        parser = MagicMock()
        mock_args = MagicMock()
        parser.parse_args.return_value = mock_args

        def raise_unexpected_error(args):
            raise ValueError("unexpected value")

        main_fn = MagicMock(side_effect=raise_unexpected_error)

        captured_stderr = io.StringIO()
        with (
            patch("sys.stderr", captured_stderr),
            self.assertRaises(SystemExit) as exit_ctx,
        ):
            run_tool(parser, main_fn)

        # Assert exit code 2
        self.assertEqual(exit_ctx.exception.code, 2)

        # Verify stderr json
        output = json.loads(captured_stderr.getvalue())
        self.assertEqual(output["status"], "error")
        self.assertEqual(output["error_type"], "unhandled")
        self.assertEqual(output["message"], "unexpected value")
        self.assertIn("traceback", output)


if __name__ == "__main__":
    unittest.main()
