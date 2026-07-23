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

import threading
import time
from collections import deque
from collections.abc import Iterable
from typing import Any

# Default buffer capacity: 500,000 metrics (~85MB RAM)
DEFAULT_CAPACITY: int = 500_000

# Metric tuple type alias: (name, value, step, timestamp)
MetricTuple = tuple[str, float, int, float]


def _coerce_float(val: Any) -> float:
    """Coerces input value to primitive float, detaching computation graphs."""
    if type(val) is float:
        return val
    if hasattr(val, "item"):
        val = val.item()
    return float(val)


def _coerce_int(val: Any) -> int:
    """Coerces input value to primitive int, detaching computation graphs."""
    if type(val) is int:
        return val
    if hasattr(val, "item"):
        val = val.item()
    return int(val)


class BoundedMetricsBuffer:
    """Thread-safe, bounded memory buffer for metric data.

    Stores metrics as flat Python tuples (name, value, step, timestamp) to minimize
    memory overhead and detach values from PyTorch/JAX computation graphs.
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
            timestamp: Timestamp in seconds (coerced to float). Defaults to current time.
        """
        c_name = str(name)
        c_value = _coerce_float(value)
        c_step = _coerce_int(step)
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
            coerced_items.append(
                (
                    str(name),
                    _coerce_float(value),
                    _coerce_int(step),
                    _coerce_float(ts),
                )
            )

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
            count = min(max_count, len(self._buffer))
            if count >= len(self._buffer):
                items = list(self._buffer)
                self._buffer.clear()
                return items
            return [self._buffer.popleft() for _ in range(count)]

    def clear(self) -> None:
        """Clears all elements from the buffer."""
        with self._lock:
            self._buffer.clear()
