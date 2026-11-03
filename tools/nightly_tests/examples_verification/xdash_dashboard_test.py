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
    python3 -m tools.nightly_tests.examples_verification.xdash_dashboard_test --dashboard_id=example_vcc_comparison
"""

from __future__ import annotations

from collections.abc import Sequence

from tools.nightly_tests.verification import verifier_cli, workload_verification

DEFAULT_DASHBOARD_ID: str = "example_vcc_comparison"
DEFAULT_TITLE_PATTERN: str = "VCC"
DEFAULT_TIMEOUT_SEC: int = 120
EXPECTED_MIN_CHARTS: int = 3


def main(argv: Sequence[str]) -> None:
    """Main verification routine for xdash_dashboard."""
    workload_verification.verify_dashboard_workload(
        argv,
        banner_title="XMC xDash Dashboard Verification & Assertions",
        summary_title="ALL XDASH DASHBOARD ASSERTIONS PASSED SUCCESSFULLY!",
        default_dashboard_id=DEFAULT_DASHBOARD_ID,
        expected_title_pattern=DEFAULT_TITLE_PATTERN,
        expected_min_charts=EXPECTED_MIN_CHARTS,
        default_timeout_sec=DEFAULT_TIMEOUT_SEC,
    )


if __name__ == "__main__":
    verifier_cli.run_main(main)
