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

"""Unit tests for cooperative stopping of harness work."""

from __future__ import annotations

import faulthandler
import itertools
import signal
import sys
import threading
import time
import unittest
from collections.abc import Callable
from types import FrameType
from typing import Any

from test.e2e.lib.cancellation import HarnessStoppedError, StopController

# Upper bound for a halted wait to return; far below the requested wait.
_WAKE_BOUND_SECONDS = 5
# Upper bound for the signal delivery test, which a deadlocked signal handler would hang.
_DEADLOCK_BOUND_SECONDS = 30


def _call_with_signal_before_line(call: Callable[[], object], line: int) -> bool:
    """Calls ``call`` and raises SIGTERM just before the ``line``-th line that it executes.

    The lines of the functions that ``call`` calls count as well, so the signal also arrives
    while ``call`` holds any lock that it takes. ``signal.raise_signal`` runs the Python
    signal handler right away, on this thread, as the interpreter does for a real signal.

    Args:
        call: The function to interrupt.
        line: 1-based number of the executed line before which the signal is raised.

    Returns:
        Whether the signal was raised, which is False once ``call`` executes fewer lines.
    """
    lines_executed = 0

    def trace(_frame: FrameType, event: str, _arg: Any) -> Callable[..., Any]:
        nonlocal lines_executed
        if event == "line":
            lines_executed += 1
            if lines_executed == line:
                signal.raise_signal(signal.SIGTERM)
        return trace

    previous_trace = sys.gettrace()
    sys.settrace(trace)
    try:
        call()
    finally:
        sys.settrace(previous_trace)
    return lines_executed >= line


class TestStopController(unittest.TestCase):
    """Tests for StopController."""

    def setUp(self):
        self.controller = StopController()

    def _restore_stop_signal_handlers_on_cleanup(self) -> None:
        for signum in (signal.SIGINT, signal.SIGTERM):
            self.addCleanup(signal.signal, signum, signal.getsignal(signum))

    def test_wait_returns_after_timeout_when_not_halted(self):
        self.controller.wait(0.01)

        self.controller.raise_if_halted()

    def test_wait_ends_early_when_attempt_is_aborted_from_another_thread(self):
        threading.Timer(0.1, self.controller.abort_attempt).start()
        start = time.monotonic()

        with self.assertRaises(HarnessStoppedError):
            self.controller.wait(60)
        self.assertLess(time.monotonic() - start, _WAKE_BOUND_SECONDS)

    def test_begin_attempt_clears_attempt_abort(self):
        self.controller.abort_attempt()

        self.controller.begin_attempt()

        self.controller.raise_if_halted()
        self.assertIsNone(self.controller.stop_signal)

    def test_begin_attempt_keeps_requested_stop(self):
        self.controller.request_stop()

        self.controller.begin_attempt()

        with self.assertRaises(HarnessStoppedError):
            self.controller.raise_if_halted()

    def test_first_stop_signal_wins(self):
        self.controller.request_stop(signal.SIGINT)
        self.controller.request_stop(signal.SIGTERM)
        self.controller.request_stop()

        self.assertEqual(self.controller.stop_signal, signal.SIGINT)

    def test_deadline_requests_stop_without_signal(self):
        self.controller.start_deadline(0.01).join(_WAKE_BOUND_SECONDS)

        with self.assertRaises(HarnessStoppedError):
            self.controller.wait(60)
        self.assertIsNone(self.controller.stop_signal)

    def test_cancelled_deadline_does_not_stop(self):
        timer = self.controller.start_deadline(0.05)
        timer.cancel()
        timer.join(_WAKE_BOUND_SECONDS)

        self.controller.wait(0.1)

    def test_signal_requests_stop_without_logging_and_ignores_further_signals(self):
        self._restore_stop_signal_handlers_on_cleanup()
        self.controller.install_signal_handlers()

        with self.assertNoLogs():
            signal.raise_signal(signal.SIGTERM)

        self.assertEqual(self.controller.stop_signal, signal.SIGTERM)
        self.assertIs(signal.getsignal(signal.SIGINT), signal.SIG_IGN)
        self.assertIs(signal.getsignal(signal.SIGTERM), signal.SIG_IGN)
        with self.assertRaises(HarnessStoppedError):
            self.controller.raise_if_halted()

    def test_signal_at_any_line_of_an_attempt_transition_stops_without_deadlock(self):
        # A handler that waited for a lock held by the interrupted transition would hang this
        # thread forever; the watchdog then ends the test process with a traceback instead.
        faulthandler.dump_traceback_later(_DEADLOCK_BOUND_SECONDS, exit=True)
        self.addCleanup(faulthandler.cancel_dump_traceback_later)
        self._restore_stop_signal_handlers_on_cleanup()
        for transition in ("begin_attempt", "abort_attempt"):
            signals_raised = 0
            for line in itertools.count(1):
                controller = StopController()
                controller.install_signal_handlers()
                if not _call_with_signal_before_line(getattr(controller, transition), line):
                    break
                signals_raised += 1
                with self.subTest(transition=transition, line=line):
                    self.assertEqual(controller.stop_signal, signal.SIGTERM)
                    self.assertTrue(controller.halted)
            self.assertGreater(signals_raised, 0, transition)


if __name__ == "__main__":
    unittest.main()
