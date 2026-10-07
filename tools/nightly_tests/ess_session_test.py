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

"""Unit tests for tools.nightly_tests.ess_session module."""

from __future__ import annotations

import io
import unittest
from unittest import mock

from tools.nightly_tests import ess_session


class EssSessionTest(unittest.TestCase):
    """Test cases for ess_session.py context managers."""

    def test_ess_channel_context(self) -> None:
        mock_stub = mock.MagicMock()
        mock_channel = mock.MagicMock()

        with mock.patch(
            "tools.nightly_tests.xmc_entities_validator.create_authenticated_stub",
            return_value=(mock_stub, mock_channel),
        ) as mock_create_stub:
            with ess_session.ess_channel_context("ess-server:8080") as (stub, channel):
                mock_create_stub.assert_called_once_with("ess-server:8080")
                self.assertEqual(stub, mock_stub)
                self.assertEqual(channel, mock_channel)
                mock_channel.close.assert_not_called()

            # Verify channel was closed after context exit
            mock_channel.close.assert_called_once()

    def test_ess_channel_context_closes_on_exception_and_handles_none_channel(self) -> None:
        mock_stub = mock.MagicMock()
        mock_channel = mock.MagicMock()

        with (
            mock.patch.dict("os.environ", {"XMANAGER_ENDPOINT": "env-ess:9090"}, clear=True),
            mock.patch(
                "tools.nightly_tests.xmc_entities_validator.create_authenticated_stub",
                return_value=(mock_stub, mock_channel),
            ) as mock_create_stub,
            self.assertRaises(RuntimeError),
        ):
            with ess_session.ess_channel_context(None):
                mock_create_stub.assert_called_once_with("env-ess:9090")
                raise RuntimeError("boom")
        mock_channel.close.assert_called_once()

        with mock.patch(
            "tools.nightly_tests.xmc_entities_validator.create_authenticated_stub",
            return_value=(mock_stub, None),
        ):
            with ess_session.ess_channel_context("ess-server:8080") as (stub, channel):
                self.assertEqual(stub, mock_stub)
                self.assertIsNone(channel)

    def test_handle_verification_errors_success(self) -> None:
        # Block executes without exception
        with ess_session.handle_verification_errors():
            pass

    def test_handle_verification_errors_assertion(self) -> None:
        with mock.patch("sys.stderr", new_callable=io.StringIO) as mock_stderr:
            with self.assertRaises(SystemExit) as ctx:
                with ess_session.handle_verification_errors():
                    raise AssertionError("WorkUnit count mismatch")
            self.assertEqual(ctx.exception.code, 1)
            self.assertIn("ASSERTION FAILED: WorkUnit count mismatch", mock_stderr.getvalue())

    def test_handle_verification_errors_timeout(self) -> None:
        with mock.patch("sys.stderr", new_callable=io.StringIO) as mock_stderr:
            with self.assertRaises(SystemExit) as ctx:
                with ess_session.handle_verification_errors():
                    raise TimeoutError("Timed out waiting for completion")
            self.assertEqual(ctx.exception.code, 1)
            self.assertIn("ASSERTION FAILED: Timed out waiting", mock_stderr.getvalue())

    def test_handle_verification_errors_generic_exception(self) -> None:
        with mock.patch("sys.stderr", new_callable=io.StringIO) as mock_stderr:
            with self.assertRaises(SystemExit) as ctx:
                with ess_session.handle_verification_errors():
                    raise RuntimeError("gRPC connection broken")
            self.assertEqual(ctx.exception.code, 2)
            self.assertIn("UNEXPECTED ERROR during verification", mock_stderr.getvalue())


if __name__ == "__main__":
    unittest.main()
