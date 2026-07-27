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

"""Thread-safe, memory-bounded buffer for XMC metrics."""

import math
import threading
import time
from collections import deque
from collections.abc import Iterable
from typing import Any

from xmc.types import is_valid_metric_name

# Default buffer capacity: 500,000 metrics (~85MB RAM)
DEFAULT_CAPACITY: int = 500_000

# Metric tuple type alias: (name, value, step, timestamp_epoch)
# WHY float timestamp: CPython time.time() returns a raw float epoch timestamp.
# Storing floats directly in the buffer avoids CPU-intensive microsecond math
# and string formatting on the training hot path. Conversion to int64 microseconds
# is deferred to the background flusher thread during serialization.
MetricTuple = tuple[str, float, int, float]


def _coerce_float(val: Any) -> float:
    """Coerces input value to primitive float, detaching computation graphs.

    WHY: ML framework tensor objects (PyTorch, JAX, TensorFlow) retain references
    to stateful C++ computation graphs. Calling .item() immediately extracts raw
    scalars and detaches them from the graph, preventing host OOM memory leaks.
    """
    if type(val) is float:
        return val
    if hasattr(val, "item"):
        val = val.item()
    return float(val)


def _coerce_int(val: Any) -> int:
    """Coerces input value to primitive int, detaching computation graphs.

    WHY: Tensor step values retain computation graph nodes in framework training loops.
    Extracting scalar integers via .item() guarantees clean graph detachment.
    """
    if type(val) is int:
        return val
    if hasattr(val, "item"):
        val = val.item()
    return int(val)


def _validate_and_coerce_inputs(name: Any, value: Any, step: Any) -> tuple[str, float, int]:
    """Pre-validates metric_name, value, and step with LRU-cached fast path."""
    if not isinstance(name, str) or not name or len(name) > 255:
        raise ValueError(
            f"Invalid metric_name {name!r}: must be non-empty string <= 255 characters."
        )

    if not is_valid_metric_name(name):
        raise ValueError(
            f"Invalid metric_name {name!r}: must match regex ^[a-zA-Z0-9_\\.\\-\\/]{{1,255}}$"
        )

    c_val = _coerce_float(value)
    if not math.isfinite(c_val):
        raise ValueError(f"Metric value for '{name}' must be finite (got {c_val}).")

    c_step = _coerce_int(step)
    if c_step < 0:
        raise ValueError(f"Step for '{name}' must be a non-negative integer (got {c_step}).")

    return name, c_val, c_step


class BoundedMetricsBuffer:
    """Thread-safe, bounded memory buffer for metric data.

    Note:
        Stores metrics as primitive tuples (name, value, step, timestamp) rather
        than xmc.types.Metric proto instances to minimize memory overhead (~80B vs
        ~400B per item) and eliminate Proto/regex validation latency on caller
        training hot paths. Proto and payload construction is deferred to the
        background flusher thread.

    Uses strict FIFO eviction when full.
    """

    def __init__(self, capacity: int = DEFAULT_CAPACITY) -> None:
        if capacity <= 0:
            raise ValueError(f"Capacity must be positive, got {capacity}")
        self._capacity = capacity
        self._lock = threading.Lock()
        self._buffer: deque[MetricTuple] = deque(maxlen=capacity)
        self._dropped_metrics: int = 0

    @property
    def capacity(self) -> int:
        """Returns the maximum capacity of the buffer."""
        return self._capacity

    @property
    def dropped_count(self) -> int:
        """Returns total number of metrics dropped due to buffer overflow."""
        with self._lock:
            return self._dropped_metrics

    def pop_dropped_count(self) -> int:
        """Returns total number of metrics dropped since last call and resets counter to 0."""
        with self._lock:
            dropped = self._dropped_metrics
            self._dropped_metrics = 0
            return dropped

    def __len__(self) -> int:
        """Returns the current number of metrics in the buffer."""
        with self._lock:
            return len(self._buffer)

    def is_empty(self) -> bool:
        """Returns True if the buffer is empty."""
        with self._lock:
            return len(self._buffer) == 0

    def put(
        self,
        name: str,
        value: Any,
        step: Any,
        timestamp: Any | None = None,
    ) -> None:
        """Appends a single metric to the buffer with immediate type coercion.

        Args:
            name: Metric name.
            value: Metric value (coerced to float).
            step: Step number (coerced to int).
            timestamp: Timestamp in seconds (coerced to float/int). Defaults to current time.
        """
        c_name, c_value, c_step = _validate_and_coerce_inputs(name, value, step)
        c_timestamp = time.time() if timestamp is None else _coerce_float(timestamp)

        item: MetricTuple = (c_name, c_value, c_step, c_timestamp)

        with self._lock:
            if len(self._buffer) == self._capacity:
                self._dropped_metrics += 1
            self._buffer.append(item)

    def put_many(
        self,
        metrics: Iterable[tuple[Any, ...]],
    ) -> None:
        """Appends multiple metrics to the buffer with immediate type coercion.

        Each element in metrics should be a tuple of (name, value, step[, timestamp]).
        """
        coerced_items = []
        now = time.time()
        for metric in metrics:
            try:
                name, value, step = metric[0], metric[1], metric[2]
                ts = metric[3] if len(metric) > 3 and metric[3] is not None else now
            except (IndexError, TypeError) as e:
                raise ValueError(
                    "Each metric must be a sequence of at least 3 elements "
                    f"(name, value, step[, timestamp]). Got: {metric}"
                ) from e
            c_name, c_val, c_step = _validate_and_coerce_inputs(name, value, step)
            coerced_items.append((c_name, c_val, c_step, _coerce_float(ts)))

        if not coerced_items:
            return

        with self._lock:
            overflow = (len(self._buffer) + len(coerced_items)) - self._capacity
            if overflow > 0:
                self._dropped_metrics += overflow
            self._buffer.extend(coerced_items)

    def get_all(self) -> list[MetricTuple]:
        """Returns a snapshot list of all metrics currently in the buffer without removing them."""
        with self._lock:
            return list(self._buffer)

    def pop_all(self) -> list[MetricTuple]:
        """Removes and returns all metrics currently in the buffer."""
        with self._lock:
            items = list(self._buffer)
            self._buffer.clear()
            return items

    def pop_batch(self, max_count: int) -> list[MetricTuple]:
        """Removes and returns up to max_count metrics from the head of the buffer."""
        if max_count <= 0:
            return []
        with self._lock:
            if max_count >= len(self._buffer):
                items = list(self._buffer)
                self._buffer.clear()
                return items
            return [self._buffer.popleft() for _ in range(max_count)]

    def clear(self) -> None:
        """Clears all elements from the buffer."""
        with self._lock:
            self._buffer.clear()
