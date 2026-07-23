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

"""Unit tests for resilience state machine, backoff, and terminal diagnostic parsing."""

import json
import threading
import time
import unittest
from unittest.mock import MagicMock

from xmc.buffer import MetricTuple
from xmc.client import XMCAPIError, XMCNetworkError
from xmc.flusher import MetricsFlusher
from xmc.network import SendResult
from xmc.resilience import ResilienceState, ResilienceStateMachine, classify_outcome


class TestResilienceStateMachine(unittest.TestCase):
    """Tests state machine transitions, backoff calculation, and full jitter bounds."""

    def test_initial_state_is_healthy(self) -> None:
        sm = ResilienceStateMachine()
        self.assertEqual(sm.state, ResilienceState.HEALTHY)
        self.assertEqual(sm.attempt, 0)

    def test_transient_failure_transitions_to_backoff(self) -> None:
        sm = ResilienceStateMachine(initial_backoff=1.0, max_backoff=10.0, backoff_factor=2.0)
        delay1 = sm.record_transient_failure()

        self.assertEqual(sm.state, ResilienceState.BACKOFF)
        self.assertEqual(sm.attempt, 1)
        # Full jitter: 0 <= delay1 <= min(10.0, 1.0 * (2^0)) = 1.0
        self.assertGreaterEqual(delay1, 0.0)
        self.assertLessEqual(delay1, 1.0)

        delay2 = sm.record_transient_failure()
        self.assertEqual(sm.state, ResilienceState.BACKOFF)
        self.assertEqual(sm.attempt, 2)
        # Full jitter: 0 <= delay2 <= min(10.0, 1.0 * (2^1)) = 2.0
        self.assertGreaterEqual(delay2, 0.0)
        self.assertLessEqual(delay2, 2.0)

    def test_record_success_resets_state_to_healthy(self) -> None:
        sm = ResilienceStateMachine()
        sm.record_transient_failure()
        self.assertEqual(sm.state, ResilienceState.BACKOFF)

        sm.record_success()
        self.assertEqual(sm.state, ResilienceState.HEALTHY)
        self.assertEqual(sm.attempt, 0)

    def test_record_terminal_failure_resets_state(self) -> None:
        sm = ResilienceStateMachine()
        sm.record_transient_failure()
        self.assertEqual(sm.state, ResilienceState.BACKOFF)

        sm.record_terminal_failure()
        self.assertEqual(sm.state, ResilienceState.HEALTHY)
        self.assertEqual(sm.attempt, 0)

    def test_full_jitter_bounds_and_max_backoff_cap(self) -> None:
        sm = ResilienceStateMachine(initial_backoff=1.0, max_backoff=4.0, backoff_factor=2.0)
        # Force attempt to 10
        sm.attempt = 10
        for _ in range(100):
            delay = sm.compute_backoff_delay()
            self.assertGreaterEqual(delay, 0.0)
            self.assertLessEqual(delay, 4.0)

    def test_classify_outcome(self) -> None:
        err = XMCAPIError(status_code=422, message="Unprocessable", response_body="{}")
        cls, code, body = classify_outcome(err)
        self.assertEqual(cls, "terminal")
        self.assertEqual(code, 422)


class TestMetricsFlusherResilience(unittest.TestCase):
    """Tests flusher integration with resilience state machine, backoff, interruptible sleep, and 400/422 parsing."""

    def test_transient_error_backoff_and_retry_recovery(self) -> None:
        calls = 0

        def flaky_flush_fn(items: list[MetricTuple]) -> None:
            nonlocal calls
            calls += 1
            if calls == 1:
                raise XMCAPIError(status_code=429, message="Too Many Requests")
            if calls == 2:
                raise XMCAPIError(status_code=503, message="Service Unavailable")

        flusher = MetricsFlusher(
            flush_fn=flaky_flush_fn,
            flush_interval=60.0,
            batch_size=1,
            initial_backoff=0.01,
            max_backoff=0.05,
            max_retries=5,
        )

        flusher.log_metric("m1", 1.0, 1)

        # Wait for flusher background thread to retry and recover
        time.sleep(0.2)

        self.assertEqual(calls, 3)
        self.assertEqual(flusher.state, ResilienceState.HEALTHY)
        flusher.shutdown()

    def test_interruptible_sleep_on_shutdown(self) -> None:
        flush_started = threading.Event()

        def hanging_flush_fn(items: list[MetricTuple]) -> None:
            flush_started.set()
            raise XMCNetworkError("Network down")

        flusher = MetricsFlusher(
            flush_fn=hanging_flush_fn,
            flush_interval=60.0,
            batch_size=1,
            initial_backoff=10.0,  # Long backoff delay
            max_backoff=60.0,
            max_retries=5,
        )

        flusher.log_metric("m1", 1.0, 1)
        self.assertTrue(flush_started.wait(timeout=2.0))

        start_time = time.monotonic()
        flusher.shutdown(timeout=2.0)
        elapsed = time.monotonic() - start_time

        # Shutdown should unblock interruptible sleep immediately (< 0.5s) instead of waiting 10s
        self.assertLess(elapsed, 0.5)
        self.assertTrue(flusher.is_shutdown)

    def test_terminal_422_diagnostic_parsing_and_immediate_discard(self) -> None:
        calls = 0
        json_body = json.dumps(
            {
                "error": "unprocessable_entity",
                "failed_records": [
                    {"index": 0, "reason": "invalid metric name"},
                    {"index": 2, "reason": "value out of range"},
                ],
            }
        )

        def terminal_flush_fn(items: list[MetricTuple]) -> None:
            nonlocal calls
            calls += 1
            raise XMCAPIError(status_code=422, message="Unprocessable", response_body=json_body)

        flusher = MetricsFlusher(
            flush_fn=terminal_flush_fn,
            flush_interval=60.0,
            batch_size=1,
            initial_backoff=0.01,
        )

        flusher.buffer.put("m1", 1.0, 1)
        with self.assertLogs("xmc.resilience", level="CRITICAL") as cm:
            flushed_count = flusher.flush()

        self.assertEqual(flushed_count, 0)

        # Batch should be discarded immediately on 422 without retries
        self.assertEqual(calls, 1)
        self.assertEqual(flusher.state, ResilienceState.HEALTHY)

        # Check CRITICAL log outputs for detailed index/reason diagnosis
        log_output = "\n".join(cm.output)
        self.assertIn("index 0 failed due to: invalid metric name", log_output)
        self.assertIn("index 2 failed due to: value out of range", log_output)

    def test_terminal_400_send_result_discard(self) -> None:
        mock_client = MagicMock()
        mock_client.send.side_effect = lambda body: SendResult.INVALID_PAYLOAD

        flusher = MetricsFlusher(
            client=mock_client,
            flush_interval=60.0,
            batch_size=1,
            initial_backoff=0.01,
        )

        flusher.buffer.put("m1", 1.0, 1)
        with self.assertLogs("xmc.resilience", level="CRITICAL") as cm:
            flushed_count = flusher.flush()

        self.assertEqual(flushed_count, 0)

        self.assertEqual(mock_client.send.call_count, 1)
        self.assertEqual(flusher.state, ResilienceState.HEALTHY)
        log_output = "\n".join(cm.output)
        self.assertIn("Terminal payload error (HTTP 400)", log_output)


if __name__ == "__main__":
    unittest.main()
