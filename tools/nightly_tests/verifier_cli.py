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

"""CLI flags, argument validation, XID parsing, and entrypoint helpers for XMC nightly verifiers."""

from __future__ import annotations

import os
from collections.abc import Sequence
from typing import Any

from absl import app, flags

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
flags.DEFINE_string(
    "dashboard_id",
    None,
    "Resource ID slug for the dashboard",
)
flags.DEFINE_string(
    "expected_title_pattern",
    None,
    "Expected title pattern for the experiment or dashboard",
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
    if not raw_xid:
        if required:
            raise ValueError("Experiment XID (--xid) is required.")
        return ""
    xid = str(raw_xid).strip().rstrip("/")
    if not xid:
        if required:
            raise ValueError("Experiment XID (--xid) is required.")
        return ""
    if xid.startswith("/"):
        xid = xid.lstrip("/")
    if xid in ("experiments", "dashboards"):
        xid = ""
    elif xid.startswith("experiments/"):
        xid = xid.split("/")[-1]
    elif xid.startswith("dashboards/"):
        xid = xid.split("dashboards/", 1)[1].strip()
    if not xid:
        if required:
            raise ValueError("Experiment XID (--xid) is required.")
        return ""
    return xid


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


def run_main(main_fn: Any) -> None:
    """Standard CLI entrypoint wrapper executing absl.app.run."""
    app.run(main_fn)
