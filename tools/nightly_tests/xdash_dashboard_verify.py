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

from collections.abc import Sequence

from tools.nightly_tests import common, xmc_entities_validator

FLAGS = common.FLAGS

# Domain assertion constants for xdash_dashboard
DEFAULT_DASHBOARD_ID: str = "example_vcc_comparison"
DEFAULT_TITLE_PATTERN: str = "VCC"
DEFAULT_TIMEOUT_SEC: int = 120


def main(argv: Sequence[str]) -> None:
    """Main verification routine for xdash_dashboard."""
    common.validate_argv(argv)

    raw_id = common.get_flag(FLAGS, "xid", None) or common.get_flag(FLAGS, "dashboard_id", None)
    dashboard_id = common.parse_xid(raw_id, required=False) or DEFAULT_DASHBOARD_ID

    expected_title_pattern = common.get_flag(FLAGS, "expected_title_pattern", DEFAULT_TITLE_PATTERN)
    timeout_sec = common.get_flag(FLAGS, "timeout_sec", DEFAULT_TIMEOUT_SEC)
    dashboard_endpoint = common.resolve_dashboard_endpoint(common.get_flag(FLAGS, "endpoint", None))
    ui_base_url = common.resolve_ui_base_url()

    common.print_banner(
        "XMC xDash Dashboard Verification & Assertions",
        [
            ("Dashboard ID", dashboard_id),
            ("Expected Title Pattern", f"'{expected_title_pattern}'"),
            ("Dashboard REST Endpoint", dashboard_endpoint),
            ("Timeout", f"{timeout_sec}s"),
        ],
    )

    with common.handle_verification_errors():
        # Validate Dashboard and referenced charts via REST API
        print(f"\n[1/2] Querying and validating Dashboard '{dashboard_id}'...")
        dash_results = xmc_entities_validator.validate_dashboard(
            dashboard_endpoint=dashboard_endpoint,
            expected_dashboards=[
                {
                    "dashboard_id": dashboard_id,
                    "expected_title": expected_title_pattern,
                }
            ],
            timeout_sec=timeout_sec,
        )

        if not dash_results:
            raise AssertionError(f"Dashboard '{dashboard_id}' was not returned by endpoint.")

        dash_info = dash_results[0]
        print(f"  [✓] Resource Name     : {dash_info['name']}")
        print(f"  [✓] Dashboard Title   : {dash_info['title']}")
        print(f"  [✓] Referenced Charts : {len(dash_info['chart_ids'])}")

        print("\n[2/2] Validating Constituent Charts...")
        for chart in dash_info.get("charts", []):
            print(f"  [✓] Chart '{chart.get('name', 'unknown')}': Title='{chart.get('title', '')}'")

        # Final Report
        common.print_success_summary(
            "ALL XDASH DASHBOARD ASSERTIONS PASSED SUCCESSFULLY!",
            [
                ("Dashboard ID", dashboard_id),
                ("Dashboard Title", dash_info["title"]),
                ("Validated Charts", len(dash_info.get("charts", []))),
                ("Web UI Link", f"{ui_base_url}/dashboards/{dashboard_id}"),
            ],
        )


if __name__ == "__main__":
    common.run_main(main)
