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
used across all XMC example verification CLI scripts.
"""

from __future__ import annotations

import contextlib
import os
import sys
from collections.abc import Iterator, Mapping, Sequence
from typing import Any

# Support multiple execution contexts: Bazel sandbox, relative, and standalone
try:
    from tools.nightly_tests import xmc_entities_validator
except ImportError:
    try:
        from . import xmc_entities_validator  # type: ignore
    except ImportError:
        import xmc_entities_validator  # type: ignore

try:
    from absl import app, flags

    FLAGS = flags.FLAGS

    # Register standard flags if not already registered
    if "xid" not in flags.FLAGS:
        flags.DEFINE_string(
            "xid",
            None,
            "Experiment XID to validate (required for experiment tests)",
        )
    if "endpoint" not in flags.FLAGS:
        flags.DEFINE_string("endpoint", None, "ESS gRPC endpoint (host:port)")
    if "expected_queue" not in flags.FLAGS:
        flags.DEFINE_string(
            "expected_queue",
            os.environ.get("QUEUE_NAME", "cpu-queue"),
            "Expected ClusterQueue name",
        )
    if "timeout_sec" not in flags.FLAGS:
        flags.DEFINE_integer("timeout_sec", 600, "Timeout in seconds")
    if "dashboard_endpoint" not in flags.FLAGS:
        flags.DEFINE_string(
            "dashboard_endpoint",
            None,
            "Dashboard Service endpoint (host:port or URL)",
        )
    if "dashboard_id" not in flags.FLAGS:
        flags.DEFINE_string(
            "dashboard_id",
            "example_vcc_comparison",
            "Dashboard ID slug to validate",
        )
    if "expected_work_units" not in flags.FLAGS:
        flags.DEFINE_integer(
            "expected_work_units",
            1,
            "Expected number of WorkUnits",
        )
    if "expected_title_pattern" not in flags.FLAGS:
        flags.DEFINE_string(
            "expected_title_pattern",
            None,
            "Expected substring in title",
        )
except ImportError:
    app = None
    flags = None
    FLAGS = None


def get_flag(flags_obj: Any, name: str, default: Any = None) -> Any:
    """Safely retrieves a flag value from absl FLAGS or a mock SimpleNamespace."""
    if flags_obj is None:
        return default
    val = getattr(flags_obj, name, None)
    return default if val is None else val


def validate_argv(argv: Sequence[str]) -> None:
    """Validates command-line arguments to ensure no extra positional arguments exist."""
    if len(argv) > 1:
        if app is not None and hasattr(app, "UsageError"):
            raise app.UsageError("Too many command-line arguments.")
        raise ValueError("Too many command-line arguments.")


def parse_xid(raw_xid: str | None, required: bool = True) -> str:
    """Normalizes and extracts the numeric or slug XID string."""
    if not raw_xid or not str(raw_xid).strip():
        if required:
            raise ValueError("Experiment XID (--xid) is required.")
        return ""
    xid = str(raw_xid).strip()
    if xid.startswith("experiments/"):
        xid = xid.split("/")[-1]
    elif xid.startswith("dashboard:"):
        xid = xid.split("dashboard:", 1)[1].strip()
    elif xid.startswith("dashboards/"):
        xid = xid.split("dashboards/", 1)[1].strip()
    return xid


def resolve_endpoint(provided_endpoint: str | None = None) -> str:
    """Resolves ESS gRPC endpoint from parameter, environment, or default."""
    return provided_endpoint or os.environ.get("XMANAGER_ENDPOINT", "localhost:8080")


def resolve_dashboard_endpoint(provided_endpoint: str | None = None) -> str:
    """Derives HTTP REST base endpoint for dashboard validation."""
    if provided_endpoint:
        ep = provided_endpoint.strip()
        if ep.startswith(("http://", "https://")):
            return ep
        if ep.startswith(("localhost", "127.0.0.1")):
            return f"http://{ep}"
        return f"https://{ep}"

    if custom_ep := (
        os.environ.get("XMC_DASHBOARD_ENDPOINT") or os.environ.get("DASHBOARD_ENDPOINT")
    ):
        return custom_ep.strip()

    if ui_url := os.environ.get("XMANAGER_UI_URL"):
        return ui_url.strip()

    if domain := os.environ.get("XMC_DOMAIN") or os.environ.get("GCP_DOMAIN_NAME"):
        d = domain.strip()
        if not d.startswith(("http://", "https://")):
            d = f"https://{d}"
        return d

    xmanager_endpoint = os.environ.get("XMANAGER_ENDPOINT", "localhost:8080")
    for prefix in ("dns:///", "dns://", "grpc://", "https://", "http://"):
        if xmanager_endpoint.startswith(prefix):
            xmanager_endpoint = xmanager_endpoint[len(prefix) :]
    host = xmanager_endpoint.split(":")[0]
    if host.startswith("grpc.api."):
        host = host[len("grpc.api.") :]
    elif host.startswith("grpc."):
        host = host[len("grpc.") :]

    scheme = "http" if host in {"localhost", "127.0.0.1", "::1"} else "https"
    port_part = (
        f":{xmanager_endpoint.split(':')[1]}"
        if ":" in xmanager_endpoint and host in {"localhost", "127.0.0.1"}
        else ""
    )
    return f"{scheme}://{host}{port_part}"


def resolve_ui_base_url() -> str:
    """Resolves Web UI base URL via xmc_entities_validator."""
    return xmc_entities_validator.resolve_ui_base_url()


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
    if app is not None:
        app.run(main_fn)
    else:
        print("absl is required to run as standalone CLI script.", file=sys.stderr)
        sys.exit(1)
