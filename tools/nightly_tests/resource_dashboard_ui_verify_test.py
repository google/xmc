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

"""Unit tests for resource_dashboard_ui_verify module."""

from __future__ import annotations

import types
import unittest
from typing import Any
from unittest import mock

from tools.nightly_tests import resource_dashboard_ui_verify, ui_entities_validator


class ResourceDashboardUIVerifyTest(unittest.TestCase):
    """Test suite for Resource Observability Dashboard (ROD) Playwright UI verifier."""

    def _build_mock_session(
        self,
        *,
        queue_titles: list[str] | None = None,
        flavor_text: str = "cpu",
        badge_text: str = "Available",
        copy_btn_text: str = "Copied",
        side_pane_queue: str | None = None,
        predictability_empty_count: int = 1,
        option_count: int = 2,
        option_text: str = "cpu",
        pill_class: str = "toggle-pill active-toggle",
        add_btn_enabled: bool = True,
        query_row_count: int = 2,
    ) -> Any:
        """Constructs a selector-aware mock XMCUIBrowserSession for unit testing."""
        titles = ["cpu-queue", "gpu-queue"] if queue_titles is None else queue_titles
        expected_side_pane = (
            (titles[1] if len(titles) > 1 else (titles[0] if titles else ""))
            if side_pane_queue is None
            else side_pane_queue
        )

        session = mock.MagicMock(spec=ui_entities_validator.XMCUIBrowserSession)
        session.screenshots_dir = "/tmp/xmc_screenshots"
        page = mock.MagicMock()
        session.page = page

        def _card_locator(sub_selector: str) -> Any:
            sub = mock.MagicMock()
            if ".flavor-main-name" in sub_selector:
                sub.first.text_content.return_value = flavor_text
            elif ".status-badge" in sub_selector:
                sub.first.text_content.return_value = badge_text
            elif "button.copy-flag-btn" in sub_selector:
                sub.first.text_content.return_value = copy_btn_text
            return sub

        mock_card = mock.MagicMock()
        mock_card.locator.side_effect = _card_locator

        def _page_locator(selector: str) -> Any:
            loc = mock.MagicMock()
            if selector == "xmc-rod-queue-card .queue-title":
                loc.all_inner_texts.return_value = titles
            elif selector == "xmc-rod-queue-card .queue-card":
                loc.first = mock_card
                loc.nth.return_value = mock_card
            elif selector == "xmc-queue-jobs-table .target-queue-name":
                loc.first.text_content.return_value = expected_side_pane
            elif selector == ".predictability-empty-state":
                loc.count.return_value = predictability_empty_count
            elif selector == "mat-option":
                loc.count.return_value = option_count
                opt = mock.MagicMock()
                opt.text_content.return_value = option_text
                loc.nth.return_value = opt
            elif "button.toggle-pill" in selector:
                loc.first.get_attribute.return_value = pill_class
            elif "button.add-resource-btn" in selector:
                loc.first.count.return_value = 1 if add_btn_enabled else 0
                loc.first.is_enabled.return_value = add_btn_enabled
            elif selector == "xmc-smart-resource-finder .query-row":
                loc.count.return_value = query_row_count
            return loc

        page.locator.side_effect = _page_locator
        return session

    def test_assert_queue_cards_success(self) -> None:
        session = self._build_mock_session()
        titles = resource_dashboard_ui_verify.assert_queue_cards(
            session, expected_queue="cpu-queue"
        )
        self.assertEqual(titles, ["cpu-queue", "gpu-queue"])

    def test_assert_queue_cards_failures(self) -> None:
        empty_session = self._build_mock_session(queue_titles=[])
        with self.assertRaisesRegex(
            ui_entities_validator.UIVerificationError, "No cluster queue cards"
        ):
            resource_dashboard_ui_verify.assert_queue_cards(empty_session)

        missing_q_session = self._build_mock_session(queue_titles=["cpu-queue"])
        with self.assertRaisesRegex(ui_entities_validator.UIVerificationError, "tpu-queue"):
            resource_dashboard_ui_verify.assert_queue_cards(
                missing_q_session, expected_queue="tpu-queue"
            )

        no_flavor_session = self._build_mock_session(flavor_text="")
        with self.assertRaisesRegex(ui_entities_validator.UIVerificationError, "missing flavor"):
            resource_dashboard_ui_verify.assert_queue_cards(no_flavor_session)

        bad_copy_session = self._build_mock_session(copy_btn_text="Copy flag")
        with self.assertRaisesRegex(ui_entities_validator.UIVerificationError, "Copied"):
            resource_dashboard_ui_verify.assert_queue_cards(bad_copy_session)

    def test_assert_queue_selection_and_jobs_pane_success_and_mismatch(self) -> None:
        session = self._build_mock_session(queue_titles=["cpu-queue", "gpu-queue"])
        selected = resource_dashboard_ui_verify.assert_queue_selection_and_jobs_pane(
            session, ["cpu-queue", "gpu-queue"]
        )
        self.assertEqual(selected, "gpu-queue")

        mismatch_session = self._build_mock_session(
            queue_titles=["cpu-queue"], side_pane_queue="wrong-queue"
        )
        with self.assertRaisesRegex(ui_entities_validator.UIVerificationError, "wrong-queue"):
            resource_dashboard_ui_verify.assert_queue_selection_and_jobs_pane(
                mismatch_session, ["cpu-queue"]
            )

    def test_assert_smart_resource_finder_and_predictability_success_and_errors(
        self,
    ) -> None:
        session = self._build_mock_session()
        res = resource_dashboard_ui_verify.assert_smart_resource_finder_and_predictability(session)
        self.assertEqual(res, "cpu")
        session.page.wait_for_selector.assert_any_call(".predictability-empty-state", timeout=15000)
        session.page.wait_for_selector.assert_any_call("mat-option", timeout=15000)
        session.page.wait_for_selector.assert_any_call(
            'xmc-rod-queue-list button.toggle-pill.active-toggle:has-text("1h")',
            timeout=15000,
        )

        no_empty_state = self._build_mock_session(predictability_empty_count=0)
        with self.assertRaisesRegex(
            ui_entities_validator.UIVerificationError, "predictability-empty-state"
        ):
            resource_dashboard_ui_verify.assert_smart_resource_finder_and_predictability(
                no_empty_state
            )

        no_options = self._build_mock_session(option_count=1)
        with self.assertRaisesRegex(
            ui_entities_validator.UIVerificationError, "selectable resource"
        ):
            resource_dashboard_ui_verify.assert_smart_resource_finder_and_predictability(no_options)

        bad_pill = self._build_mock_session(pill_class="toggle-pill")
        with self.assertRaisesRegex(ui_entities_validator.UIVerificationError, "active-toggle"):
            resource_dashboard_ui_verify.assert_smart_resource_finder_and_predictability(bad_pill)

        bad_row_add = self._build_mock_session(query_row_count=1)
        with self.assertRaisesRegex(
            ui_entities_validator.UIVerificationError, ">= 2 resource query rows"
        ):
            resource_dashboard_ui_verify.assert_smart_resource_finder_and_predictability(
                bad_row_add
            )

    def test_verify_resource_dashboard_in_session_and_ui_entrypoint(self) -> None:
        session = self._build_mock_session()
        session.__enter__.return_value = session
        session.__exit__.return_value = None

        with mock.patch.object(ui_entities_validator, "XMCUIBrowserSession", return_value=session):
            summary = resource_dashboard_ui_verify.verify_resource_dashboard_ui(
                ui_base_url="https://xmc.example.com",
                expected_queue="cpu-queue",
            )

        self.assertEqual(
            summary,
            {
                "queues": ["cpu-queue", "gpu-queue"],
                "selected_queue": "gpu-queue",
                "selected_resource": "cpu",
            },
        )
        session.navigate_to_route.assert_called_once()
        session.assert_no_runtime_errors.assert_called_once_with(
            "resource_dashboard",
            ignored_http_substrings=(":batchGetSecondaryData",),
        )

    def test_main_invokes_verify_resource_dashboard_ui(self) -> None:
        fake_flags = types.SimpleNamespace(
            ui_base_url="https://xmc.example.com",
            auth_token="tok",
            headless=True,
            screenshots_dir="/tmp/shots",
            expected_queue="cpu-queue",
        )
        with (
            mock.patch.object(resource_dashboard_ui_verify, "FLAGS", fake_flags),
            mock.patch.object(
                resource_dashboard_ui_verify, "verify_resource_dashboard_ui"
            ) as mock_verify,
        ):
            resource_dashboard_ui_verify.main(["prog"])
            mock_verify.assert_called_once_with(
                ui_base_url="https://xmc.example.com",
                auth_token="tok",
                headless=True,
                screenshots_dir="/tmp/shots",
                expected_queue="cpu-queue",
            )


if __name__ == "__main__":
    unittest.main()
