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

"""Unit tests for test.e2e.lib.polling."""

from __future__ import annotations

import json
import subprocess
import unittest
from unittest import mock

from test.e2e.lib.polling import PollTimeoutError, TransientProbeError, poll_until


@mock.patch("test.e2e.lib.polling.time.sleep")
class PollUntilTest(unittest.TestCase):
    """Tests for poll_until."""

    def test_returns_first_value_after_sleeping_between_attempts(
        self, mock_sleep: mock.MagicMock
    ) -> None:
        """poll_until keeps polling while probe returns None and returns the first value."""
        probe = mock.Mock(side_effect=[None, None, {"ready": True}])

        got = poll_until(probe, "readiness", timeout_seconds=5.0, interval_seconds=0.5)

        self.assertEqual(got, {"ready": True})
        self.assertEqual(probe.call_count, 3)
        mock_sleep.assert_has_calls([mock.call(0.5), mock.call(0.5)])

    def test_retries_transient_kubectl_errors(self, mock_sleep: mock.MagicMock) -> None:
        """poll_until retries subprocess failures and malformed JSON output."""
        probe = mock.Mock(
            side_effect=[
                subprocess.CalledProcessError(returncode=1, cmd=["kubectl"], stderr="refused"),
                subprocess.TimeoutExpired(cmd=["kubectl"], timeout=10.0),
                json.JSONDecodeError("Expecting value", "", 0),
                "done",
            ]
        )

        got = poll_until(probe, "readiness", timeout_seconds=5.0)

        self.assertEqual(got, "done")
        self.assertEqual(mock_sleep.call_count, 3)

    @mock.patch("test.e2e.lib.polling.time.monotonic")
    def test_chains_transient_error_of_last_attempt(
        self, mock_monotonic: mock.MagicMock, mock_sleep: mock.MagicMock
    ) -> None:
        """A timeout keeps the transient failure of the last attempt as its cause."""
        mock_monotonic.side_effect = [0.0, 10.0]
        error = subprocess.CalledProcessError(returncode=1, cmd=["kubectl"], stderr="refused")
        probe = mock.Mock(side_effect=error)

        with self.assertRaises(PollTimeoutError) as ctx:
            poll_until(probe, "readiness", timeout_seconds=5.0)

        self.assertIs(ctx.exception.__cause__, error)

    @mock.patch("test.e2e.lib.polling.time.monotonic")
    def test_does_not_chain_transient_error_of_earlier_attempt(
        self, mock_monotonic: mock.MagicMock, mock_sleep: mock.MagicMock
    ) -> None:
        """A timeout after a last attempt that ran but found nothing has no cause."""
        mock_monotonic.side_effect = [0.0, 1.0, 10.0]
        error = subprocess.CalledProcessError(returncode=1, cmd=["kubectl"], stderr="refused")
        probe = mock.Mock(side_effect=[error, None])

        with self.assertRaises(PollTimeoutError) as ctx:
            poll_until(probe, "readiness", timeout_seconds=5.0)

        self.assertIsNone(ctx.exception.__cause__)

    @mock.patch("test.e2e.lib.polling.time.monotonic")
    def test_raises_after_deadline_without_trailing_sleep(
        self, mock_monotonic: mock.MagicMock, mock_sleep: mock.MagicMock
    ) -> None:
        """poll_until raises PollTimeoutError once the deadline passes, without sleeping."""
        mock_monotonic.side_effect = [0.0, 10.0]
        probe = mock.Mock(return_value=None)

        with self.assertRaisesRegex(PollTimeoutError, "Timed out after 5.0s waiting for readiness"):
            poll_until(probe, "readiness", timeout_seconds=5.0)

        probe.assert_called_once()
        mock_sleep.assert_not_called()

    def test_propagates_non_transient_errors(self, mock_sleep: mock.MagicMock) -> None:
        """poll_until does not retry errors other than transient failures."""
        probe = mock.Mock(side_effect=ValueError("bad state"))

        with self.assertRaisesRegex(ValueError, "bad state"):
            poll_until(probe, "readiness", timeout_seconds=5.0)

        probe.assert_called_once()
        mock_sleep.assert_not_called()

    def test_retries_transient_probe_errors(self, mock_sleep: mock.MagicMock) -> None:
        """poll_until retries the transient failures that a probe reports."""
        probe = mock.Mock(side_effect=[TransientProbeError("503 unavailable"), "done"])

        got = poll_until(probe, "readiness", timeout_seconds=5.0, interval_seconds=0.5)

        self.assertEqual(got, "done")
        self.assertEqual(probe.call_count, 2)
        mock_sleep.assert_called_once_with(0.5)

    def test_propagates_runtime_errors_that_are_not_transient(
        self, mock_sleep: mock.MagicMock
    ) -> None:
        """poll_until retries only the TransientProbeError subclass of RuntimeError."""
        probe = mock.Mock(side_effect=RuntimeError("404 not found"))

        with self.assertRaisesRegex(RuntimeError, "404 not found"):
            poll_until(probe, "readiness", timeout_seconds=5.0)

        probe.assert_called_once()
        mock_sleep.assert_not_called()


if __name__ == "__main__":
    unittest.main()
