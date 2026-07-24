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

"""Resilience state machine and exponential backoff helpers for XMC Metrics."""

import enum
import json
import logging
import random
import socket
import urllib.error
from typing import Any

from xmc.client import XMCNetworkError
from xmc.network import SendResult

logger = logging.getLogger("xmc.resilience")

DEFAULT_INITIAL_BACKOFF: float = 1.0
DEFAULT_MAX_BACKOFF: float = 32.0
DEFAULT_BACKOFF_FACTOR: float = 2.0
DEFAULT_MAX_RETRIES: int = 5


class ResilienceState(enum.Enum):
    """Resilience states for metrics flusher retry loop."""

    HEALTHY = "HEALTHY"
    BACKOFF = "BACKOFF"


class ResilienceStateMachine:
    """State machine managing exponential backoff, jitter, and retry state."""

    def __init__(
        self,
        initial_backoff: float = DEFAULT_INITIAL_BACKOFF,
        max_backoff: float = DEFAULT_MAX_BACKOFF,
        backoff_factor: float = DEFAULT_BACKOFF_FACTOR,
        max_retries: int = DEFAULT_MAX_RETRIES,
    ) -> None:
        """Initializes ResilienceStateMachine with backoff parameters."""
        self.initial_backoff = initial_backoff
        self.max_backoff = max_backoff
        self.backoff_factor = backoff_factor
        self.max_retries = max_retries
        self.state = ResilienceState.HEALTHY
        self.attempt = 0

    def reset(self) -> None:
        """Resets state machine back to HEALTHY state."""
        self.state = ResilienceState.HEALTHY
        self.attempt = 0

    def compute_backoff_delay(self) -> float:
        """Computes exponential backoff delay with full jitter."""
        try:
            max_delay = min(
                self.max_backoff,
                self.initial_backoff * (self.backoff_factor**self.attempt),
            )
        except OverflowError:
            max_delay = self.max_backoff
        return random.uniform(0, max_delay)

    def record_transient_failure(self) -> float:
        """Transitions state to BACKOFF, calculates delay with full jitter, and increments attempt counter."""
        self.state = ResilienceState.BACKOFF
        delay = self.compute_backoff_delay()
        self.attempt += 1
        return delay

    def record_terminal_failure(self) -> None:
        """Resets state after a terminal failure (batch discarded)."""
        self.reset()

    def record_success(self) -> None:
        """Resets state after successful send."""
        self.reset()


def _log_failed_record(status_code: int, error_type: str, record: Any) -> bool:
    """Logs details for a single failed record if valid dictionary."""
    if not isinstance(record, dict):
        return False
    idx = record.get("index", "unknown")
    reason = record.get("reason", "unspecified reason")
    logger.critical(
        "Terminal payload error (HTTP %d, error=%s): record at index %s failed due to: %s",
        status_code,
        error_type,
        idx,
        reason,
    )
    return True


def _log_failed_records_diagnostic(
    status_code: int,
    response_body: str | dict[str, Any] | None,
) -> bool:
    """Parses response body and logs failed records if present. Returns True if logged."""
    if not response_body:
        return False
    try:
        data = json.loads(response_body) if isinstance(response_body, str) else response_body
    except Exception:
        return False

    if not isinstance(data, dict):
        return False

    failed_records = data.get("failed_records")
    if not isinstance(failed_records, list) or not failed_records:
        return False

    error_type = data.get("error", "terminal_error")
    logged_any = False
    for record in failed_records:
        if _log_failed_record(status_code, error_type, record):
            logged_any = True
    return logged_any


def parse_and_log_terminal_diagnostic(
    status_code: int,
    response_body: str | dict[str, Any] | None,
    exc: Exception | None = None,
) -> None:
    """Parses gateway's structured JSON diagnostic body and logs CRITICAL error details."""
    if _log_failed_records_diagnostic(status_code, response_body):
        return

    logger.critical(
        "Terminal payload error (HTTP %d): %s. Diagnostic body: %s",
        status_code,
        exc or "Batch discarded",
        response_body,
    )


def _extract_response_body(exc: Exception) -> str | None:
    """Extracts and normalizes response body string from an exception."""
    body = getattr(
        exc,
        "response_body",
        getattr(exc, "body", getattr(exc, "content", None)),
    )
    if body is None:
        return None
    if not isinstance(body, (str, bytes)):
        try:
            body = json.dumps(body)
        except Exception:
            body = str(body)
    if isinstance(body, bytes):
        return body.decode("utf-8", errors="replace")
    return body


def classify_outcome(
    result_or_exc: Any,
) -> tuple[str, int | None, str | None]:
    """Classifies an execution outcome as 'transient' or 'terminal'.

    Returns:
        Tuple of (classification, status_code, response_body).
    """
    if result_or_exc == SendResult.INVALID_PAYLOAD:
        return "terminal", 400, None
    if result_or_exc == SendResult.TRANSIENT_FAILURE:
        return "transient", 500, None
    if not isinstance(result_or_exc, Exception):
        return "transient", None, None

    exc = result_or_exc
    if isinstance(exc, (TypeError, KeyError, AttributeError, ValueError)):
        return "terminal", None, str(exc)

    status_code = getattr(
        exc,
        "status_code",
        getattr(exc, "code", getattr(exc, "status", None)),
    )
    response_body = _extract_response_body(exc)

    if status_code is not None:
        if status_code == 429 or 500 <= status_code <= 599:
            return "transient", status_code, response_body
        if 400 <= status_code < 500:
            return "terminal", status_code, response_body

    if isinstance(
        exc,
        (
            XMCNetworkError,
            TimeoutError,
            socket.timeout,
            urllib.error.URLError,
            ConnectionError,
            OSError,
        ),
    ):
        return "transient", status_code, response_body

    return "transient", None, None
