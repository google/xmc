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

"""Unit tests for BoundedMetricsBuffer."""

import gc
import threading
import time
import tracemalloc
import unittest
from typing import Any

from xmc.buffer import DEFAULT_CAPACITY, BoundedMetricsBuffer, MetricTuple


class MockComputationGraphNode:
    """Mock node representing a heavy deep learning computation graph."""

    pass


class MockTensor:
    """Mock tensor object imitating PyTorch / JAX / NumPy scalar tensor."""

    def __init__(self, value: Any, graph_node: Any = None) -> None:
        self._value = value
        self.grad_fn = graph_node

    def item(self) -> Any:
        return self._value


class TestBoundedMetricsBuffer(unittest.TestCase):
    def test_invalid_capacity(self) -> None:
        with self.assertRaises(ValueError):
            BoundedMetricsBuffer(capacity=0)
        with self.assertRaises(ValueError):
            BoundedMetricsBuffer(capacity=-10)

    def test_default_capacity(self) -> None:
        buf = BoundedMetricsBuffer()
        self.assertEqual(buf.capacity, DEFAULT_CAPACITY)
        self.assertTrue(buf.is_empty())
        self.assertEqual(len(buf), 0)

    def test_put_and_pop_all(self) -> None:
        buf = BoundedMetricsBuffer(capacity=10)
        now = time.time()
        buf.put("loss", 0.5, 1, timestamp=now)
        buf.put("accuracy", 0.95, 2, timestamp=now + 1.0)

        self.assertEqual(len(buf), 2)
        self.assertFalse(buf.is_empty())

        items = buf.pop_all()
        self.assertEqual(len(items), 2)
        self.assertEqual(items[0], ("loss", 0.5, 1, float(now)))
        self.assertEqual(items[1], ("accuracy", 0.95, 2, float(now + 1.0)))
        self.assertTrue(buf.is_empty())

    def test_put_many(self) -> None:
        buf = BoundedMetricsBuffer(capacity=10)
        metrics = [
            ("m1", 1.0, 1, 100.0),
            ("m2", 2.0, 2, 101.0),
            ("m3", 3.0, 3, 102.0),
        ]
        buf.put_many(metrics)
        self.assertEqual(len(buf), 3)

        items = buf.get_all()
        self.assertEqual(len(items), 3)
        self.assertEqual(items[0], ("m1", 1.0, 1, 100.0))
        self.assertEqual(items[2], ("m3", 3.0, 3, 102.0))

    def test_put_many_invalid_metric(self) -> None:
        buf = BoundedMetricsBuffer(capacity=10)
        invalid_metrics = [("m1", 1.0)]  # Only 2 elements
        with self.assertRaises(ValueError) as ctx:
            buf.put_many(invalid_metrics)  # type: ignore
        self.assertIn("at least 3 elements", str(ctx.exception))

    def test_pop_batch(self) -> None:
        buf = BoundedMetricsBuffer(capacity=10)
        for i in range(5):
            buf.put(f"metric_{i}", float(i), i, float(100 + i))

        batch = buf.pop_batch(3)
        self.assertEqual(len(batch), 3)
        self.assertEqual(batch[0][0], "metric_0")
        self.assertEqual(batch[2][0], "metric_2")
        self.assertEqual(len(buf), 2)

        remaining = buf.pop_batch(10)
        self.assertEqual(len(remaining), 2)
        self.assertTrue(buf.is_empty())

    def test_dropped_count(self) -> None:
        buf = BoundedMetricsBuffer(capacity=5)
        self.assertEqual(buf.dropped_count, 0)

        # Fill buffer
        for i in range(5):
            buf.put(f"m_{i}", float(i), i)
        self.assertEqual(buf.dropped_count, 0)

        # Put 1 item beyond capacity
        buf.put("m_overflow", 1.0, 5)
        self.assertEqual(buf.dropped_count, 1)

        # Put many (3 items) beyond capacity (buffer currently has 5 items)
        buf.put_many([("m_a", 1.0, 6), ("m_b", 2.0, 7), ("m_c", 3.0, 8)])
        self.assertEqual(buf.dropped_count, 4)

    def test_fifo_eviction(self) -> None:
        """Verifies strict FIFO eviction policy when capacity is exceeded."""
        capacity = 5
        buf = BoundedMetricsBuffer(capacity=capacity)

        # Fill to capacity
        for i in range(5):
            buf.put(f"metric_{i}", float(i), i, float(i))
        self.assertEqual(len(buf), 5)

        # Append beyond capacity
        for i in range(5, 8):
            buf.put(f"metric_{i}", float(i), i, float(i))

        # Size should still be capacity
        self.assertEqual(len(buf), capacity)

        # Oldest elements (metric_0, metric_1, metric_2) should have been evicted
        items = buf.pop_all()
        expected_names = [f"metric_{i}" for i in range(3, 8)]
        actual_names = [item[0] for item in items]
        self.assertEqual(actual_names, expected_names)

    def test_tensor_detachment(self) -> None:
        """Verifies immediate type coercion detaches PyTorch/JAX computation graph references."""
        buf = BoundedMetricsBuffer(capacity=10)
        graph_node = MockComputationGraphNode()

        val_tensor = MockTensor(0.12345, graph_node=graph_node)
        step_tensor = MockTensor(100, graph_node=graph_node)
        ts_tensor = MockTensor(1700000000.5, graph_node=graph_node)

        buf.put("loss", val_tensor, step_tensor, timestamp=ts_tensor)

        items = buf.pop_all()
        self.assertEqual(len(items), 1)
        name, val, step, ts = items[0]

        # Verify type coercion to standard Python primitives
        self.assertIsInstance(name, str)
        self.assertIsInstance(val, float)
        self.assertIsInstance(step, int)
        self.assertIsInstance(ts, float)

        self.assertNotIsInstance(val, MockTensor)
        self.assertNotIsInstance(step, MockTensor)
        self.assertEqual(val, 0.12345)
        self.assertEqual(step, 100)
        self.assertEqual(ts, 1700000000.5)

    def test_thread_safety(self) -> None:
        """Verifies thread safety with concurrent producers and consumers."""
        buf = BoundedMetricsBuffer(capacity=10_000)
        num_producers = 4
        items_per_producer = 2_000
        popped_items: list[MetricTuple] = []
        popped_lock = threading.Lock()
        stop_consumer = threading.Event()

        def producer(thread_id: int) -> None:
            for i in range(items_per_producer):
                buf.put(f"t{thread_id}_m", float(i), i, float(i))

        def consumer() -> None:
            while not stop_consumer.is_set() or not buf.is_empty():
                batch = buf.pop_batch(100)
                if batch:
                    with popped_lock:
                        popped_items.extend(batch)
                else:
                    time.sleep(0.001)

        producers = [threading.Thread(target=producer, args=(i,)) for i in range(num_producers)]
        consumers = [threading.Thread(target=consumer) for _ in range(2)]

        for c in consumers:
            c.start()
        for p in producers:
            p.start()

        for p in producers:
            p.join()

        stop_consumer.set()
        for c in consumers:
            c.join()

        # Pop any remaining items
        with popped_lock:
            popped_items.extend(buf.pop_all())

        total_expected = num_producers * items_per_producer
        self.assertEqual(len(popped_items), total_expected)

    def test_memory_limit_500k(self) -> None:
        """Verifies memory usage for 500,000 metrics remains under 90MB RAM."""
        capacity = 500_000

        # Create unique metric names in advance so string allocation is fixed
        names = [f"metric_{i % 100}" for i in range(100)]

        buf = BoundedMetricsBuffer(capacity=capacity)

        # Pass generator to avoid allocating an extra 500k tuple list in test scope
        def generate_metrics():
            for i in range(capacity):
                yield (names[i % 100], 0.123456789, i, 1700000000.0 + i)

        gc.collect()
        tracemalloc.start()

        buf.put_many(generate_metrics())

        gc.collect()
        current, peak = tracemalloc.get_traced_memory()
        tracemalloc.stop()

        allocated_mb = current / (1024 * 1024)
        peak_mb = peak / (1024 * 1024)

        self.assertEqual(len(buf), capacity)
        msg = (
            f"Buffer memory usage {allocated_mb:.2f}MB (peak {peak_mb:.2f}MB) "
            "exceeded limit of 90MB for 500,000 metrics"
        )
        self.assertLess(allocated_mb, 90.0, msg)


if __name__ == "__main__":
    unittest.main()
