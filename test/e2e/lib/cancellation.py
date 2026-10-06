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

"""Cooperative stopping of harness work on SIGINT, SIGTERM, or the harness deadline."""

from __future__ import annotations

import signal
import threading
import time
from types import FrameType

_STOP_SIGNALS = (signal.SIGINT, signal.SIGTERM)
# How often a wait checks whether the harness halted.
_HALT_POLL_SECONDS = 0.5


class HarnessStoppedError(BaseException):
    """Raised by harness work that is halted by a stop or by an aborted attempt.

    It derives from ``BaseException`` so that phase classification and ``except Exception``
    handlers let it propagate to the attempt.
    """


class StopController:
    """Halts the in-flight work of the harness through shared flags.

    A stop, requested by a signal or by the harness deadline, halts all remaining work of the
    run. An attempt abort, requested when a concurrent task fails, halts the sibling tasks
    until the next attempt begins. Blocking work polls the flags: commands are killed and
    backoff waits end early.

    The flags are plain attributes, never guarded by a lock, because the signal handler runs
    on the main thread between any two bytecodes: a handler that waited for a lock held by
    the interrupted code, such as the lock inside ``threading.Event.set()``, would deadlock.
    No update needs a lock either: the signal handler and the deadline timer only set the
    stop flags, and only the main thread sets and clears the attempt abort.
    """

    def __init__(self) -> None:
        self._signum: int | None = None
        self._stop_requested = False
        self._attempt_aborted = False

    @property
    def stop_signal(self) -> int | None:
        """The first signal that stopped the harness, or None."""
        return self._signum

    @property
    def halted(self) -> bool:
        """Whether a stop or an attempt abort halts work."""
        return self._stop_requested or self._attempt_aborted

    def request_stop(self, signum: int | None = None) -> None:
        """Halts all remaining work of the run; safe to call from a signal handler.

        Args:
            signum: The received signal, or None when the harness deadline expired.
        """
        # The signal is recorded first, so that any thread that observes the stop also
        # observes its signal.
        if signum is not None and self._signum is None:
            self._signum = signum
        self._stop_requested = True

    def abort_attempt(self) -> None:
        """Halts the remaining work of the current attempt."""
        self._attempt_aborted = True

    def begin_attempt(self) -> None:
        """Clears an attempt abort; a requested stop stays in effect."""
        self._attempt_aborted = False

    def raise_if_halted(self) -> None:
        """Raises ``HarnessStoppedError`` if work is halted."""
        if self.halted:
            raise HarnessStoppedError()

    def wait(self, seconds: float) -> None:
        """Waits for ``seconds``, raising ``HarnessStoppedError`` within 0.5 s of a halt."""
        deadline = time.monotonic() + seconds
        while True:
            self.raise_if_halted()
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                return
            time.sleep(min(_HALT_POLL_SECONDS, remaining))

    def install_signal_handlers(self) -> None:
        """Turns the first SIGINT or SIGTERM into a stop request."""
        for signum in _STOP_SIGNALS:
            signal.signal(signum, self._handle_signal)

    def start_deadline(self, seconds: float) -> threading.Timer:
        """Starts a daemon timer that requests a stop after ``seconds``; cancel it when done."""
        timer = threading.Timer(seconds, self.request_stop)
        timer.daemon = True
        timer.start()
        return timer

    def _handle_signal(self, signum: int, _frame: FrameType | None) -> None:
        """Ignores further stop signals, so that teardown is not interrupted, and stops.

        The handler only sets flags: it neither logs nor takes a lock, so that it cannot
        deadlock with the code that it interrupts. Polling work acts on the stop, and the
        orchestrator reports the signal before teardown.
        """
        for stop_signal in _STOP_SIGNALS:
            signal.signal(stop_signal, signal.SIG_IGN)
        self.request_stop(signum)


# The process-wide controller. Callers look it up at call time, as
# ``cancellation.CONTROLLER``, so that tests can replace it.
CONTROLLER = StopController()
