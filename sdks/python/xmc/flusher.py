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

"""Background flusher thread for XMC metrics collection."""

import atexit
import enum
import json
import logging
import random
import socket
import threading
import urllib.error
from collections.abc import Callable, Iterable
from typing import Any

from xmc.buffer import BoundedMetricsBuffer, MetricTuple
from xmc.client import Client, XMCNetworkError
from xmc.network import HTTPClient, SendResult

logger = logging.getLogger(__name__)

DEFAULT_FLUSH_INTERVAL: float = 5.0
DEFAULT_BATCH_SIZE: int = 1000
DEFAULT_SHUTDOWN_TIMEOUT: float = 13.0
DEFAULT_MAX_CONSECUTIVE_FAILURES: int = 3
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
        max_delay = min(
            self.max_backoff,
            self.initial_backoff * (self.backoff_factor**self.attempt),
        )
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


def _parse_and_log_terminal_diagnostic(
    status_code: int,
    response_body: str | None,
    exc: Exception | None = None,
) -> None:
    """Parses gateway's structured JSON diagnostic body and logs CRITICAL error details."""
    failed_records_logged = False
    if response_body:
        try:
            data = json.loads(response_body) if isinstance(response_body, str) else response_body
            if isinstance(data, dict):
                error_type = data.get("error", "terminal_error")
                failed_records = data.get("failed_records")
                if isinstance(failed_records, list) and failed_records:
                    for record in failed_records:
                        if isinstance(record, dict):
                            idx = record.get("index", "unknown")
                            reason = record.get("reason", "unspecified reason")
                            logger.critical(
                                "Terminal payload error (HTTP %d, error=%s): record at index %s failed due to: %s",
                                status_code,
                                error_type,
                                idx,
                                reason,
                            )
                    failed_records_logged = True
        except Exception:
            pass

    if not failed_records_logged:
        logger.critical(
            "Terminal payload error (HTTP %d): %s. Diagnostic body: %s",
            status_code,
            exc or "Batch discarded",
            response_body,
        )


def _classify_outcome(
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

    if isinstance(result_or_exc, Exception):
        exc = result_or_exc
        status_code = getattr(
            exc,
            "status_code",
            getattr(exc, "code", getattr(exc, "status", None)),
        )
        response_body = getattr(
            exc,
            "response_body",
            getattr(exc, "body", getattr(exc, "content", None)),
        )
        if isinstance(response_body, bytes):
            response_body = response_body.decode("utf-8", errors="replace")

        if status_code in (400, 422):
            return "terminal", status_code, response_body
        if status_code is not None and (status_code == 429 or 500 <= status_code <= 599):
            return "transient", status_code, response_body
        if status_code is not None and 400 <= status_code < 500:
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


class MetricsFlusher:
    """Background daemon thread loop with graceful shutdown for XMC metrics flushing.

    Buffers metric entries in a BoundedMetricsBuffer and flushes them to an HTTP
    endpoint or custom callback asynchronously in a background thread. Thread loop
    is lazily started on the first log call using double-checked locking.

    Attributes:
        buffer: Underlying BoundedMetricsBuffer.
        flush_interval: Periodic flush interval in seconds.
        batch_size: Batch size threshold to trigger an early flusher wakeup.
        is_started: Returns True if the flusher background thread has started.
        is_shutdown: Returns True if the flusher has been shut down.
    """

    def __init__(
        self,
        buffer: BoundedMetricsBuffer | None = None,
        client: HTTPClient | Client | Any | None = None,
        flush_fn: Callable[[list[MetricTuple]], None] | None = None,
        flush_interval: float = DEFAULT_FLUSH_INTERVAL,
        batch_size: int = DEFAULT_BATCH_SIZE,
        initial_backoff: float = DEFAULT_INITIAL_BACKOFF,
        max_backoff: float = DEFAULT_MAX_BACKOFF,
        backoff_factor: float = DEFAULT_BACKOFF_FACTOR,
        max_retries: int = DEFAULT_MAX_RETRIES,
    ) -> None:
        """Initializes MetricsFlusher.

        Args:
            buffer: Optional custom BoundedMetricsBuffer instance.
            client: Optional client implementing a send() or post() method.
            flush_fn: Optional custom callback function accepting a list of MetricTuples.
            flush_interval: Periodic flush interval in seconds (default 5.0).
            batch_size: Metric count threshold triggering early wakeup (default 1000).
            initial_backoff: Initial backoff delay in seconds (default 1.0).
            max_backoff: Maximum backoff delay cap in seconds (default 32.0).
            backoff_factor: Multiplicative factor for exponential backoff (default 2.0).
            max_retries: Maximum consecutive retry attempts per batch (default 5).

        Raises:
            ValueError: If parameters are invalid or client and flush_fn are both specified/omitted.
        """
        if flush_interval <= 0:
            raise ValueError(f"flush_interval must be positive, got {flush_interval}")
        if batch_size <= 0:
            raise ValueError(f"batch_size must be positive, got {batch_size}")
        if client is not None and flush_fn is not None:
            raise ValueError("Cannot specify both 'client' and 'flush_fn'.")
        if client is None and flush_fn is None:
            raise ValueError("Either 'client' or 'flush_fn' must be provided.")
        if client is not None and not (hasattr(client, "send") or hasattr(client, "post")):
            raise ValueError("client must implement either 'send' or 'post' method.")

        self._buffer = buffer if buffer is not None else BoundedMetricsBuffer()
        self._client = client
        self._flush_fn = flush_fn
        self._flush_interval = flush_interval
        self._batch_size = batch_size
        self._consecutive_failures = 0
        self._state_machine = ResilienceStateMachine(
            initial_backoff=initial_backoff,
            max_backoff=max_backoff,
            backoff_factor=backoff_factor,
            max_retries=max_retries,
        )

        self._init_lock = threading.Lock()
        self._condition = threading.Condition()
        self._shutdown_event = threading.Event()
        self._started = False
        self._thread: threading.Thread | None = None
        self._atexit_handler: Callable[[], None] | None = None

    @property
    def state(self) -> ResilienceState:
        """Returns the current resilience state machine state."""
        return self._state_machine.state

    @property
    def state_machine(self) -> ResilienceStateMachine:
        """Returns the underlying ResilienceStateMachine instance."""
        return self._state_machine

    @property
    def buffer(self) -> BoundedMetricsBuffer:
        """Returns the underlying metric buffer."""
        return self._buffer

    @property
    def is_started(self) -> bool:
        """Returns True if the background flusher thread is started."""
        return self._started

    @property
    def is_shutdown(self) -> bool:
        """Returns True if the flusher has been shut down."""
        return self._shutdown_event.is_set()

    def start(self) -> None:
        """Lazily starts the background flusher daemon thread using double-checked locking."""
        if self._started or self._shutdown_event.is_set():
            return

        # Avoid spawning daemon threads if metric logging is never invoked while ensuring
        # thread safety.
        with self._init_lock:
            if not self._started and not self._shutdown_event.is_set():
                self._thread = threading.Thread(
                    target=self._flusher_loop,
                    name="xmc-metrics-flusher",
                    daemon=True,
                )
                self._thread.start()
                self._atexit_handler = lambda: self.shutdown(timeout=DEFAULT_SHUTDOWN_TIMEOUT)
                atexit.register(self._atexit_handler)
                self._started = True
                logger.info(
                    "Started metrics flusher thread (flush_interval=%.1fs, batch_size=%d)",
                    self._flush_interval,
                    self._batch_size,
                )

    def log_metric(
        self,
        name: str,
        value: Any,
        step: Any,
        timestamp: Any | None = None,
    ) -> None:
        """Buffers a metric, lazily starts flusher thread, and notifies on batch threshold.

        Args:
            name: Metric name string.
            value: Metric value (coerced to float).
            step: Step index (coerced to int).
            timestamp: Optional epoch timestamp in seconds (coerced to float).
        """
        if self._shutdown_event.is_set():
            return

        self._buffer.put(name, value, step, timestamp)
        self.start()

        if len(self._buffer) >= self._batch_size:
            with self._condition:
                self._condition.notify()

    def log_metrics(self, metrics: Iterable[tuple[Any, ...]]) -> None:
        """Buffers multiple metrics, lazily starts flusher, and notifies on batch threshold.

        Args:
            metrics: Iterable of metric tuples (name, value, step[, timestamp]).
        """
        if self._shutdown_event.is_set():
            return

        self._buffer.put_many(metrics)
        self.start()

        if len(self._buffer) >= self._batch_size:
            with self._condition:
                self._condition.notify()

    def _flusher_loop(self) -> None:
        """Background loop blocking on condition variable until timeout or notification."""
        while not self._shutdown_event.is_set():
            with self._condition:
                # Wait for batch threshold or shutdown event to drain multi-batch bursts cleanly.
                self._condition.wait_for(
                    lambda: len(self._buffer) >= self._batch_size or self._shutdown_event.is_set(),
                    timeout=self._flush_interval,
                )

            if self._shutdown_event.is_set():
                break

            self.flush()

    def flush(self, max_count: int | None = None) -> int:
        """Flushes a batch of metrics from the buffer without network I/O under lock.

        Args:
            max_count: Optional maximum number of metrics to flush.

        Returns:
            Number of metrics successfully flushed in this batch.
        """
        count = max_count if max_count is not None else self._batch_size
        items = self._buffer.pop_batch(count)
        if not items:
            return 0

        success = self._send_batch(items)
        return len(items) if success else 0

    def flush_all(self) -> int:
        """Flushes all metrics currently remaining in the buffer synchronously in batches.

        Returns:
            Total number of metrics flushed.
        """
        total_flushed = 0
        while True:
            # Flush in batch_size chunks to prevent OOM or HTTP 413 (Payload Too Large) on shutdown.
            items = self._buffer.pop_batch(self._batch_size)
            if not items:
                break
            success = self._send_batch(items)
            if success:
                total_flushed += len(items)
            elif self._consecutive_failures >= DEFAULT_MAX_CONSECUTIVE_FAILURES:
                logger.warning(
                    "Aborting flush_all after %d consecutive send failures",
                    self._consecutive_failures,
                )
                break
        return total_flushed

    def _handle_send_failure(self, exc: Exception) -> bool:
        """Handles batch send failure, suppressing duplicate stack traces on consecutive errors."""
        self._consecutive_failures += 1
        if self._consecutive_failures == 1:
            logger.exception("Error sending metrics batch: %s", exc)
        else:
            logger.warning(
                "Consecutive metric batch send failure (%d): %s",
                self._consecutive_failures,
                exc,
            )
        return False

    def _dispatch_send(self, items: list[MetricTuple]) -> Any:
        """Dispatches batch to custom flush callback or HTTP client."""
        if self._flush_fn is not None:
            return self._flush_fn(items)

        if self._client is None:
            raise ValueError("No client or flush_fn configured.")

        payload = {
            "metrics": [
                {
                    "metric_name": name,
                    "metric_value": val,
                    "step": step,
                    "timestamp": ts,
                }
                for name, val, step, ts in items
            ]
        }

        if hasattr(self._client, "send"):
            body = json.dumps(payload)
            return self._client.send(body)

        if hasattr(self._client, "post"):
            return self._client.post("", payload)

        return None

    def _send_batch(self, items: list[MetricTuple]) -> bool:
        """Sends metric items via custom flush callback or client with resilience retries."""
        while True:
            outcome_res: Any = None
            try:
                outcome_res = self._dispatch_send(items)
                if outcome_res in (None, SendResult.SUCCESS):
                    self._state_machine.record_success()
                    self._consecutive_failures = 0
                    return True
                classification, status_code, body = _classify_outcome(outcome_res)
            except Exception as exc:
                outcome_res = exc
                classification, status_code, body = _classify_outcome(exc)

            if classification == "terminal":
                _parse_and_log_terminal_diagnostic(
                    status_code or 400,
                    body,
                    outcome_res if isinstance(outcome_res, Exception) else None,
                )
                self._state_machine.record_terminal_failure()
                return False

            self._consecutive_failures += 1
            if self._state_machine.attempt >= self._state_machine.max_retries:
                logger.warning(
                    "Max retries (%d) reached for metric batch. Discarding batch.",
                    self._state_machine.max_retries,
                )
                self._state_machine.reset()
                return False

            delay = self._state_machine.record_transient_failure()
            logger.warning(
                "Transient error sending metrics batch (attempt %d/%d, state=%s). Backing off for %.2fs",
                self._state_machine.attempt,
                self._state_machine.max_retries,
                self._state_machine.state.value,
                delay,
            )

            interrupted = self._shutdown_event.wait(timeout=delay)
            if interrupted:
                logger.info("Backoff sleep interrupted by shutdown signal.")
                return False

    def shutdown(self, timeout: float = DEFAULT_SHUTDOWN_TIMEOUT) -> None:
        """Shuts down flusher thread, joining with timeout and flushing remaining metrics.

        Args:
            timeout: Maximum seconds to wait when joining the background thread.
        """
        with self._init_lock:
            if self._shutdown_event.is_set():
                return
            self._shutdown_event.set()

            if self._atexit_handler is not None:
                try:
                    atexit.unregister(self._atexit_handler)
                except Exception:
                    pass
                self._atexit_handler = None

        with self._condition:
            self._condition.notify_all()

        if self._thread is not None and self._thread.is_alive():
            self._thread.join(timeout=timeout)

        remaining_flushed = self.flush_all()
        logger.info(
            "Metrics flusher shutdown complete (remaining_flushed=%d)",
            remaining_flushed,
        )

    def __enter__(self) -> "MetricsFlusher":
        self.start()
        return self

    def __exit__(self, exc_type: Any, exc_val: Any, exc_tb: Any) -> None:
        self.shutdown()
