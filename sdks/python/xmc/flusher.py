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
import logging
import threading
import time
from collections.abc import Callable, Iterable, Mapping
from typing import Any

from xmc.buffer import BoundedMetricsBuffer, MetricTuple
from xmc.client import Client
from xmc.dist_harvest import (
    harvest_client_metadata,
)
from xmc.network import HTTPClient, SendResult
from xmc.resilience import (
    DEFAULT_BACKOFF_FACTOR,
    DEFAULT_INITIAL_BACKOFF,
    DEFAULT_MAX_BACKOFF,
    DEFAULT_MAX_RETRIES,
    ResilienceState,
    ResilienceStateMachine,
    classify_outcome,
    parse_and_log_terminal_diagnostic,
)
from xmc.types import format_metric_batch_payload

logger = logging.getLogger(__name__)

DEFAULT_FLUSH_INTERVAL: float = 5.0
DEFAULT_BATCH_SIZE: int = 1000
DEFAULT_SHUTDOWN_TIMEOUT: float = 13.0
DEFAULT_MAX_CONSECUTIVE_FAILURES: int = 3


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
        is_silenced: Returns True if telemetry logging is silenced on non-zero rank replicas.
        xid: Workload XID metadata integer.
        work_unit_id: Work unit ID metadata integer.
        execution_id: Execution ID metadata integer.
        replica_id: Replica/rank ID metadata string.
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
        env: Mapping[str, str] | None = None,
        log_all_replicas: bool = False,
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
            env: Optional environment variables dictionary.
            log_all_replicas: Opt-in to bypass rank-zero silencer.

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
        if (
            client is not None
            and not isinstance(client, (Client, HTTPClient))
            and not callable(getattr(client, "send_payload", None))
        ):
            raise ValueError("client must be an instance of Client or implement send_payload.")

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

        if isinstance(client, (Client, HTTPClient)):
            self._xid = client.xid
            self._work_unit_id = client.work_unit_id
            self._execution_id = client.execution_id
            self._replica_id = client.replica_id
            self._is_silenced = client.is_silenced
        else:
            (
                self._xid,
                self._work_unit_id,
                self._execution_id,
                self._replica_id,
                self._is_silenced,
            ) = harvest_client_metadata(
                env=env,
                log_all_replicas=log_all_replicas,
            )

        self._init_lock = threading.Lock()
        self._condition = threading.Condition()
        self._shutdown_event = threading.Event()
        self._started = False
        self._thread: threading.Thread | None = None
        self._atexit_handler: Callable[[], None] | None = None
        self._accumulated_drops: int = 0
        self._last_drop_log_time: float = 0.0
        self._drop_log_lock = threading.Lock()

    @property
    def xid(self) -> str:
        """Returns the workload XID metadata string.

        WHY str return type: Though xid is a 64-bit integer in Spanner/ClickHouse,
        transmitting 64-bit integers as raw JSON numbers causes precision truncation in
        JavaScript IEEE 754 web dashboards (capped at 2^53 - 1). Stringifying integer
        IDs preserves 64-bit precision per Google AIP-127 and XMCMetricsBatch schema.
        """
        return self._xid

    @property
    def work_unit_id(self) -> str:
        """Returns the work unit ID metadata string.

        WHY str return type: Stringified 64-bit integer prevents JavaScript precision loss.
        """
        return self._work_unit_id

    @property
    def execution_id(self) -> str:
        """Returns the execution ID metadata string.

        WHY str return type: Stringified 64-bit integer prevents JavaScript precision loss.
        """
        return self._execution_id

    @property
    def replica_id(self) -> str:
        """Returns the replica/rank ID metadata string.

        WHY str: Identifier for parallel worker rank (e.g., "0", "3"). Defaults to "0".
        """
        return self._replica_id

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

    @property
    def is_silenced(self) -> bool:
        """Returns True if telemetry logging is silenced on non-zero rank replicas."""
        return self._is_silenced

    def start(self) -> None:
        """Lazily starts the background flusher daemon thread using double-checked locking."""
        if self._started or self._shutdown_event.is_set() or self._is_silenced:
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
            timestamp: Optional epoch timestamp (coerced to int per metrics.proto int64).
        """
        if self._shutdown_event.is_set() or self._is_silenced:
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
        if self._shutdown_event.is_set() or self._is_silenced:
            return

        self._buffer.put_many(metrics)
        self.start()

        if len(self._buffer) >= self._batch_size:
            with self._condition:
                self._condition.notify()

    def _check_and_log_dropped_metrics(self) -> None:
        """Periodically reports cumulative dropped metrics at most once every 10 seconds.

        WHY rate-limited (10s): During network partitions or high gateway backpressure,
        the buffer overflows and evicts metrics. Logging on every dropped metric would
        flood stdout/stderr logs and stall the host process. Rate-limiting drop warnings
        to once every 10 seconds preserves host throughput while keeping overflow visible.
        """
        with self._drop_log_lock:
            dropped = self._buffer.pop_dropped_count()
            if dropped > 0:
                self._accumulated_drops += dropped
            now = time.monotonic()
            if self._accumulated_drops > 0 and (
                now - self._last_drop_log_time >= 10.0 or self._shutdown_event.is_set()
            ):
                logger.warning("Dropped %d metrics due to buffer overflow", self._accumulated_drops)
                self._accumulated_drops = 0
                self._last_drop_log_time = now

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
        self._check_and_log_dropped_metrics()
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
        """Dispatches batch to custom flush callback or client."""
        if self._flush_fn is not None:
            return self._flush_fn(items)

        if self._client is None:
            raise ValueError("No client or flush_fn configured.")

        # Client is guaranteed to implement send_payload
        payload = format_metric_batch_payload(
            items,
            xid=self._xid,
            work_unit_id=self._work_unit_id,
            execution_id=self._execution_id,
            replica_id=self._replica_id,
        )
        return self._client.send_payload(payload)

    def _send_batch(self, items: list[MetricTuple]) -> bool:
        """Sends metric items via custom flush callback or client with resilience retries."""
        while True:
            outcome_res: Any = None
            try:
                outcome_res = self._dispatch_send(items)
                if outcome_res in (None, SendResult.SUCCESS) or isinstance(
                    outcome_res, (dict, list)
                ):
                    self._state_machine.record_success()
                    self._consecutive_failures = 0
                    return True
                classification, status_code, body = classify_outcome(outcome_res)
            except Exception as exc:
                outcome_res = exc
                classification, status_code, body = classify_outcome(exc)

            if classification == "terminal":
                parse_and_log_terminal_diagnostic(
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
                "Transient error sending metrics batch (attempt %d/%d, state=%s): %s. Backing off for %.2fs",
                self._state_machine.attempt,
                self._state_machine.max_retries,
                self._state_machine.state.value,
                outcome_res,
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
