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

"""Unit tests for background flusher thread (sdks/python/xmc/flusher.py)."""

import os
import threading
import time
import unittest
from unittest.mock import MagicMock

from xmc.buffer import MetricTuple
from xmc.flusher import MetricsFlusher
from xmc.network import HTTPClient


class TestMetricsFlusherInitialization(unittest.TestCase):
    """Tests constructor, validation, lazy startup, and thread state."""

    def setUp(self) -> None:
        self._orig_env = os.environ.copy()
        os.environ["XMC_XID"] = "1001"
        os.environ["XMC_WORK_UNIT_ID"] = "2002"
        os.environ["XMC_EXECUTION_ID"] = "3003"

    def tearDown(self) -> None:
        os.environ.clear()
        os.environ.update(self._orig_env)

    def test_invalid_parameters(self) -> None:
        mock_fn = MagicMock()
        mock_client = MagicMock()
        with self.assertRaises(ValueError):
            MetricsFlusher(flush_fn=mock_fn, flush_interval=0)
        with self.assertRaises(ValueError):
            MetricsFlusher(flush_fn=mock_fn, batch_size=-1)
        with self.assertRaises(ValueError):
            MetricsFlusher(client=None, flush_fn=None)
        with self.assertRaises(ValueError):
            MetricsFlusher(client="invalid_client_object")
        with self.assertRaises(ValueError):
            MetricsFlusher(client=mock_client, flush_fn=mock_fn)

    def test_lazy_startup(self) -> None:
        flusher = MetricsFlusher(
            flush_fn=MagicMock(),
            flush_interval=60.0,
            batch_size=100,
        )
        self.assertFalse(flusher.is_started)
        self.assertFalse(flusher.is_shutdown)

        # First metric log call triggers lazy thread startup
        flusher.log_metric("loss", 0.5, 0)
        self.assertTrue(flusher.is_started)
        self.assertIsNotNone(flusher._thread)
        assert flusher._thread is not None
        self.assertTrue(flusher._thread.is_alive())
        self.assertTrue(flusher._thread.daemon)
        self.assertEqual(flusher._thread.name, "xmc-metrics-flusher")

        flusher.shutdown()
        self.assertTrue(flusher.is_shutdown)
        self.assertFalse(flusher._thread.is_alive())

    def test_concurrent_lazy_startup_double_checked_locking(self) -> None:
        flusher = MetricsFlusher(
            flush_fn=MagicMock(),
            flush_interval=60.0,
            batch_size=100,
        )
        threads = []
        for i in range(10):
            t = threading.Thread(
                target=flusher.log_metric,
                args=(f"metric_{i}", float(i), i),
            )
            threads.append(t)

        for t in threads:
            t.start()
        for t in threads:
            t.join()

        self.assertTrue(flusher.is_started)
        flusher.shutdown()


class TestMetricsFlusherFlushing(unittest.TestCase):
    """Tests early wakeup, non-blocking execution, and graceful shutdown flushing."""

    def setUp(self) -> None:
        self._orig_env = os.environ.copy()
        os.environ["XMC_XID"] = "1001"
        os.environ["XMC_WORK_UNIT_ID"] = "2002"
        os.environ["XMC_EXECUTION_ID"] = "3003"

    def tearDown(self) -> None:
        os.environ.clear()
        os.environ.update(self._orig_env)

    def test_early_wakeup_on_batch_size(self) -> None:
        flushed_batches: list[list[MetricTuple]] = []
        flush_event = threading.Event()

        def mock_flush_fn(items: list[MetricTuple]) -> None:
            flushed_batches.append(items)
            flush_event.set()

        # Set long interval (60s) so flush only triggers via early notification on batch_size=3
        flusher = MetricsFlusher(
            flush_fn=mock_flush_fn,
            flush_interval=60.0,
            batch_size=3,
        )

        flusher.log_metric("loss", 0.5, 1)
        flusher.log_metric("loss", 0.4, 2)
        # Buffer length is 2 (< batch_size 3), flush event not set yet
        self.assertFalse(flush_event.is_set())

        # Log 3rd metric, reaching batch_size threshold
        flusher.log_metric("loss", 0.3, 3)

        # Should wake flusher thread early and flush batch
        success = flush_event.wait(timeout=3.0)
        self.assertTrue(success)
        self.assertGreaterEqual(len(flushed_batches), 1)

        flusher.shutdown()

    def test_non_blocking_training_thread(self) -> None:
        block_event = threading.Event()

        def slow_flush_fn(items: list[MetricTuple]) -> None:
            block_event.wait(timeout=2.0)

        flusher = MetricsFlusher(
            flush_fn=slow_flush_fn,
            flush_interval=60.0,
            batch_size=1,
        )

        start_time = time.monotonic()
        # Enqueue metric which notifies flusher thread
        flusher.log_metric("slow_metric", 1.0, 1)
        elapsed = time.monotonic() - start_time

        # Training thread log_metric call must return immediately (< 0.05s) despite slow flusher
        self.assertLess(elapsed, 0.05)

        block_event.set()
        flusher.shutdown()

    def test_graceful_shutdown_flushes_remaining_metrics(self) -> None:
        flushed_metrics: list[MetricTuple] = []

        def mock_flush_fn(items: list[MetricTuple]) -> None:
            flushed_metrics.extend(items)

        flusher = MetricsFlusher(
            flush_fn=mock_flush_fn,
            flush_interval=60.0,
            batch_size=10,
        )

        flusher.log_metrics(
            [
                ("m1", 1.0, 1, 100.0),
                ("m2", 2.0, 2, 101.0),
                ("m3", 3.0, 3, 102.0),
            ]
        )

        # Batch size is 10, so background thread has not flushed yet
        self.assertEqual(len(flushed_metrics), 0)

        flusher.shutdown(timeout=13.0)

        # Shutdown triggers final synchronous flush of all remaining items
        self.assertEqual(len(flushed_metrics), 3)
        self.assertEqual(flushed_metrics[0][0], "m1")
        self.assertEqual(flushed_metrics[2][0], "m3")
        self.assertTrue(flusher.is_shutdown)

    def test_client_integration_send_payload(self) -> None:
        mock_client = MagicMock(spec=HTTPClient)
        mock_client.xid = "1001"
        mock_client.work_unit_id = "2002"
        mock_client.execution_id = "3003"
        mock_client.replica_id = "1"
        mock_client.is_silenced = False

        flusher = MetricsFlusher(
            client=mock_client,
            flush_interval=60.0,
            batch_size=2,
        )

        flusher.log_metric("accuracy", 0.95, 10, timestamp=12345.0)
        flusher.log_metric("accuracy", 0.96, 11, timestamp=12346.0)

        flusher.shutdown()

        self.assertTrue(mock_client.send_payload.called)
        args, _ = mock_client.send_payload.call_args
        payload = args[0]
        self.assertIsInstance(payload, list)
        self.assertEqual(len(payload), 2)
        self.assertEqual(payload[0]["metric_name"], "accuracy")
        self.assertEqual(payload[0]["metric_value"], 0.95)

    def test_context_manager_support(self) -> None:
        flushed_metrics: list[MetricTuple] = []

        with MetricsFlusher(
            flush_fn=flushed_metrics.extend,
            flush_interval=60.0,
            batch_size=10,
        ) as flusher:
            flusher.log_metric("cm_metric", 42.0, 1)

        self.assertTrue(flusher.is_shutdown)
        self.assertEqual(len(flushed_metrics), 1)

    def test_flush_all_consecutive_failure_limit(self) -> None:
        fail_count = 0

        def failing_flush_fn(items: list[MetricTuple]) -> None:
            nonlocal fail_count
            fail_count += 1
            raise RuntimeError("Network down")

        flusher = MetricsFlusher(
            flush_fn=failing_flush_fn,
            flush_interval=60.0,
            batch_size=1,
            initial_backoff=0.001,
            max_retries=2,
        )

        for i in range(10):
            flusher.buffer.put(f"m_{i}", float(i), i)

        flushed_count = flusher.flush_all()
        # Should abort batch after max retries (3 attempts total)
        self.assertEqual(flushed_count, 0)
        self.assertEqual(fail_count, 3)

    def test_client_metadata_harvesting_and_payload_fields(self) -> None:
        mock_client = MagicMock(spec=HTTPClient)
        mock_client.xid = "1001"
        mock_client.work_unit_id = "2002"
        mock_client.execution_id = "3003"
        mock_client.replica_id = "replica-5"
        mock_client.job_name = "my_job_name"
        mock_client.is_silenced = False

        env = {
            "XMC_XID": "1001",
            "XMC_WORK_UNIT_ID": "2002",
            "XMC_EXECUTION_ID": "3003",
            "XMC_JOB_NAME": "my_job_name",
            "XMC_REPLICA_ID": "replica-5",
        }
        flusher = MetricsFlusher(
            client=mock_client,
            flush_interval=60.0,
            batch_size=1,
            env=env,
        )

        self.assertEqual(flusher.xid, "1001")
        self.assertEqual(flusher.work_unit_id, "2002")
        self.assertEqual(flusher.execution_id, "3003")
        self.assertEqual(flusher.job_name, "my_job_name")
        self.assertEqual(flusher.replica_id, "replica-5")

        flusher.log_metric("loss", 0.123, 5, timestamp=100.0)
        flusher.shutdown()

        self.assertTrue(mock_client.send_payload.called)
        args, _ = mock_client.send_payload.call_args
        payload = args[0]
        self.assertIsInstance(payload, list)
        self.assertEqual(len(payload), 1)
        metric_dict = payload[0]

        # Verify required fields DoD per XMCMetricsBatch schema
        self.assertEqual(metric_dict["xid"], "1001")
        self.assertEqual(metric_dict["work_unit_id"], "2002")
        self.assertEqual(metric_dict["execution_id"], "3003")
        self.assertEqual(metric_dict["job_name"], "my_job_name")
        self.assertEqual(metric_dict["replica_id"], "replica-5")
        self.assertEqual(metric_dict["metric_name"], "loss")
        self.assertEqual(metric_dict["metric_value"], 0.123)
        self.assertEqual(metric_dict["step"], 5)
        self.assertEqual(metric_dict["timestamp"], 100000000)  # 100.0 * 1_000_000

    def test_buffer_lightweight_tuples(self) -> None:
        mock_client = MagicMock()
        env = {
            "XMC_XID": "123",
            "XMC_WORK_UNIT_ID": "456",
            "XMC_EXECUTION_ID": "789",
            "XMC_JOB_NAME": "my_job_name",
            "XMC_REPLICA_ID": "rep-0",
        }
        flusher = MetricsFlusher(
            client=mock_client,
            flush_interval=60.0,
            batch_size=10,
            env=env,
        )

        flusher.log_metric("train_loss", 0.42, 1, timestamp=500.0)

        # Buffer must store only 4-element tuples (name, val, step, ts_epoch)
        raw_items = flusher.buffer.get_all()
        self.assertEqual(len(raw_items), 1)
        self.assertEqual(len(raw_items[0]), 4)
        self.assertEqual(raw_items[0], ("train_loss", 0.42, 1, 500))

        flusher.shutdown()

    def test_silencing_support(self) -> None:
        env_silenced = {"RANK": "3", "XMC_LOG_ALL_REPLICAS": "0"}
        mock_client = MagicMock()
        flusher = MetricsFlusher(
            client=mock_client,
            flush_interval=60.0,
            batch_size=1,
            env=env_silenced,
        )
        self.assertTrue(flusher.is_silenced)

        flusher.log_metric("silenced_metric", 1.0, 1)
        self.assertFalse(mock_client.send_payload.called)


if __name__ == "__main__":
    unittest.main()
