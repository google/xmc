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

"""Validation script for xdash dashboard builder in XMC.

Asserts that the xdash comparative metrics dashboard and its constituent charts
were correctly created and are resolvable via the Dashboard Service REST/gRPC API.

Usage:
    python3 -m tools.nightly_tests.xdash_dashboard_verify --dashboard_id=example_vcc_comparison
"""

from __future__ import annotations

import os
from collections.abc import Sequence
from typing import Any

from absl import flags

from tools.nightly_tests import common, xmc_entities_validator

FLAGS = common.FLAGS

flags.DEFINE_string("dashboard_id", None, "Resource ID slug for the dashboard")
flags.DEFINE_string("expected_title_pattern", None, "Expected title pattern for the dashboard")

DEFAULT_DASHBOARD_ID: str = "example_vcc_comparison"
DEFAULT_TITLE_PATTERN: str = "VCC"
DEFAULT_TIMEOUT_SEC: int = 120
EXPECTED_MIN_CHARTS: int = 3


def _assert_dashboard(
    dashboard_endpoint: str, dashboard_id: str, expected_title_pattern: str, timeout_sec: int
) -> dict[str, Any]:
    """Queries and validates dashboard and referenced charts via REST API."""
    print(f"\n[1/2] Querying and validating Dashboard '{dashboard_id}'...")
    dash_results = xmc_entities_validator.validate_dashboard(
        endpoint=dashboard_endpoint,
        expected_dashboards=[
            {"dashboard_id": dashboard_id, "expected_title": expected_title_pattern}
        ],
        timeout_sec=timeout_sec,
    )
    if not dash_results:
        raise AssertionError(f"Dashboard '{dashboard_id}' was not returned by endpoint.")
    dash_info = dash_results[0]
    charts = dash_info.get("charts", [])
    if not dash_info.get("chart_ids") or len(charts) < EXPECTED_MIN_CHARTS:
        raise AssertionError(
            f"Dashboard '{dashboard_id}' expected at least {EXPECTED_MIN_CHARTS} constituent "
            f"charts, found {len(charts)} (chart_ids: {dash_info.get('chart_ids')})."
        )
    print(f"  [✓] Dashboard ID      : {dash_info.get('id') or dash_info.get('dashboard_id', '')}")
    print(f"  [✓] Dashboard Title   : {dash_info['title']}")
    print(f"  [✓] Referenced Charts : {len(dash_info['chart_ids'])}")
    print("\n[2/2] Validating Constituent Charts...")
    for chart in charts:
        cid = chart.get("name", chart.get("id", "unknown"))
        print(f"  [✓] Chart '{cid}': Title='{chart.get('title', '')}'")
    return dash_info


def _print_banner(
    dashboard_id: str, expected_title_pattern: str, dashboard_endpoint: str, timeout_sec: int
) -> None:
    """Prints verification header banner."""
    common.print_banner(
        "XMC xDash Dashboard Verification & Assertions",
        [
            ("Dashboard ID", dashboard_id),
            ("Expected Title Pattern", f"'{expected_title_pattern}'"),
            ("Dashboard REST Endpoint", dashboard_endpoint),
            ("Timeout", f"{timeout_sec}s"),
        ],
    )


def _print_summary(dashboard_id: str, dash_info: dict[str, Any], ui_base_url: str) -> None:
    """Prints verification success summary."""
    common.print_success_summary(
        "ALL XDASH DASHBOARD ASSERTIONS PASSED SUCCESSFULLY!",
        [
            ("Dashboard ID", dashboard_id),
            ("Dashboard Title", dash_info["title"]),
            ("Validated Charts", len(dash_info.get("charts", []))),
            ("Web UI Link", f"{ui_base_url}/dashboards/{dashboard_id}"),
        ],
    )


def main(argv: Sequence[str]) -> None:
    """Main verification routine for xdash_dashboard."""
    common.validate_argv(argv)
    raw_id = common.get_flag(FLAGS, "xid", None) or common.get_flag(FLAGS, "dashboard_id", None)
    dashboard_id = common.parse_xid(raw_id, required=False) or DEFAULT_DASHBOARD_ID
    expected_title_pattern = common.get_flag(FLAGS, "expected_title_pattern", DEFAULT_TITLE_PATTERN)
    timeout_sec = common.get_flag(FLAGS, "timeout_sec", DEFAULT_TIMEOUT_SEC)
    raw_dash_ep = (
        common.get_flag(FLAGS, "dashboard_endpoint", None)
        or os.environ.get("XMC_DASHBOARD_REST_ENDPOINT")
        or os.environ.get("DASHBOARD_ENDPOINT")
    )
    dashboard_endpoint = common.resolve_dashboard_endpoint(raw_dash_ep)
    ui_base_url = common.resolve_ui_base_url()

    _print_banner(dashboard_id, expected_title_pattern, dashboard_endpoint, timeout_sec)
    with common.handle_verification_errors():
        dash_info = _assert_dashboard(
            dashboard_endpoint, dashboard_id, expected_title_pattern, timeout_sec
        )
        _print_summary(dashboard_id, dash_info, ui_base_url)


if __name__ == "__main__":
    common.run_main(main)
