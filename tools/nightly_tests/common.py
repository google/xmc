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

"""Common utilities and verification abstractions for XMC nightly tests.

Provides standard command-line flags, gRPC channel lifecycle managers,
endpoint resolution, argument validation, and formatted logging helpers
used across XMC verification CLI scripts.
"""

from __future__ import annotations

import contextlib
import os
import sys
from collections.abc import Iterator, Mapping, Sequence
from typing import Any

from absl import app, flags

from tools.nightly_tests import xmc_entities_validator

FLAGS = flags.FLAGS

flags.DEFINE_string(
    "xid",
    None,
    "Experiment XID to validate (required for experiment tests)",
)
flags.DEFINE_string("endpoint", None, "ESS gRPC endpoint (host:port)")
flags.DEFINE_string(
    "expected_queue",
    os.environ.get("QUEUE_NAME", "cpu-queue"),
    "Expected ClusterQueue name",
)
flags.DEFINE_integer("timeout_sec", 600, "Timeout in seconds")
flags.DEFINE_string(
    "dashboard_endpoint",
    None,
    "Dashboard Service endpoint (host:port or URL)",
)
flags.DEFINE_integer(
    "expected_work_units",
    None,
    "Expected number of WorkUnits",
)
flags.DEFINE_string(
    "clickhouse_endpoint",
    None,
    "ClickHouse HTTP endpoint (host:port or URL)",
)
flags.DEFINE_bool(
    "require_clickhouse_metrics",
    False,
    "Whether ClickHouse metric arrival assertion is strictly required",
)


def get_flag(flags_obj: Any, name: str, default: Any = None) -> Any:
    """Safely retrieves a flag value from absl FLAGS or a mock SimpleNamespace."""
    return default if (val := getattr(flags_obj, name, None)) is None else val


def validate_argv(argv: Sequence[str]) -> None:
    """Validates command-line arguments to ensure no extra positional arguments exist."""
    if len(argv) > 1:
        raise app.UsageError("Too many command-line arguments.")


def parse_xid(raw_xid: str | None, required: bool = True) -> str:
    """Normalizes and extracts the numeric or slug XID string."""
    if not raw_xid or not str(raw_xid).strip():
        if required:
            raise ValueError("Experiment XID (--xid) is required.")
        return ""
    xid = str(raw_xid).strip()
    if xid.startswith("/"):
        xid = xid.lstrip("/")
    if xid.startswith("experiments/"):
        xid = xid.split("/")[-1]
    elif xid.startswith("dashboards/"):
        xid = xid.split("dashboards/", 1)[1].strip()
    return xid


def resolve_endpoint(provided_endpoint: str | None = None) -> str:
    """Resolves ESS gRPC endpoint from parameter, environment, or default."""
    return provided_endpoint or os.environ.get("XMANAGER_ENDPOINT", "localhost:8080")


def resolve_dashboard_endpoint(provided_endpoint: str | None = None) -> str:
    """Derives HTTP REST base endpoint for dashboard validation."""
    if provided_endpoint and str(provided_endpoint).strip():
        ep = str(provided_endpoint).strip()
        if ep.startswith(("http://", "https://")):
            return ep
        return f"http://{ep}" if ep.startswith(("localhost", "127.0.0.1")) else f"https://{ep}"
    return resolve_ui_base_url()


def resolve_clickhouse_endpoint(provided_endpoint: str | None = None) -> str:
    """Resolves ClickHouse HTTP endpoint from parameter, environment, or default."""
    if provided_endpoint and str(provided_endpoint).strip():
        ep = str(provided_endpoint).strip()
    elif os.environ.get("CLICKHOUSE_ENDPOINT"):
        ep = os.environ["CLICKHOUSE_ENDPOINT"].strip()
    elif os.environ.get("CLICKHOUSE_HOST"):
        host = os.environ["CLICKHOUSE_HOST"].strip()
        port = os.environ.get(
            "CLICKHOUSE_HTTP_PORT", os.environ.get("CLICKHOUSE_PORT", "8123")
        ).strip()
        ep = f"{host}:{port}"
    else:
        ep = "http://127.0.0.1:8123"

    if not ep.startswith(("http://", "https://")):
        ep = f"http://{ep}"
    return ep.rstrip("/")


def resolve_ui_base_url() -> str:
    """Resolves Web UI base URL via xmc_entities_validator."""
    return xmc_entities_validator.resolve_ui_base_url()


def resolve_expected_work_units(default: int = 3) -> int:
    """Resolves expected work units count from flag, NUM_WORK_UNITS env var, or default.

    Args:
        default: Fallback count if neither the flag nor the env var is set.

    Returns:
        The resolved integer number of work units.
    """
    flag_val = get_flag(FLAGS, "expected_work_units", None)
    if flag_val is not None:
        try:
            val = int(flag_val)
            if val > 0:
                return val
        except (ValueError, TypeError):
            pass
    env_val = os.environ.get("NUM_WORK_UNITS")
    if env_val:
        try:
            val = int(env_val)
            if val > 0:
                return val
        except (ValueError, TypeError):
            pass
    return default


@contextlib.contextmanager
def ess_channel_context(endpoint: str | None = None) -> Iterator[tuple[Any, Any]]:
    """Context manager for authenticated ESS gRPC channel and stub lifecycle."""
    resolved_endpoint = resolve_endpoint(endpoint)
    print(f"Connecting to Experiment State Server at: {resolved_endpoint} ...")
    stub, channel = xmc_entities_validator.create_authenticated_stub(resolved_endpoint)
    try:
        yield stub, channel
    finally:
        if channel is not None and hasattr(channel, "close"):
            channel.close()


@contextlib.contextmanager
def handle_verification_errors() -> Iterator[None]:
    """Context manager handling verification assertion errors and unexpected exceptions."""
    try:
        yield
    except (AssertionError, TimeoutError) as e:
        print(f"\n❌ ASSERTION FAILED: {e}", file=sys.stderr)
        sys.exit(1)
    except Exception as e:
        print(f"\n💥 UNEXPECTED ERROR during verification: {e}", file=sys.stderr)
        sys.exit(2)


def print_banner(title: str, details: Mapping[str, Any] | Sequence[tuple[str, Any]]) -> None:
    """Prints a formatted verification header banner."""
    print("=================================================================")
    print(f"       {title.strip()}       ".center(65))
    print("=================================================================")
    items = details.items() if isinstance(details, Mapping) else details
    for label, val in items:
        val_str = str(list(val)) if isinstance(val, (list, tuple)) else str(val)
        print(f"{label:<24}: {val_str}")
    print("-----------------------------------------------------------------")


def print_success_summary(
    title: str,
    details: Mapping[str, Any] | Sequence[tuple[str, Any]],
) -> None:
    """Prints a formatted verification success summary."""
    print("\n=================================================================")
    print(f"🎉 {title.strip()}")
    print("=================================================================")
    items = details.items() if isinstance(details, Mapping) else details
    for label, val in items:
        val_str = str(list(val)) if isinstance(val, (list, tuple)) else str(val)
        print(f"  • {label:<24}: {val_str}")
    print("=================================================================\n")


def run_main(main_fn: Any) -> None:
    """Standard CLI entrypoint wrapper executing absl.app.run."""
    app.run(main_fn)
