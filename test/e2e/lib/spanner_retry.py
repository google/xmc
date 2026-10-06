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

"""Bounded retries of Cloud Spanner commands that fail with transient errors."""

from __future__ import annotations

import dataclasses
import enum
import logging
import random
import re
import subprocess
import time
from collections.abc import Callable

logger = logging.getLogger("xmc_e2e_runner")

# Characters of a failed command's redacted error output logged for every failed attempt.
ERROR_OUTPUT_TAIL_CHARS = 2048
# Smallest part of the total budget worth a retry; a shorter attempt would most likely time
# out.
MIN_RETRY_ATTEMPT_SECONDS = 30

# Patterns of transient failures, matched against captured command output only.
_TRANSIENT_PATTERNS = (
    # Status names as gcloud prints them, e.g. "ABORTED: ..." or "status": "ABORTED".
    re.compile(r"\b(?:DEADLINE_EXCEEDED|ABORTED|RESOURCE_EXHAUSTED)\b"),
    # The same statuses as gRPC codes printed by Go clients such as the migrations binary.
    re.compile(r"\bcode = (?:DeadlineExceeded|Aborted|ResourceExhausted)\b"),
    # UNAVAILABLE in any spelling, as a whole word so that identifiers such as maxUnavailable
    # never match, and the message of a failed long-running operation, which gcloud prints
    # without a status code.
    re.compile(r"\bunavailable\b|deadline exceeded", re.IGNORECASE),
    # Quota and concurrent schema change messages, which can also lack a status code.
    re.compile(r"quota exceeded|too many concurrent|pending ddl operations", re.IGNORECASE),
    # HTTP 429, 503, and 504, only when introduced by an http, status, or code token, so that
    # numbers in echoed DDL, such as STRING(503), never match.
    re.compile(r"\b(?:http(?:/[\d.]+)?|status|code)\w*\W{0,4}(?:429|503|504)\b", re.IGNORECASE),
)
# Patterns of INTERNAL errors, which can be deterministic and are therefore retried only once.
_INTERNAL_PATTERNS = (
    re.compile(r"\bINTERNAL\b|\bcode = Internal\b"),
    re.compile(r"internal error encountered", re.IGNORECASE),
)
_ALREADY_EXISTS_PATTERN = re.compile(r"already[_ ]?exists", re.IGNORECASE)

# Project IDs and numbers are masked in logged error output because CI logs are public.
_PROJECT_PATH = re.compile(r"projects/[^/\s'\"]+")
_PROJECT_NUMBER = re.compile(r"(project(?:_number)?['\"]?\s*[:/ =]\s*['\"]?)\d+", re.IGNORECASE)


class ErrorClass(enum.Enum):
    """Retry classification of a failed attempt."""

    TRANSIENT = "transient"
    INTERNAL = "internal"
    PERMANENT = "permanent"


@dataclasses.dataclass(frozen=True)
class RetryBudget:
    """Attempt, time, and backoff limits of a retried command.

    Attributes:
        max_attempts: Total attempts, including the first one.
        base_delay_seconds: Wait after the first failed attempt; doubles after every attempt.
        attempt_timeout_seconds: Upper bound for a single attempt.
        total_seconds: Upper bound for all attempts and waits together.
        jitter_fraction: Largest relative deviation randomly applied to every wait.
    """

    max_attempts: int
    base_delay_seconds: float
    attempt_timeout_seconds: float
    total_seconds: float
    jitter_fraction: float = 0.0

    def delay_seconds(self, attempt: int) -> float:
        """Returns the jittered wait after the failed 1-based ``attempt``."""
        jitter = random.uniform(-self.jitter_fraction, self.jitter_fraction)
        return self.base_delay_seconds * 2 ** (attempt - 1) * (1 + jitter)


def classify_error(exc: Exception) -> ErrorClass:
    """Classifies the failure of one attempt for retrying.

    Only the captured output of a failed command is examined, never its command line, whose
    generated database names and paths could contain a marker.

    Args:
        exc: The failure raised by one attempt.

    Returns:
        TRANSIENT for timeouts and transient statuses, INTERNAL for INTERNAL errors, and
        PERMANENT for everything else, including commands that could not be started.
    """
    if isinstance(exc, subprocess.TimeoutExpired):
        return ErrorClass.TRANSIENT
    if not isinstance(exc, subprocess.CalledProcessError):
        return ErrorClass.PERMANENT
    output = _captured_output(exc)
    if any(pattern.search(output) for pattern in _TRANSIENT_PATTERNS):
        return ErrorClass.TRANSIENT
    if any(pattern.search(output) for pattern in _INTERNAL_PATTERNS):
        return ErrorClass.INTERNAL
    return ErrorClass.PERMANENT


def is_already_exists_error(exc: Exception) -> bool:
    """Returns whether a failed command reported ALREADY_EXISTS in its captured output."""
    return isinstance(exc, subprocess.CalledProcessError) and bool(
        _ALREADY_EXISTS_PATTERN.search(_captured_output(exc))
    )


def redact_error_output(text: str) -> str:
    """Masks project IDs and project numbers in error output.

    Args:
        text: Error output of a failed command.

    Returns:
        ``text`` with every ``projects/<id>`` resource path and project number masked.
    """
    text = _PROJECT_PATH.sub("projects/***", text)
    return _PROJECT_NUMBER.sub(r"\1***", text)


def run_with_transient_retry(
    action: Callable[[int, float], object], description: str, budget: RetryBudget
) -> None:
    """Runs ``action`` until it succeeds, retrying transient failures within ``budget``.

    Every failed attempt is logged with the redacted tail of its error output, whether or not
    it is retried. No retry starts unless at least ``MIN_RETRY_ATTEMPT_SECONDS`` of the total
    time budget remain after its wait.

    Args:
        action: Runs one attempt, given its 1-based number and its timeout in seconds, and
            raises on failure.
        description: Operation name used in log messages.
        budget: Attempt, time, and backoff limits.

    Raises:
        Exception: The unchanged failure of the last attempt, when it is not retried.
    """
    deadline = time.monotonic() + budget.total_seconds
    internal_retried = False
    for attempt in range(1, budget.max_attempts + 1):
        try:
            action(attempt, min(budget.attempt_timeout_seconds, deadline - time.monotonic()))
            return
        except Exception as exc:
            error_class = classify_error(exc)
            delay = _next_delay(budget, attempt, error_class, internal_retried, deadline)
            _log_failed_attempt(description, attempt, budget.max_attempts, exc, delay)
            if delay is None:
                raise
            internal_retried = internal_retried or error_class is ErrorClass.INTERNAL
            time.sleep(delay)


def _next_delay(
    budget: RetryBudget,
    attempt: int,
    error_class: ErrorClass,
    internal_retried: bool,
    deadline: float,
) -> float | None:
    """Returns the wait before retrying the failed ``attempt``, or None to give up."""
    if error_class is ErrorClass.PERMANENT or attempt >= budget.max_attempts:
        return None
    if error_class is ErrorClass.INTERNAL and internal_retried:
        return None
    delay = budget.delay_seconds(attempt)
    if deadline - (time.monotonic() + delay) < MIN_RETRY_ATTEMPT_SECONDS:
        return None
    return delay


def _decode(stream: str | bytes | None) -> str:
    """Decodes captured output, which is bytes when a command times out."""
    if isinstance(stream, bytes):
        return stream.decode("utf-8", errors="replace")
    return stream or ""


def _captured_output(exc: subprocess.SubprocessError) -> str:
    """Returns the captured stderr and stdout of a failed command."""
    return f"{_decode(getattr(exc, 'stderr', None))}\n{_decode(getattr(exc, 'output', None))}"


def _error_output(exc: Exception) -> str:
    """Returns the failure's stderr, falling back to its stdout, without the command line."""
    if not isinstance(exc, subprocess.SubprocessError):
        return str(exc)
    stderr = _decode(getattr(exc, "stderr", None)).strip()
    return stderr or _decode(getattr(exc, "output", None)).strip() or "<none>"


def _log_failed_attempt(
    description: str, attempt: int, max_attempts: int, exc: Exception, delay: float | None
) -> None:
    """Logs a failed attempt, its retry decision, and its redacted error output tail."""
    decision = f"retrying in {delay:.0f}s" if delay is not None else "not retrying"
    logger.warning(
        "%s failed with %s (attempt %d/%d, %s). Error output (redacted, last %d chars):\n%s",
        description,
        type(exc).__name__,
        attempt,
        max_attempts,
        decision,
        ERROR_OUTPUT_TAIL_CHARS,
        redact_error_output(_error_output(exc))[-ERROR_OUTPUT_TAIL_CHARS:],
    )
