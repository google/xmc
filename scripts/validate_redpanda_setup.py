#!/usr/bin/env python3
# Copyright 2025 Google LLC
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

"""Helper script to manage local standalone Redpanda container and topics for XMC testing."""

import argparse
import re
import shutil
import subprocess
import sys
import time
from pathlib import Path

CONTAINER_NAME = "redpanda-standalone"
DEFAULT_TOPIC = "xmc-metrics"
MAX_WAIT_SECONDS = 30


def get_compose_file_path() -> Path:
    """Returns the path to the redpanda-standalone.yaml compose file."""
    script_dir = Path(__file__).resolve().parent
    return script_dir.parent / "deploy" / "local" / "redpanda-standalone.yaml"


def get_compose_command() -> list[str]:
    """Determines the appropriate docker compose command to use."""
    if shutil.which("docker-compose"):
        return ["docker-compose"]
    if shutil.which("docker"):
        return ["docker", "compose"]
    raise RuntimeError("Neither 'docker-compose' nor 'docker' CLI is available.")


def cleanup_container() -> None:
    """Stops and removes the local Redpanda container and volumes."""
    print("Stopping and removing local Redpanda standalone container...")
    compose_file = get_compose_file_path()
    try:
        cmd = get_compose_command() + [
            "-f",
            str(compose_file),
            "down",
            "-v",
            "--remove-orphans",
        ]
        subprocess.run(cmd, check=True)
    except Exception:
        subprocess.run(["docker", "stop", CONTAINER_NAME], check=False)
        subprocess.run(["docker", "rm", "-v", CONTAINER_NAME], check=False)
    print("Cleanup completed successfully.")


def start_container() -> None:
    """Spins up the Redpanda container via docker compose."""
    print("=== Starting Local Standalone Redpanda Container ===")
    compose_file = get_compose_file_path()
    cmd = get_compose_command() + ["-f", str(compose_file), "up", "-d"]
    subprocess.run(cmd, check=True)


def wait_for_cluster_health(max_wait_seconds: int = MAX_WAIT_SECONDS) -> None:
    """Polls Redpanda cluster health until healthy or timeout."""
    print("Waiting for Redpanda cluster to become healthy...")
    start_time = time.monotonic()
    while time.monotonic() - start_time < max_wait_seconds:
        res = subprocess.run(
            ["docker", "exec", CONTAINER_NAME, "rpk", "cluster", "health"],
            capture_output=True,
            text=True,
            check=False,
        )
        if res.returncode == 0 and re.search(r"Healthy:\s+true", res.stdout, re.IGNORECASE):
            print("Redpanda cluster is healthy!")
            return
        time.sleep(1)

    print(
        f"Error: Redpanda cluster failed to become healthy within {max_wait_seconds} seconds.",
        file=sys.stderr,
    )
    subprocess.run(["docker", "logs", CONTAINER_NAME, "--tail", "50"], check=False)
    sys.exit(1)


def setup_topic(topic_name: str = DEFAULT_TOPIC) -> None:
    """Creates the target topic if it does not already exist."""
    print(f"Creating topic '{topic_name}'...")
    list_res = subprocess.run(
        ["docker", "exec", CONTAINER_NAME, "rpk", "topic", "list"],
        capture_output=True,
        text=True,
        check=False,
    )
    existing_topics = [
        line.split()[0]
        for line in list_res.stdout.strip().splitlines()
        if line.split() and line.split()[0] != "NAME"
    ]
    if topic_name in existing_topics:
        print(f"Topic '{topic_name}' already exists.")
        return

    subprocess.run(
        [
            "docker",
            "exec",
            CONTAINER_NAME,
            "rpk",
            "topic",
            "create",
            topic_name,
            "--partitions",
            "1",
            "--replicas",
            "1",
        ],
        check=True,
    )
    print(f"Topic '{topic_name}' created successfully.")


def verify_topic(topic_name: str = DEFAULT_TOPIC) -> None:
    """Describes the topic topology to confirm existence and configuration."""
    print("=== Verifying Topic Topology ===")
    subprocess.run(
        ["docker", "exec", CONTAINER_NAME, "rpk", "topic", "describe", topic_name],
        check=True,
    )
    print("=== Redpanda Setup Verification Complete ===")


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Manage local standalone Redpanda container for XMC metrics."
    )
    parser.add_argument(
        "--cleanup",
        "--down",
        action="store_true",
        help="Stop and remove local Redpanda container and volumes.",
    )
    parser.add_argument(
        "--topic",
        default=DEFAULT_TOPIC,
        help=f"Topic name to create (default: {DEFAULT_TOPIC}).",
    )
    args = parser.parse_args()

    if args.cleanup:
        cleanup_container()
        return

    start_container()
    wait_for_cluster_health()
    setup_topic(args.topic)
    verify_topic(args.topic)


if __name__ == "__main__":
    main()
