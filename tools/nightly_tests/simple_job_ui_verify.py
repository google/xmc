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

"""UI Traversal Verifier for Demonstration Workloads in XMC Nightly."""

from __future__ import annotations

from collections.abc import Sequence

from absl import flags

from tools.nightly_tests import common, ui_entities_validator

FLAGS = flags.FLAGS

flags.DEFINE_string("ui_base_url", None, "Base URL of the XMC Web UI")
flags.DEFINE_string("auth_token", None, "Optional Bearer/IAP auth token")
flags.DEFINE_bool("headless", True, "Run browser in headless mode")
flags.DEFINE_string("screenshots_dir", None, "Target directory for diagnostic screenshots")

EXPECTED_WORK_UNITS: int = 3
EXPECTED_WORK_UNIT_NAMES: tuple[str, ...] = ("1", "2", "3")


def verify_simple_job_ui(
    xid: str | int,
    ui_base_url: str,
    auth_token: str | None = None,
    headless: bool = True,
    screenshots_dir: str | None = None,
) -> None:
    """Executes UI traversal assertions for the simple demo workload experiment.

    Args:
        xid: Experiment XID to verify.
        ui_base_url: Base URL of the XMC Web UI.
        auth_token: Optional Bearer or IAP OIDC token.
        headless: Whether to run Chromium in headless mode.
        screenshots_dir: Directory where diagnostic screenshots are saved.
    """
    with ui_entities_validator.XMCUIBrowserSession(
        ui_base_url=ui_base_url,
        auth_token=auth_token,
        headless=headless,
        screenshots_dir=screenshots_dir,
    ) as session:
        session.navigate_to_experiment(xid)
        header = session.assert_experiment_header(expected_title_pattern="Demo Workload")
        session.take_screenshot(f"ui_step2_header_xid_{xid}.png")
        session.assert_work_units_tab(
            min_units=EXPECTED_WORK_UNITS,
            expected_names=EXPECTED_WORK_UNIT_NAMES,
        )
        session.take_screenshot(f"ui_step3_work_units_xid_{xid}.png")
        common.print_success_summary(
            "SIMPLE DEMO WORKLOAD UI TRAVERSAL PASSED SUCCESSFULLY!",
            [
                ("Experiment XID", str(xid)),
                ("UI Base URL", ui_base_url),
                ("Header Title", header),
                ("Diagnostic Screenshots", session.screenshots_dir),
            ],
        )


def main(argv: Sequence[str]) -> None:
    """CLI entrypoint."""
    common.validate_argv(argv)
    with common.handle_verification_errors():
        verify_simple_job_ui(
            xid=common.parse_xid(common.get_flag(FLAGS, "xid")),
            ui_base_url=common.get_flag(FLAGS, "ui_base_url") or common.resolve_ui_base_url(),
            auth_token=common.get_flag(FLAGS, "auth_token"),
            headless=common.get_flag(FLAGS, "headless", True),
            screenshots_dir=common.get_flag(FLAGS, "screenshots_dir"),
        )


if __name__ == "__main__":
    common.run_main(main)
