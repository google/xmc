#!/usr/bin/env python3
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

"""Test execution script for local ClickHouse schema iteration.

Spins up a local standalone ClickHouse container (out-of-band of CTK),
waits for readiness, and executes basic schema DDL/DML queries.
Reference: Design Doc Section 4.1.1 & 7.2 (b/537681533).
"""

import argparse
import logging
import os
import shutil
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

logging.basicConfig(
    level=logging.INFO,
    format="[%(levelname)s] %(message)s",
)
logger = logging.getLogger("test_clickhouse_schema")


def is_clickhouse_responsive(host: str, port: int) -> bool:
    """Check if ClickHouse HTTP ping endpoint responds with 200 OK."""
    url = f"http://{host}:{port}/ping"
    try:
        req = urllib.request.Request(url, method="GET")
        with urllib.request.urlopen(req, timeout=3) as resp:
            body = resp.read().decode("utf-8").strip()
            return resp.status == 200 and body.startswith("Ok")
    except (urllib.error.URLError, TimeoutError, OSError):
        return False


def ensure_container_running(compose_file: str, host: str, port: int, tcp_port: int) -> None:
    """Ensure ClickHouse container is running either locally or via Docker Compose."""
    if is_clickhouse_responsive(host, port):
        logger.info("ClickHouse is already responding at http://%s:%d/ping.", host, port)
        return

    docker_bin = shutil.which("docker")
    if not docker_bin:
        logger.error("ClickHouse is not running and 'docker' CLI was not found in PATH.")
        sys.exit(1)

    if os.path.exists(compose_file):
        logger.info(
            "Starting local ClickHouse container via Docker Compose using %s...", compose_file
        )
        res = subprocess.run([docker_bin, "compose", "-f", compose_file, "up", "-d"], check=False)
        if res.returncode == 0:
            return

    logger.info("Falling back to 'docker run' for ClickHouse container...")
    subprocess.run(
        [docker_bin, "rm", "-f", "clickhouse-standalone"],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        check=False,
    )
    run_cmd = [
        docker_bin,
        "run",
        "-d",
        "--name",
        "clickhouse-standalone",
        "-p",
        f"{port}:8123",
        "-p",
        f"{tcp_port}:9000",
        "clickhouse/clickhouse-server:24.3",
    ]
    subprocess.run(run_cmd, check=True)


def wait_for_clickhouse(
    host: str, port: int, max_retries: int = 30, retry_interval: float = 1.0
) -> None:
    """Poll ClickHouse ping endpoint until it becomes ready."""
    logger.info("Waiting for ClickHouse readiness at http://%s:%d/ping...", host, port)
    for retry in range(1, max_retries + 1):
        if is_clickhouse_responsive(host, port):
            logger.info("ClickHouse is ready and healthy!")
            return
        logger.debug(
            "Retry %d/%d: ClickHouse not ready yet. Retrying in %s seconds...",
            retry,
            max_retries,
            retry_interval,
        )
        time.sleep(retry_interval)

    logger.error(
        "ClickHouse failed to become ready within %d seconds.", int(max_retries * retry_interval)
    )
    sys.exit(1)


def execute_clickhouse_query(host: str, port: int, query: str) -> str:
    """Execute a SQL query against ClickHouse HTTP interface."""
    url = f"http://{host}:{port}/"
    data = query.encode("utf-8")
    req = urllib.request.Request(url, data=data, method="POST")
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            return resp.read().decode("utf-8").strip()
    except urllib.error.HTTPError as e:
        err_body = e.read().decode("utf-8", errors="replace").strip()
        logger.error("ClickHouse query failed with HTTP %d: %s", e.code, err_body)
        raise


def test_schema_queries(host: str, port: int) -> None:
    """Run ping, version, DDL table creation, DML insert, count query, and cleanup."""
    logger.info("Executing basic ping query...")
    ping_url = f"http://{host}:{port}/ping"
    with urllib.request.urlopen(ping_url, timeout=5) as resp:
        body = resp.read().decode("utf-8").strip()
        if not body.startswith("Ok"):
            logger.error("Unexpected ping response: %s", body)
            sys.exit(1)
        logger.info("Ping query response: %s", body)

    logger.info("Executing version query...")
    version = execute_clickhouse_query(host, port, "SELECT version()")
    logger.info("ClickHouse server version: %s", version)

    logger.info("Testing schema creation and basic DDL/DML operations...")
    create_table_ddl = """
    CREATE TABLE IF NOT EXISTS default.xmc_schema_test (
        id UInt64,
        metric_name String,
        value Float64,
        timestamp DateTime DEFAULT now()
    ) ENGINE = MergeTree() ORDER BY (timestamp, id);
    """
    execute_clickhouse_query(host, port, create_table_ddl)
    logger.info("Table 'default.xmc_schema_test' created successfully.")

    insert_dml = "INSERT INTO default.xmc_schema_test (id, metric_name, value) VALUES (1, 'test_metric_cpu', 85.5);"
    execute_clickhouse_query(host, port, insert_dml)
    logger.info("Inserted test record into 'default.xmc_schema_test'.")

    count_str = execute_clickhouse_query(host, port, "SELECT count() FROM default.xmc_schema_test;")
    count = int(count_str)
    if count < 1:
        logger.error("Expected count >= 1, got %d", count)
        sys.exit(1)
    logger.info("Successfully inserted and queried schema test table (record count: %d).", count)

    drop_table_ddl = "DROP TABLE IF EXISTS default.xmc_schema_test;"
    execute_clickhouse_query(host, port, drop_table_ddl)
    logger.info("Cleaned up temporary test table 'default.xmc_schema_test'.")


def cleanup_container(compose_file: str, host: str, port: int) -> None:
    """Clean up local ClickHouse container if requested."""
    logger.info("Cleaning up local ClickHouse container...")
    docker_bin = shutil.which("docker")
    if not docker_bin:
        return
    if os.path.exists(compose_file):
        subprocess.run([docker_bin, "compose", "-f", compose_file, "down"], check=False)
    subprocess.run(
        [docker_bin, "rm", "-f", "clickhouse-standalone"],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        check=False,
    )
    logger.info("Container cleanup completed.")


def parse_args(args: list[str] | None = None) -> argparse.Namespace:
    """Parse command line arguments."""
    parser = argparse.ArgumentParser(
        description="Test execution script for local ClickHouse schema iteration (b/537681533)."
    )
    parser.add_argument("--host", default="localhost", help="ClickHouse host (default: localhost)")
    parser.add_argument(
        "--port", type=int, default=8123, help="ClickHouse HTTP port (default: 8123)"
    )
    parser.add_argument(
        "--tcp-port", type=int, default=9000, help="ClickHouse TCP port (default: 9000)"
    )
    parser.add_argument(
        "--max-retries", type=int, default=30, help="Maximum readiness retries (default: 30)"
    )
    parser.add_argument(
        "--retry-interval", type=float, default=1.0, help="Retry interval in seconds (default: 1.0)"
    )
    parser.add_argument(
        "--cleanup", action="store_true", help="Teardown local container after testing"
    )
    parser.add_argument(
        "--compose-file",
        default="",
        help="Path to docker compose file (default: deploy/local/clickhouse-standalone.yaml)",
    )
    return parser.parse_args(args)


def main() -> None:
    """Main entry point for ClickHouse schema test script."""
    args = parse_args()
    script_dir = os.path.dirname(os.path.abspath(__file__))
    repo_root = os.path.abspath(os.path.join(script_dir, ".."))
    compose_file = args.compose_file or os.path.join(
        repo_root, "deploy", "local", "clickhouse-standalone.yaml"
    )

    ensure_container_running(compose_file, args.host, args.port, args.tcp_port)
    wait_for_clickhouse(args.host, args.port, args.max_retries, args.retry_interval)
    test_schema_queries(args.host, args.port)

    if args.cleanup:
        cleanup_container(compose_file, args.host, args.port)

    logger.info("All ClickHouse local schema tests passed successfully!")


if __name__ == "__main__":
    main()
