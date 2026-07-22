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
import json
import logging
import threading
from collections.abc import Callable, Iterable
from typing import Any

from xmc.buffer import BoundedMetricsBuffer, MetricTuple
from xmc.client import Client
from xmc.network import HTTPClient

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
    """

    def __init__(
        self,
        buffer: BoundedMetricsBuffer | None = None,
        client: HTTPClient | Client | Any | None = None,
        flush_fn: Callable[[list[MetricTuple]], None] | None = None,
        flush_interval: float = DEFAULT_FLUSH_INTERVAL,
        batch_size: int = DEFAULT_BATCH_SIZE,
    ) -> None:
        """Initializes MetricsFlusher.

        Args:
            buffer: Optional custom BoundedMetricsBuffer instance.
            client: Optional client implementing a send() or post() method.
            flush_fn: Optional custom callback function accepting a list of MetricTuples.
            flush_interval: Periodic flush interval in seconds (default 5.0).
            batch_size: Metric count threshold triggering early wakeup (default 1000).

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

        self._init_lock = threading.Lock()
        self._condition = threading.Condition()
        self._shutdown_event = threading.Event()
        self._started = False
        self._thread: threading.Thread | None = None
        self._atexit_handler: Callable[[], None] | None = None

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

    def _send_batch(self, items: list[MetricTuple]) -> bool:
        """Sends metric items via custom flush callback or configured HTTP client.

        Args:
            items: List of MetricTuple elements to send.

        Returns:
            True if batch was successfully sent, False otherwise.
        """
        if self._flush_fn is not None:
            try:
                self._flush_fn(items)
                self._consecutive_failures = 0
                return True
            except Exception as e:
                return self._handle_send_failure(e)

        if self._client is None:
            return False

        # Perform JSON serialization and network POST outside of any lock to prevent thread stalls.
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

        try:
            if hasattr(self._client, "send"):
                body = json.dumps(payload)
                self._client.send(body)
            elif hasattr(self._client, "post"):
                self._client.post("", payload)
            self._consecutive_failures = 0
            return True
        except Exception as e:
            return self._handle_send_failure(e)

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
