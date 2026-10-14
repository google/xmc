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

"""Deadline-bounded polling of eventually consistent cluster and API state in E2E tests."""

from __future__ import annotations

import json
import logging
import subprocess
import time
from collections.abc import Callable
from typing import TypeVar

T = TypeVar("T")

logger = logging.getLogger(__name__)


class PollTimeoutError(RuntimeError):
    """Raised when a polled condition is not met before its deadline."""


def poll_until(
    probe: Callable[[], T | None],
    description: str,
    timeout_seconds: float,
    interval_seconds: float = 1.0,
) -> T:
    """Calls probe until it returns a value other than None or the deadline passes.

    Transient kubectl failures (subprocess errors and malformed JSON output) are logged and
    retried; any other exception raised by probe propagates immediately.

    Args:
        probe: Returns the awaited value, or None while the condition is not yet met.
        description: What is awaited, used in log and error messages.
        timeout_seconds: Seconds after which polling gives up.
        interval_seconds: Seconds to sleep between attempts.

    Returns:
        The first value returned by probe that is not None.

    Raises:
        PollTimeoutError: If probe returns None or fails transiently until the deadline.
    """
    deadline = time.monotonic() + timeout_seconds
    while True:
        try:
            result = probe()
            if result is not None:
                return result
        except (subprocess.SubprocessError, json.JSONDecodeError) as exc:
            logger.warning(
                "Transient error while waiting for %s (will retry): %s (stderr: %s)",
                description,
                exc,
                getattr(exc, "stderr", None),
            )
        if time.monotonic() >= deadline:
            raise PollTimeoutError(f"Timed out after {timeout_seconds}s waiting for {description}")
        time.sleep(interval_seconds)
