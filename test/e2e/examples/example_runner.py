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

"""Reusable test harness and subprocess execution utilities for XMC example jobs.

Provides gRPC endpoint resolution, environment configuration, subprocess launch
execution, and output parsing for canonical example launch scripts in KinD
E2E tests.
"""

from __future__ import annotations

import contextlib
import dataclasses
import logging
import os
import re
import socket
import subprocess
import sys
from collections.abc import Generator
from pathlib import Path

from test.e2e.lib.config import (
    DEFAULT_ADMIN_PASSWORD,
    DEFAULT_ADMIN_USER,
)
from test.e2e.lib.dev_token import (
    fetch_dex_oidc_token,
)

logger = logging.getLogger(__name__)

ADMIN_USER = DEFAULT_ADMIN_USER
ADMIN_PASSWORD = DEFAULT_ADMIN_PASSWORD


@dataclasses.dataclass(frozen=True)
class ExampleLaunchResult:
    """Encapsulates parsed identifiers and captured output from an example launch."""

    xid: str
    wid: str | None = None
    artifact_name: str | None = None
    raw_output: str = ""

    @property
    def experiment_name(self) -> str:
        """Returns the canonical experiment resource name (e.g. 'experiments/123')."""
        return f"experiments/{self.xid}"

    @property
    def canonical_work_unit_name(self) -> str | None:
        """Returns canonical work unit resource name if wid is present."""
        if self.wid is not None:
            return f"experiments/{self.xid}/workUnits/{self.wid}"
        return None


def is_port_open(host: str, port: int) -> bool:
    """Checks if a TCP port is open and accepting connections."""
    try:
        with socket.create_connection((host, port), timeout=0.5):
            return True
    except (OSError, ConnectionRefusedError):
        return False


@contextlib.contextmanager
def port_forward_grpc_service(
    remote_port: int = 50051,
) -> Generator[str, None, None]:
    """Provides a reachable host:port gRPC endpoint for ESS.

    Reuses existing open host port 50051 (e.g. mapped via KinD NodePort/Gateway)
    without spawning ephemeral port-forward subprocesses.
    """
    env_endpoint = os.getenv("XMANAGER_ENDPOINT")
    if env_endpoint:
        logger.info("Using configured gRPC endpoint: %s", env_endpoint)
        yield env_endpoint
        return

    if is_port_open("127.0.0.1", remote_port):
        logger.info("Using existing open host gRPC port: 127.0.0.1:%d", remote_port)
        yield f"127.0.0.1:{remote_port}"
        return

    yield f"127.0.0.1:{remote_port}"


# TODO: This regex-based stdout matching is brittle and prone to breakage from log format changes.
# It would be better to standardize the output returned by example launch scripts explicitly (e.g.
# via structured JSON stdout, a typed return object, or writing an execution metadata file).
def parse_launch_output(output_str: str) -> ExampleLaunchResult:
    """Parses standard XMC launch stdout to extract XID, WID, and Artifact name."""
    art_match = re.search(
        r"(?:Created (?:[A-Za-z0-9_-]+ )?Artifact successfully:\s*|--> Created (?:[A-Za-z0-9_-]+ )?artifact:\s*)(\S+)",
        output_str,
        re.IGNORECASE,
    )
    artifact_name = art_match.group(1) if art_match else None

    wu_match = re.search(
        r"Created Work\s*Unit successfully:\s*experiments/(\d+)/workUnits/(\d+)",
        output_str,
        re.IGNORECASE,
    )
    if wu_match:
        xid = wu_match.group(1)
        wid = wu_match.group(2)
    else:
        exp_match = re.search(r"experiments/(\d+)", output_str)
        xid = exp_match.group(1) if exp_match else None
        wid = None

    if not xid:
        raise RuntimeError(
            f"Failed to find created experiment/work unit in launch output:\n{output_str}"
        )

    return ExampleLaunchResult(
        xid=xid,
        wid=wid,
        artifact_name=artifact_name,
        raw_output=output_str,
    )


def execute_example_launch(
    launch_script: str | Path,
    args: list[str] | None = None,
    env_overrides: dict[str, str] | None = None,
    user_email: str = ADMIN_USER,
    timeout_sec: float = 60.0,
) -> ExampleLaunchResult:
    """Executes a canonical example launch script via subprocess with local gRPC support.

    Args:
        launch_script: Relative or absolute path to the example launch script
          (e.g. 'examples/chart_artifact_job/launch.py').
        args: Optional CLI arguments to pass to the script.
        env_overrides: Optional environment variables to override.
        user_email: Email used for Dex OIDC authentication.
        timeout_sec: Maximum execution timeout in seconds.

    Returns:
        Parsed ExampleLaunchResult containing XID, WID, and artifact name.
    """
    script_path = Path(launch_script)
    if not script_path.is_absolute():
        workspace_root = Path(__file__).resolve().parents[3]
        script_path = workspace_root / launch_script

    if not script_path.exists():
        raise FileNotFoundError(f"Example launch file not found: {script_path}")

    logger.info("=== Executing Example Launch Script: %s ===", script_path)

    with port_forward_grpc_service() as active_grpc_endpoint:
        proc_env = {
            **os.environ,
            "PROJECT_ID": "xmc-test-project",
            "IMAGE_PATH": "test-image",
            "QUEUE_NAME": "cpu-queue",
            "XMANAGER_ENDPOINT": active_grpc_endpoint,
            "XMANAGER_INSECURE_GRPC": "true",
            "XMC_USER_EMAIL": user_email,
            "XMC_AUTH_TOKEN": os.getenv("XMC_AUTH_TOKEN") or fetch_dex_oidc_token(email=user_email),
            "XMC_DOMAIN": os.getenv("XMC_DOMAIN", "localhost:8080"),
            "IAP_CLIENT_ID": os.getenv("IAP_CLIENT_ID", "test-client-id"),
            **(env_overrides or {}),
        }

        cmd = [sys.executable, str(script_path), *(args or [])]
        try:
            proc = subprocess.run(
                cmd,
                env=proc_env,
                capture_output=True,
                text=True,
                timeout=timeout_sec,
                check=False,
            )
        except subprocess.TimeoutExpired as e:
            captured_out = (e.stdout or "") + (e.stderr or "")
            logger.error(
                "Example launch timed out after %s seconds. Captured output:\n%s",
                timeout_sec,
                captured_out,
            )
            raise RuntimeError(
                f"Example launch timed out after {timeout_sec}s:\n{captured_out}"
            ) from e

    output_str = proc.stdout + proc.stderr
    logger.info("Captured launch output:\n%s", output_str)

    if proc.returncode != 0:
        logger.error("Example launch failed with code %d:\n%s", proc.returncode, output_str)
        raise RuntimeError(f"Example launch exited with code {proc.returncode}:\n{output_str}")

    result = parse_launch_output(output_str)
    logger.info(
        "✓ Launch parsed: Experiment %s, WorkUnit %s, Artifact %s",
        result.experiment_name,
        result.canonical_work_unit_name,
        result.artifact_name,
    )
    return result
