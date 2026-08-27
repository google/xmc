#!/usr/bin/env python3
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

"""ClickHouse metrics worker script for XMC WorkUnits.

Connects to a ClickHouse database using parameters specified via environment
variables, generates time-series metrics matching the DataEngine testing schema,
and writes them in batch under the active Experiment ID and WorkUnit ID.
"""

from __future__ import annotations

import datetime
import json
import math
import os
import random
import sys
import urllib.error
import urllib.parse
import urllib.request
from typing import Any


def _get_env_bool(key: str, default: bool = False) -> bool:
    """Parses a boolean value from environment variables."""
    val = os.environ.get(key, "").strip().lower()
    if not val:
        return default
    return val in {"1", "true", "yes", "t", "y"}


def generate_metric_records(
    xid: int,
    work_unit_id: int,
    execution_id: int = 1,
    job_name: str = "worker",
    replica_id: str = "0",
    total_steps: int = 100,
) -> list[dict[str, Any]]:
    """Generates synthetic time-series metrics matching DataEngine test datasets.

    Args:
        xid: Experiment ID.
        work_unit_id: WorkUnit ID.
        execution_id: WorkUnit Execution ID.
        job_name: Job name identifier.
        replica_id: Replica index string.
        total_steps: Total number of training steps to simulate.

    Returns:
        A list of row dicts matching the xmc_metrics MergeTree schema.
    """
    records: list[dict[str, Any]] = []
    base_time = datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(
        seconds=total_steps * 10
    )

    random.seed(42 + xid + work_unit_id)

    for step in range(total_steps):
        step_time = base_time + datetime.timedelta(seconds=step * 10)
        timestamp_str = step_time.strftime("%Y-%m-%d %H:%M:%S.%f")

        # 1. train/loss: exponential decay from 2.0 to ~0.15
        train_loss = 2.0 * math.exp(-0.03 * step) + random.uniform(-0.02, 0.02)
        train_loss = max(0.01, train_loss)

        # 2. train/accuracy: logarithmic increase from 0.30 to ~0.96
        train_acc = 0.30 + 0.65 * (1.0 - math.exp(-0.04 * step)) + random.uniform(-0.01, 0.01)
        train_acc = min(0.999, max(0.0, train_acc))

        # 3. eval/loss: validation loss slightly higher than training loss
        eval_loss = 2.2 * math.exp(-0.025 * step) + random.uniform(-0.04, 0.04)
        eval_loss = max(0.02, eval_loss)

        # 4. perf/step_time_ms: step execution duration around 120ms with noise
        step_time_ms = 120.0 + random.gauss(0.0, 5.0)

        step_metrics = [
            ("train/loss", round(train_loss, 4)),
            ("train/accuracy", round(train_acc, 4)),
            ("eval/loss", round(eval_loss, 4)),
            ("perf/step_time_ms", round(step_time_ms, 2)),
        ]

        for metric_name, metric_value in step_metrics:
            records.append(
                {
                    "xid": xid,
                    "work_unit_id": work_unit_id,
                    "execution_id": execution_id,
                    "job_name": job_name,
                    "replica_id": replica_id,
                    "metric_name": metric_name,
                    "metric_value": metric_value,
                    "step": step,
                    "timestamp": timestamp_str,
                }
            )

    return records


def insert_records_via_clickhouse_connect(
    host: str,
    port: int,
    username: str,
    password: str,
    database: str,
    secure: bool,
    table_name: str,
    records: list[dict[str, Any]],
) -> None:
    """Inserts metric records using the clickhouse-connect package."""
    import clickhouse_connect  # pylint: disable=import-outside-toplevel

    client = clickhouse_connect.get_client(
        host=host,
        port=port,
        username=username,
        password=password,
        database=database,
        secure=secure,
    )

    column_names = [
        "xid",
        "work_unit_id",
        "execution_id",
        "job_name",
        "replica_id",
        "metric_name",
        "metric_value",
        "step",
        "timestamp",
    ]
    rows = [[r[col] for col in column_names] for r in records]

    client.insert(
        table=table_name,
        data=rows,
        column_names=column_names,
    )


def insert_records_via_http(
    host: str,
    port: int,
    username: str,
    password: str,
    database: str,
    secure: bool,
    table_name: str,
    records: list[dict[str, Any]],
) -> None:
    """Inserts metric records directly via ClickHouse HTTP interface (JSONEachRow)."""
    scheme = "https" if secure else "http"
    query = f"INSERT INTO {database}.{table_name} FORMAT JSONEachRow"
    params = {"query": query}
    if username:
        params["user"] = username
    if password:
        params["password"] = password

    url = f"{scheme}://{host}:{port}/?{urllib.parse.urlencode(params)}"
    payload = "\n".join(json.dumps(r) for r in records).encode("utf-8")

    req = urllib.request.Request(
        url,
        data=payload,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    # urlopen raises on any non-2xx status, so the failure has to be caught rather than inspected on
    # the response. The body is what carries the reason ClickHouse rejected the insert -- an unknown
    # table or a column type mismatch -- and HTTPError alone reports only the status code.
    try:
        with urllib.request.urlopen(req, timeout=30):
            pass
    except urllib.error.HTTPError as err:
        body = err.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"ClickHouse HTTP insert failed ({err.code}): {body}") from err


def _load_env_file_if_present() -> None:
    """Loads environment variables from worker.env or .env if present."""
    script_dir = os.path.dirname(os.path.abspath(__file__))
    for candidate in ["worker.env", ".env"]:
        path = os.path.join(script_dir, candidate)
        if os.path.isfile(path):
            try:
                with open(path, encoding="utf-8") as f:
                    for line in f:
                        line = line.strip()
                        if not line or line.startswith("#") or "=" not in line:
                            continue
                        k, v = line.split("=", 1)
                        k = k.strip()
                        v = v.strip().strip("'\"")
                        if k not in os.environ:
                            os.environ[k] = v
            except OSError:
                pass


def main() -> None:
    """Executes ClickHouse metrics data generation and ingestion."""
    _load_env_file_if_present()
    print("=== ClickHouse Metrics Worker Started ===")

    # Connection environment variables
    host = os.environ.get("CLICKHOUSE_HOST", "clickhouse.xmc-metrics.svc.cluster.local")
    port = int(os.environ.get("CLICKHOUSE_PORT", "8123"))
    username = os.environ.get("CLICKHOUSE_USER", "default")
    password = os.environ.get("CLICKHOUSE_PASSWORD", "")
    database = os.environ.get("CLICKHOUSE_DATABASE", "default")
    secure = _get_env_bool("CLICKHOUSE_SECURE", default=False)
    table_name = os.environ.get("CLICKHOUSE_TABLE", "xmc_metrics")

    # WorkUnit runtime environment variables
    xid_str = os.environ.get("XMC_EXPERIMENT_ID") or os.environ.get("XMC_XID", "9001")
    wid_str = os.environ.get("XMC_WORK_UNIT_ID", "1")
    exec_id_str = os.environ.get("XMC_EXECUTION_ID", "1")
    job_name = os.environ.get("XMC_JOB_NAME", "worker")
    replica_id = os.environ.get("XMC_REPLICA_ID", "0")
    total_steps = int(os.environ.get("NUM_STEPS", "100"))

    try:
        xid = int(xid_str)
        work_unit_id = int(wid_str)
        execution_id = int(exec_id_str)
    except ValueError as e:
        sys.stderr.write(f"Error parsing runtime IDs: {e}\n")
        sys.exit(1)

    print(f"Target ClickHouse Host : {host}:{port} (db: {database}, secure: {secure})")
    print(f"Target Table           : {table_name}")
    print(f"Experiment ID (xid)    : {xid}")
    print(f"WorkUnit ID (wid)      : {work_unit_id}")
    print(f"Execution ID           : {execution_id}")
    print(f"Simulating Steps       : {total_steps}")

    print("--> Generating synthetic metrics records...")
    records = generate_metric_records(
        xid=xid,
        work_unit_id=work_unit_id,
        execution_id=execution_id,
        job_name=job_name,
        replica_id=replica_id,
        total_steps=total_steps,
    )
    print(f"--> Generated {len(records)} metric rows.")

    print("--> Ingesting records into ClickHouse...")
    try:
        try:
            insert_records_via_clickhouse_connect(
                host=host,
                port=port,
                username=username,
                password=password,
                database=database,
                secure=secure,
                table_name=table_name,
                records=records,
            )
            print("--> Ingested batch via clickhouse-connect successfully.")
        except ImportError:
            print("--> clickhouse-connect not found, falling back to HTTP interface...")
            insert_records_via_http(
                host=host,
                port=port,
                username=username,
                password=password,
                database=database,
                secure=secure,
                table_name=table_name,
                records=records,
            )
            print("--> Ingested batch via ClickHouse HTTP endpoint successfully.")
    except Exception as exc:  # pylint: disable=broad-except
        sys.stderr.write(f"❌ Failed to insert metrics into ClickHouse: {exc}\n")
        sys.exit(1)

    print(f"=== ✅ Worker Completed: Ingested {len(records)} rows for XID {xid} ===")


if __name__ == "__main__":
    main()
