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

"""Unit tests for resource_observability_dashboard_ui_verify module."""

from __future__ import annotations

import contextlib
import unittest
from typing import Any
from unittest import mock

from tools.nightly_tests.examples_verification import (
    resource_observability_dashboard_ui_verify as rod_verify,
)
from tools.nightly_tests.verification import (
    ui_css_selectors as sel,
    ui_entities_validator,
)


def _make_row_mock(wid: str, pos: str) -> Any:
    """Creates a mock Playwright row locator returning wid and pos cell text."""
    row = mock.MagicMock()

    def _cell_locator(selector: str) -> Any:
        cell = mock.MagicMock()
        if selector == sel.JOB_WORKUNIT_CELL_SEL:
            cell.first.text_content.return_value = wid
        elif selector == sel.JOB_POSITION_CELL_SEL:
            cell.first.text_content.return_value = pos
        return cell

    row.locator.side_effect = _cell_locator
    return row


class ResourceObservabilityDashboardUIVerifyTest(unittest.TestCase):
    """Test suite for resource_observability_dashboard_ui_verify helpers and CLI."""

    def test_resolve_cluster_queue_and_parse_velocity_rate(self) -> None:
        self.assertEqual(rod_verify._resolve_cluster_queue(None), "cpu-cluster-queue")
        self.assertEqual(rod_verify._resolve_cluster_queue("cpu-queue"), "cpu-cluster-queue")
        self.assertEqual(
            rod_verify._resolve_cluster_queue("gpu-cluster-queue"), "gpu-cluster-queue"
        )
        self.assertEqual(rod_verify._parse_velocity_rate("0.86 jobs/hr"), 0.86)
        self.assertEqual(rod_verify._parse_velocity_rate("~1.34 cpu/hr"), 1.34)
        self.assertEqual(rod_verify._parse_velocity_rate("N/A"), 0.0)

    def test_wait_for_selector_with_retry_handles_timeout_and_http_errors(self) -> None:
        session = ui_entities_validator.XMCUIBrowserSession("https://xmc.example.com")
        session._page = mock.MagicMock()
        session.http_errors = ["500 https://xmc.example.com/v1/clusterqueues:batchGetSecondaryData"]
        session.wait_for_selector_with_retry(sel.QUEUE_CARD_SEL, 5000)
        self.assertEqual(session.http_errors, [])
        session.page.reload.assert_called_once_with(wait_until="networkidle", timeout=5000)

        session2 = ui_entities_validator.XMCUIBrowserSession("https://xmc.example.com")
        session2._page = mock.MagicMock()
        session2.page.wait_for_selector.side_effect = [RuntimeError("timeout"), None]
        session2.wait_for_selector_with_retry(sel.QUEUE_CARD_SEL, 5000)
        session2.page.reload.assert_called_once_with(wait_until="networkidle", timeout=5000)

    def test_collect_and_verify_work_units_across_paginator_pages(self) -> None:
        session = mock.MagicMock()
        page1_rows = [_make_row_mock("1", "N/A - running"), _make_row_mock("2", "1")]
        page2_rows = [_make_row_mock("3", "2")]
        state: dict[str, Any] = {"page": 0, "filter_args": []}

        def _locator_side_effect(selector: str) -> Any:
            loc = mock.MagicMock()
            if selector == sel.JOBS_TABLE_ROW_SEL:
                current = page1_rows if state["page"] == 0 else page2_rows
                filtered = mock.MagicMock()
                filtered.count.return_value = len(current)
                filtered.nth.side_effect = lambda idx: current[idx]

                def _record_filter(**kwargs: Any) -> Any:
                    state["filter_args"].append(kwargs)
                    return filtered

                loc.filter.side_effect = _record_filter
            elif selector == sel.JOBS_NEXT_PAGE_BTN_SEL:
                loc.count.return_value = 1 if state["page"] == 0 else 0
                loc.first.click.side_effect = lambda: state.__setitem__("page", 1)
            return loc

        session.page.locator.side_effect = _locator_side_effect
        rod_verify._verify_work_units_in_jobs_table(
            session, "cpu-cluster-queue", "gpu-cluster-queue", "experiments/36272"
        )
        self.assertEqual(state["page"], 1)
        self.assertEqual(state["filter_args"], [{"has_text": "Experiment #36272"}] * 2)

        state["page"] = 0
        with self.assertRaises(ui_entities_validator.UIVerificationError) as err_ctx:
            rod_verify._verify_work_units_in_jobs_table(
                session, "cpu-cluster-queue", "gpu-cluster-queue", "invalid-xid"
            )
        self.assertIn("Invalid numeric XID: 'invalid-xid'", str(err_ctx.exception))

    def test_main_passes_keyword_args_and_stops_work_units_in_finally(self) -> None:
        flag_vals = {
            "ui_base_url": "https://xmc.example.com",
            "auth_token": "tok-123",
            "headless": False,
            "screenshots_dir": "/tmp/custom_shots",
            "xid": "experiments/36272",
            "expected_queue": "cpu-queue",
            "endpoint": "grpc.xmc.example.com:443",
        }
        mock_stub = mock.MagicMock()

        @contextlib.contextmanager
        def _fake_ess_channel(ep: str | None) -> Any:
            yield (mock_stub, ep)

        with (
            mock.patch.object(
                rod_verify.cli,
                "get_flag",
                side_effect=lambda _, name, d=None: flag_vals.get(name, d),
            ),
            mock.patch.object(rod_verify.ess, "ess_channel_context", side_effect=_fake_ess_channel),
            mock.patch.object(rod_verify.Session, "start"),
            mock.patch.object(rod_verify.Session, "close"),
            mock.patch.object(rod_verify, "verify_resource_dashboard_ui") as mock_verify,
            mock.patch.object(rod_verify.xmc_entities_validator, "stop_work_units") as mock_stop,
        ):
            rod_verify.main(["prog"])
            s = mock_verify.call_args[0][0]
            self.assertEqual(s.ui_base_url, "https://xmc.example.com")
            self.assertEqual(s.auth_token, "tok-123")
            self.assertFalse(s.headless)
            self.assertEqual(s.screenshots_dir, "/tmp/custom_shots")
            mock_verify.assert_called_once()
            mock_stop.assert_called_once_with(mock_stub, "36272")
            mock_stop.reset_mock()
            rod_verify._stop_experiment_work_units("invalid-xid", "grpc.xmc.example.com:443")
            mock_stop.assert_not_called()


if __name__ == "__main__":
    unittest.main()
