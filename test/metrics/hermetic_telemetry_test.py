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

"""Multi-language distributed Hermetic telemetry flow test.

Verifies:
1. End-to-end telemetry ingestion pipeline (Python SDK -> Go Ingestion Gateway -> Redpanda -> ClickHouse).
2. SDK connection recycling uniform distribution across gateway pods (variance < 10%).
3. SDK connection recycling triggered by TTL (age >= base_ttl ± jitter).
4. Zero Data Loss using argMax(metric_value, created_at) query-time deduplication in ClickHouse.
"""

import json
import os
import random
import subprocess
import sys
import threading
import time
import unittest
import urllib.parse
import urllib.request
from collections.abc import Callable
from typing import Any, ClassVar

from xmc import MetricsFlusher
from xmc.network import HTTPClient


# HTTPClient is Any because the xmc SDK does not resolve from the repo root; unused-ignore
# keeps environments with an installed xmc passing.
class LoadBalancingRecyclingClient(HTTPClient):  # type: ignore[misc, unused-ignore]
    """HTTPClient subclass that rotates gateway endpoints upon connection recycling for load testing.

    Note on Load Balancing Architecture:
        In production Kubernetes deployments, HTTPClient targets a single K8s Service ClusterIP or
        DNS name (e.g. http://ingestion-gateway:8080), and K8s L4/L7 load balancers (kube-proxy / IPVS / Envoy)
        automatically route newly established sockets across gateway pods.

        In this local hermetic test environment (which runs outside Kubernetes), LoadBalancingRecyclingClient
        rotates among local gateway server endpoints on connection re-establishment to simulate K8s L4 load balancer
        pod distribution while exercising the real HTTPClient connection recycling lifecycle (`_should_recycle()`,
        `max_requests`, `base_ttl`, `jitter`, `_connect()`, `_ensure_connection()`).
    """

    def __init__(
        self,
        urls: list[str],
        on_connect: Callable[[str], None] | None = None,
        **kwargs: Any,
    ) -> None:
        self._urls = urls
        self._url_idx = random.randint(0, len(urls) - 1)
        self._on_connect = on_connect
        super().__init__(endpoint=urls[self._url_idx], **kwargs)

    def _connect(self) -> None:
        self._url_idx = (self._url_idx + 1) % len(self._urls)
        endpoint = self._urls[self._url_idx]
        self._parsed_url = urllib.parse.urlparse(endpoint)
        self._host = self._parsed_url.hostname or ""
        self._port = self._parsed_url.port
        self._scheme = self._parsed_url.scheme
        self._base_path = self._parsed_url.path.rstrip("/")
        if self._on_connect:
            self._on_connect(endpoint)
        super()._connect()


def locate_hermetic_runner() -> str:
    """Locates the hermetic_runner compiled Go binary from Bazel environment or workspace."""
    if src_dir := os.environ.get("TEST_SRCDIR"):
        workspace = os.environ.get("TEST_WORKSPACE", "xmanager_cloud")
        path = os.path.join(
            src_dir, workspace, "test", "metrics", "hermetic_runner_", "hermetic_runner"
        )
        if os.path.exists(path):
            return path
    candidates = [
        os.path.join("bazel-bin", "test", "metrics", "hermetic_runner_", "hermetic_runner"),
        os.path.join(
            "..", "..", "bazel-bin", "test", "metrics", "hermetic_runner_", "hermetic_runner"
        ),
        os.path.join("test", "metrics", "hermetic_runner_", "hermetic_runner"),
    ]
    for cand in candidates:
        abs_p = os.path.abspath(cand)
        if os.path.exists(abs_p):
            return abs_p
    raise FileNotFoundError("hermetic_runner binary not found")


def query_clickhouse(ch_base_url: str, query: str, timeout: float = 5.0) -> str:
    """Executes a SQL query against ClickHouse HTTP interface."""
    full_url = f"{ch_base_url}&query={urllib.parse.quote(query)}"
    with urllib.request.urlopen(full_url, timeout=timeout) as resp:
        return resp.read().decode("utf-8").strip()


def poll_clickhouse_counts(
    ch_base_url: str,
    xid: int,
    total_logged: int,
    timeout: float = 45.0,
) -> tuple[int, int]:
    """Polls ClickHouse for deduplicated (argMax) and raw ingested record counts."""
    dedup_query = (
        f"SELECT count() FROM ("
        f"SELECT xid, work_unit_id, execution_id, job_name, replica_id, metric_name, step, "
        f"argMax(metric_value, created_at) AS metric_value "
        f"FROM xmc_metrics WHERE xid = {xid} "
        f"GROUP BY xid, work_unit_id, execution_id, job_name, replica_id, metric_name, step)"
    )
    raw_query = f"SELECT count() FROM xmc_metrics WHERE xid = {xid}"

    deadline = time.monotonic() + timeout
    dedup_count = 0
    raw_count = 0

    while time.monotonic() < deadline:
        try:
            dedup_res = query_clickhouse(ch_base_url, dedup_query)
            raw_res = query_clickhouse(ch_base_url, raw_query)
            dedup_count = int(dedup_res) if dedup_res else 0
            raw_count = int(raw_res) if raw_res else 0
            if dedup_count >= total_logged:
                break
        except Exception as err:
            sys.stderr.write(f"[poll_clickhouse_counts transient warning] {err}\n")
        time.sleep(1.0)

    return dedup_count, raw_count


def dump_clickhouse_diagnostics(ch_base_url: str) -> None:
    """Dumps ClickHouse system diagnostics to stderr upon assertion failure."""
    diag_queries = [
        (
            "system_errors",
            "SELECT name, value, last_error_message FROM system.errors WHERE value > 0 LIMIT 10",
        ),
        (
            "kafka_consumers",
            "SELECT table, consumer_id, last_exception FROM system.kafka_consumers",
        ),
        (
            "text_log",
            "SELECT logger_name, level, message FROM system.text_log WHERE logger_name LIKE '%Kafka%' OR message LIKE '%kafka%' ORDER BY event_time_microseconds DESC LIMIT 15",
        ),
        (
            "queue_count",
            "SELECT count() FROM xmc_metrics_queue SETTINGS stream_like_engine_allow_direct_select=1",
        ),
        ("metrics_count", "SELECT count() FROM xmc_metrics"),
    ]
    for label, q in diag_queries:
        try:
            res_text = query_clickhouse(ch_base_url, q)
            print(f"\n--- ClickHouse Diag [{label}] ---\n{res_text}", file=sys.stderr)
        except Exception as err:
            print(f"\n--- ClickHouse Diag [{label}] error: {err} ---", file=sys.stderr)


def run_simulated_training_workers(
    gateway_urls: list[str],
    num_workers: int,
    steps_per_worker: int,
    xid: int,
    base_work_unit_id: int,
    base_execution_id: int,
    on_connect: Callable[[str], None],
) -> None:
    """Spawns parallel worker threads simulating ML training workloads across WorkUnits and replicas."""
    exceptions: list[Exception] = []

    def worker_task(rank: int) -> None:
        try:
            # Simulate realistic multi-WorkUnit multi-replica distributed training jobs
            worker_wu_id = base_work_unit_id + (rank // 2)
            worker_exec_id = base_execution_id + (rank % 2)
            worker_replica_id = str(rank % 2)
            env = {
                "XMC_XID": str(xid),
                "XMC_WORK_UNIT_ID": str(worker_wu_id),
                "XMC_EXECUTION_ID": str(worker_exec_id),
                "XMC_JOB_NAME": "simulated_train_job",
                "XMC_REPLICA_ID": worker_replica_id,
            }
            client = LoadBalancingRecyclingClient(
                urls=gateway_urls,
                on_connect=on_connect,
                max_requests=1,
                base_ttl=1.0,
                jitter=0.2,
                env=env,
                log_all_replicas=True,
            )
            flusher = MetricsFlusher(
                client=client,
                flush_interval=0.5,
                batch_size=10,
                env=env,
                log_all_replicas=True,
            )
            flusher.start()
            try:
                for step in range(steps_per_worker):
                    flusher.log_metric("train_loss", 0.5 - step * 0.005, step)
                    flusher.log_metric("val_accuracy", 0.8 + step * 0.002, step)
                    flusher.log_metric("gpu_utilization", 95.5, step)
                    flusher.log_metric("throughput_sec", 1200.0 + step, step)
                    flusher.log_metric("epoch", float(step // 10), step)
                    time.sleep(0.005)
            finally:
                flusher.shutdown()
                client.close()
        except Exception as err:
            exceptions.append(err)
            raise

    workers = [threading.Thread(target=worker_task, args=(rank,)) for rank in range(num_workers)]
    for w in workers:
        w.start()
    for w in workers:
        w.join()

    if exceptions:
        raise exceptions[0]


class TestHermeticTelemetryFlow(unittest.TestCase):
    """Multi-language distributed Hermetic telemetry flow test suite."""

    runner_proc: subprocess.Popen | None = None
    config: ClassVar[dict] = {}

    @classmethod
    def setUpClass(cls) -> None:
        runner_bin = locate_hermetic_runner()
        cls.runner_proc = subprocess.Popen(
            [runner_bin, "-num_gateways=5"],
            stdout=subprocess.PIPE,
            stdin=subprocess.PIPE,
            stderr=sys.stderr,
            text=True,
        )
        try:
            assert cls.runner_proc.stdout is not None
            line = cls.runner_proc.stdout.readline()
            if not line:
                raise RuntimeError("hermetic_runner failed to output readiness JSON line")
            cls.config = json.loads(line)
            if cls.config.get("status") != "ready":
                raise RuntimeError(f"hermetic_runner reported non-ready status: {cls.config}")
        except Exception:
            if cls.runner_proc and cls.runner_proc.poll() is None:
                cls.runner_proc.kill()
                cls.runner_proc.wait()
            raise

    @classmethod
    def tearDownClass(cls) -> None:
        if cls.runner_proc and cls.runner_proc.poll() is None:
            if cls.runner_proc.stdin:
                cls.runner_proc.stdin.close()
            cls.runner_proc.terminate()
            try:
                cls.runner_proc.wait(timeout=10.0)
            except subprocess.TimeoutExpired:
                cls.runner_proc.kill()

    def test_hermetic_telemetry_flow_and_connection_recycling_by_count(self) -> None:
        gateway_urls = self.config["gateway_urls"]
        clickhouse_http_addr = self.config["clickhouse_http_addr"]

        num_workers = 10
        steps_per_worker = 50
        metrics_per_step = 5
        xid = 535543832
        work_unit_id = 100
        execution_id = 1

        total_logged = num_workers * steps_per_worker * metrics_per_step

        connection_counts = dict.fromkeys(gateway_urls, 0)
        lock = threading.Lock()

        def record_connection(endpoint: str) -> None:
            with lock:
                connection_counts[endpoint] += 1

        run_simulated_training_workers(
            gateway_urls=gateway_urls,
            num_workers=num_workers,
            steps_per_worker=steps_per_worker,
            xid=xid,
            base_work_unit_id=work_unit_id,
            base_execution_id=execution_id,
            on_connect=record_connection,
        )

        # 1. Assert SDK Connection Recycling Uniform Distribution (variance < 10%)
        counts = list(connection_counts.values())
        mean_count = sum(counts) / len(counts)
        variance = sum((c - mean_count) ** 2 for c in counts) / len(counts)
        std_dev = variance**0.5
        relative_std_dev = (std_dev / mean_count) if mean_count > 0 else 0.0

        self.assertGreater(mean_count, 0, "No connections recorded across gateway pods")
        self.assertLess(
            relative_std_dev,
            0.10,
            f"Connection recycling variance ({relative_std_dev * 100:.2f}%) exceeds 10% threshold. Counts: {counts}",
        )

        # 2. Assert Zero Data Loss: Query ClickHouse using argMax(metric_value, created_at)
        ch_base_url = f"http://{clickhouse_http_addr}/?user=clickhouse&password=clickhouse"
        dedup_count, raw_count = poll_clickhouse_counts(
            ch_base_url, xid, total_logged, timeout=45.0
        )

        if dedup_count < total_logged:
            dump_clickhouse_diagnostics(ch_base_url)

        self.assertEqual(
            dedup_count,
            total_logged,
            f"Zero Data Loss check failed: ClickHouse argMax deduplicated count ({dedup_count}) != logged count ({total_logged})",
        )
        self.assertGreaterEqual(
            raw_count,
            total_logged,
            f"Raw ingested count ({raw_count}) should be >= total logged ({total_logged})",
        )

    def test_connection_recycling_by_ttl(self) -> None:
        """Tests that HTTPClient triggers connection recycling when connection age exceeds base_ttl ± jitter."""
        gateway_urls = self.config["gateway_urls"]
        connection_recycles = 0
        lock = threading.Lock()

        def on_connect(_: str) -> None:
            nonlocal connection_recycles
            with lock:
                connection_recycles += 1

        from xmc.network import SendResult

        client = LoadBalancingRecyclingClient(
            urls=gateway_urls,
            on_connect=on_connect,
            max_requests=10000,  # High request limit so recycling is driven purely by TTL
            base_ttl=1.0,  # 1.0s TTL (matches minimum bound in HTTPClient._calculate_ttl)
            jitter=0.05,
        )

        # Send 4 batches spaced 1.15s apart (exceeding 1.0s TTL on each iteration)
        for i in range(4):
            res = client.send_payload(
                [
                    {
                        "xid": 535543833,
                        "work_unit_id": 1,
                        "execution_id": 1,
                        "replica_id": "0",
                        "metric_name": "ttl_metric",
                        "metric_value": float(i),
                        "step": i,
                        "timestamp": time.time_ns() // 1_000,
                    }
                ]
            )
            self.assertEqual(res, SendResult.SUCCESS, f"HTTP send failed: {res}")
            time.sleep(1.15)

        client.close()

        # Connection should have recycled at least 3 times due to 1.0s TTL expiry across 4 iterations spaced 1.15s apart
        self.assertGreaterEqual(
            connection_recycles,
            3,
            f"Expected at least 3 connection recyclings due to TTL, got {connection_recycles}",
        )


if __name__ == "__main__":
    unittest.main()
