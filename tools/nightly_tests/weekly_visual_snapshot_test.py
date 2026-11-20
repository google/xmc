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

"""Unit tests for weekly_visual_snapshot module."""

from __future__ import annotations

import json
import os
import re
import tempfile
import types
import unittest
from collections.abc import Callable, Mapping
from unittest import mock

from tools.nightly_tests import weekly_visual_snapshot


class _FakeLocator:
    """Minimal Playwright Locator test double for table, link, and state queries."""

    def __init__(self, items: list[_FakeElement] | None = None) -> None:
        self._items = items or []

    def count(self) -> int:
        return len(self._items)

    def nth(self, index: int) -> _FakeElement:
        return self._items[index]

    @property
    def first(self) -> _FakeElement:
        return self._items[0]


class _FakeElement:
    """Minimal Playwright element test double supporting nested locator queries."""

    def __init__(
        self,
        *,
        text: str = "",
        attrs: Mapping[str, str] | None = None,
        children: Mapping[str, list[_FakeElement]] | None = None,
        on_click: Callable[[], None] | None = None,
    ) -> None:
        self._text = text
        self._attrs = dict(attrs or {})
        self._children = dict(children or {})
        self._on_click = on_click

    def inner_text(self) -> str:
        return self._text

    def get_attribute(self, name: str) -> str | None:
        return self._attrs.get(name)

    def locator(self, selector: str) -> _FakeLocator:
        return _FakeLocator(self._children.get(selector, []))

    def click(self) -> None:
        if self._on_click is not None:
            self._on_click()


class _FakeRoutePage:
    """Playwright Page test double routing URLs to preconfigured DOM element maps."""

    def __init__(
        self,
        routes: Mapping[str, Callable[[], Mapping[str, list[_FakeElement]]]],
        on_screenshot: Callable[[str], None] | None = None,
    ) -> None:
        self._routes = dict(routes)
        self._on_screenshot = on_screenshot
        self.url = ""
        self.visited_urls: list[str] = []

    def goto(self, url: str, wait_until: str = "networkidle", timeout: int = 60000) -> None:
        del wait_until, timeout
        self.url = url
        self.visited_urls.append(url)

    def locator(self, selector: str) -> _FakeLocator:
        path = re.sub(r"^https?://[^/]+", "", self.url)
        dom_factory = self._routes.get(path)
        if dom_factory is None:
            return _FakeLocator([])
        dom = dom_factory()
        for candidate in (selector, *(part.strip() for part in selector.split(","))):
            if candidate in dom:
                return _FakeLocator(dom[candidate])
        return _FakeLocator([])

    def wait_for_selector(self, selector: str, timeout: int = 30000) -> None:
        del selector, timeout

    def wait_for_load_state(self, state: str = "networkidle", timeout: int = 30000) -> None:
        del state, timeout

    def screenshot(self, path: str, full_page: bool = True) -> None:
        del full_page
        if self._on_screenshot is not None:
            self._on_screenshot(path)


def _experiment_row(
    xid: str, *, has_work_units: bool, use_badge_classes: bool = True
) -> _FakeElement:
    """Builds a fake /experiments table row element."""
    children: dict[str, list[_FakeElement]] = {
        'a[href*="/experiments/"]': [_FakeElement(attrs={"href": f"/experiments/{xid}"})],
    }
    if use_badge_classes:
        badge_sel = ".status-bar" if has_work_units else ".no-work-units"
        children[badge_sel] = [_FakeElement(text="1" if has_work_units else "No Work Units")]
    text = f"{xid} Exp {xid}" if has_work_units else f"{xid} Exp {xid} No Work Units"
    return _FakeElement(text=text, children=children)


class WeeklyVisualSnapshotTest(unittest.TestCase):
    """Test suite for weekly_visual_snapshot library and CLI."""

    def setUp(self) -> None:
        super().setUp()
        self.temp_dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp_dir.cleanup)
        self.out_dir = self.temp_dir.name

    def _write_file(self, rel_path: str, data: bytes) -> str:
        full_path = os.path.join(self.out_dir, rel_path)
        os.makedirs(os.path.dirname(full_path), exist_ok=True)
        with open(full_path, "wb") as f:
            f.write(data)
        return full_path

    def _mock_session(self) -> mock.MagicMock:
        session = mock.MagicMock()
        session.__enter__.return_value = session
        session.ui_base_url = "https://xmc.example.com"
        session.page.screenshot.side_effect = lambda path, full_page=True: self._write_file(
            os.path.relpath(path, self.out_dir), b"PNG"
        )
        return session

    def _route_session(self, page: _FakeRoutePage) -> mock.MagicMock:
        session = mock.MagicMock()
        session.__enter__.return_value = session
        session.ui_base_url = "https://xmc.example.com"
        session.page = page
        return session

    def test_golden_routes_definitions(self) -> None:
        route_ids = [r.id for r in weekly_visual_snapshot.GOLDEN_ROUTES]
        self.assertEqual(
            route_ids,
            [
                "01_experiments_list",
                "03_experiment_workunits",
                "05_workunit_details",
                "06_resource_dashboard",
            ],
        )
        self.assertEqual(weekly_visual_snapshot.GOLDEN_ROUTES[0].route, "/experiments")
        self.assertEqual(
            weekly_visual_snapshot.GOLDEN_ROUTES[2].route,
            "/experiments/{xid}/workUnits/{wid}",
        )

    def test_discover_candidate_xids_skips_no_work_units_rows(self) -> None:
        rows = [
            _experiment_row("101", has_work_units=False),
            _experiment_row("102", has_work_units=False, use_badge_classes=False),
            _experiment_row("103", has_work_units=True),
            _experiment_row("104", has_work_units=True),
            _experiment_row("103", has_work_units=True),
        ]
        page = _FakeRoutePage({"/experiments": lambda: {"tbody tr:not(.detail-row)": rows}})
        page.goto("https://xmc.example.com/experiments")

        self.assertEqual(
            weekly_visual_snapshot.discover_candidate_xids_from_page(page),
            ["103", "104"],
        )
        self.assertEqual(weekly_visual_snapshot.discover_xid_from_page(page), "103")

    def test_discover_xid_from_page_link_fallback_and_empty(self) -> None:
        page_with_link = mock.MagicMock()
        links = mock.MagicMock(count=mock.MagicMock(return_value=1))
        links.nth.return_value.get_attribute.return_value = "/experiments/108"
        page_with_link.locator.side_effect = lambda sel: (
            mock.MagicMock(count=mock.MagicMock(return_value=0)) if "tbody" in sel else links
        )
        self.assertEqual(weekly_visual_snapshot.discover_xid_from_page(page_with_link), "108")

        page_empty = mock.MagicMock()
        page_empty.locator.return_value = mock.MagicMock(count=mock.MagicMock(return_value=0))
        self.assertEqual(weekly_visual_snapshot.discover_xid_from_page(page_empty), "")

    def test_discover_workunit_ids_from_page(self) -> None:
        links = [
            _FakeElement(attrs={"href": "/experiments/99/workUnits/1"}, text="1"),
            _FakeElement(attrs={"href": "/experiments/42/workUnits/7"}, text="7"),
            _FakeElement(attrs={"href": "/experiments/42/workUnits/12"}, text="12"),
            _FakeElement(attrs={"href": "/experiments/42/workUnits/7"}, text="7"),
        ]
        page = _FakeRoutePage(
            {
                "/experiments/42": lambda: {
                    'xmc-work-units-table a[href*="/workUnits/"]': links,
                }
            }
        )
        page.goto("https://xmc.example.com/experiments/42")

        self.assertEqual(
            weekly_visual_snapshot.discover_workunit_ids_from_page(page, xid="42"),
            ["7", "12"],
        )
        self.assertEqual(weekly_visual_snapshot.discover_workunit_id_from_page(page, xid="42"), "7")

    def test_probe_experiment_workunit_handles_acl_403_empty_and_valid(self) -> None:
        routes: dict[str, Callable[[], Mapping[str, list[_FakeElement]]]] = {
            # 403 ACL mismatch on GetExperiment -> no title rendered
            "/experiments/201": lambda: {},
            # Experiment loads, but 0 work units or 403 on ListWorkUnits -> no workUnit links
            "/experiments/202": lambda: {
                "h1.experiment-title": [_FakeElement(text="Empty Or Forbidden WorkUnits")],
                "xmc-work-units-table .no-work-units-message": [
                    _FakeElement(text="This experiment has no work units.")
                ],
            },
            # Experiment loads with workUnit 3 (empty details) and workUnit 9 (valid details)
            "/experiments/203": lambda: {
                "h1.experiment-title": [_FakeElement(text="Valid Experiment")],
                'mat-tab-header .mdc-tab:has-text("Work Units")': [_FakeElement(text="Work Units")],
                'xmc-work-units-table a[href*="/workUnits/"]': [
                    _FakeElement(attrs={"href": "/experiments/203/workUnits/3"}, text="3"),
                    _FakeElement(attrs={"href": "/experiments/203/workUnits/9"}, text="9"),
                ],
            },
            "/experiments/203/workUnits/3": lambda: {},
            "/experiments/203/workUnits/9": lambda: {
                "xmc-work-unit-details": [_FakeElement(text="Metadata")],
            },
        }
        session = self._route_session(_FakeRoutePage(routes))

        self.assertEqual(weekly_visual_snapshot.probe_experiment_workunit(session, "201"), "")
        self.assertEqual(weekly_visual_snapshot.probe_experiment_workunit(session, "202"), "")
        self.assertEqual(weekly_visual_snapshot.probe_experiment_workunit(session, "203"), "9")
        session.switch_tab.assert_called_once_with("Work Units", timeout_ms=15000)

    def test_discover_experiment_and_workunit_paginates_on_exhausted_page(self) -> None:
        current_page = 0

        def next_page() -> None:
            nonlocal current_page
            current_page += 1

        def experiments_dom() -> Mapping[str, list[_FakeElement]]:
            if current_page == 0:
                return {
                    "tbody tr:not(.detail-row)": [
                        _experiment_row("301", has_work_units=False),
                        _experiment_row("302", has_work_units=True),
                    ],
                    'button[data-test-id="next-page-button"]:not([disabled])': [
                        _FakeElement(on_click=next_page)
                    ],
                }
            return {
                "tbody tr:not(.detail-row)": [
                    _experiment_row("303", has_work_units=True),
                ],
            }

        routes: dict[str, Callable[[], Mapping[str, list[_FakeElement]]]] = {
            "/experiments": experiments_dom,
            "/experiments/302": lambda: {},  # 403 ACL mismatch
            "/experiments/303": lambda: {
                "h1.experiment-title": [_FakeElement(text="Accessible Exp")],
                'xmc-work-units-table a[href*="/workUnits/"]': [
                    _FakeElement(attrs={"href": "/experiments/303/workUnits/4"}, text="4"),
                ],
            },
            "/experiments/303/workUnits/4": lambda: {
                "xmc-work-unit-details": [_FakeElement(text="Details")],
            },
        }
        page = _FakeRoutePage(routes)
        page.goto("https://xmc.example.com/experiments")
        session = self._route_session(page)

        self.assertEqual(
            weekly_visual_snapshot.discover_experiment_and_workunit(session, max_pages=3),
            ("303", "4"),
        )

    def test_capture_all_routes_saves_screenshots_with_discovered_wid(self) -> None:
        session = self._mock_session()
        with mock.patch.object(
            weekly_visual_snapshot, "discover_workunit_id_from_page", return_value="7"
        ):
            caps = weekly_visual_snapshot.capture_all_routes(
                session, output_dir=self.out_dir, xid=42
            )
        self.assertEqual(len(caps), 4)
        for cap in caps:
            self.assertTrue(
                os.path.isfile(os.path.join(self.out_dir, "screenshots", cap["filename"]))
            )
        self.assertEqual(caps[1]["route"], "/experiments/42")
        self.assertEqual(caps[2]["route"], "/experiments/42/workUnits/7")
        session.switch_tab.assert_called_once_with("Work Units", timeout_ms=60000)

    def test_capture_all_routes_skips_workunit_details_when_no_workunits(self) -> None:
        session = self._mock_session()
        with (
            mock.patch.object(
                weekly_visual_snapshot, "discover_workunit_id_from_page", return_value=""
            ),
            mock.patch.object(weekly_visual_snapshot, "probe_experiment_workunit", return_value=""),
        ):
            caps = weekly_visual_snapshot.capture_all_routes(
                session, output_dir=self.out_dir, xid=42
            )
        self.assertEqual(
            [c["id"] for c in caps],
            ["01_experiments_list", "03_experiment_workunits", "06_resource_dashboard"],
        )

    def test_capture_single_route_normalizes_trailing_slash_and_relative_route(self) -> None:
        session = self._mock_session()
        session.ui_base_url = "https://xmc.example.com///"
        route_spec = weekly_visual_snapshot.GoldenRoute(
            id="test_route",
            title="Test Route",
            route="experiments/{xid}/workUnits/{wid}",
        )
        cap = weekly_visual_snapshot.capture_single_route(
            session, route_spec, xid=123, output_dir=self.out_dir, wid=5
        )
        self.assertEqual(session.ui_base_url, "https://xmc.example.com")
        self.assertEqual(cap["route"], "/experiments/123/workUnits/5")
        session.page.goto.assert_called_once_with(
            "https://xmc.example.com/experiments/123/workUnits/5",
            wait_until="networkidle",
            timeout=60000,
        )

    def test_capture_all_routes_auto_discovers_or_skips_xid_routes(self) -> None:
        session = self._mock_session()
        with mock.patch.object(
            weekly_visual_snapshot,
            "discover_experiment_and_workunit",
            return_value=("88", "3"),
        ):
            caps_found = weekly_visual_snapshot.capture_all_routes(
                session, output_dir=self.out_dir, xid=""
            )
        self.assertEqual(caps_found[1]["route"], "/experiments/88")
        self.assertEqual(caps_found[2]["route"], "/experiments/88/workUnits/3")

        with mock.patch.object(
            weekly_visual_snapshot,
            "discover_experiment_and_workunit",
            return_value=("", ""),
        ):
            caps_skipped = weekly_visual_snapshot.capture_all_routes(
                session, output_dir=self.out_dir, xid=""
            )
        self.assertEqual(
            [c["id"] for c in caps_skipped], ["01_experiments_list", "06_resource_dashboard"]
        )

    def test_build_manifest_and_write_gallery_artifacts(self) -> None:
        captures = [
            {
                "id": "01",
                "title": "T </script><script>alert(1)</script>",
                "route": "/exp",
                "filename": "01.png",
            }
        ]
        manifest = weekly_visual_snapshot.build_manifest(
            "https://xmc.example.com///",
            99,
            "abc1234",
            captures,
            generated_at="2026-09-23T00:00:00Z",
        )
        self.assertEqual(manifest["commit_sha"], "abc1234")
        self.assertEqual(manifest["ui_base_url"], "https://xmc.example.com")
        self.assertEqual(manifest["captures"], captures)

        man_path, html_path = weekly_visual_snapshot.write_gallery_artifacts(self.out_dir, manifest)
        with open(man_path, encoding="utf-8") as f_m, open(html_path, encoding="utf-8") as f_h:
            loaded = json.load(f_m)
            html = f_h.read()
        self.assertEqual(loaded["commit_sha"], "abc1234")
        self.assertIn("XMC Weekly Visual UI Gallery", html)
        self.assertIn(r"T \u003c/script>", html)
        self.assertNotIn("</script><script>", html)

        # Verify embedded JSON safely unescapes via standard JSON parsing
        script_match = re.search(
            r'<script id="embedded-manifest"[^>]*>(.*?)</script>', html, re.DOTALL
        )
        self.assertIsNotNone(script_match)
        assert script_match is not None
        embedded_json = json.loads(script_match.group(1))
        self.assertEqual(
            embedded_json["captures"][0]["title"], "T </script><script>alert(1)</script>"
        )

    def test_run_visual_snapshot_normalizes_trailing_slash(self) -> None:
        session = self._mock_session()
        with (
            mock.patch.object(
                weekly_visual_snapshot.ui_entities_validator,
                "XMCUIBrowserSession",
            ) as mock_session_cls,
            mock.patch.object(
                weekly_visual_snapshot, "discover_workunit_id_from_page", return_value="2"
            ),
        ):

            def create_session(**kwargs):
                session.ui_base_url = kwargs.get("ui_base_url", "")
                return session

            mock_session_cls.side_effect = create_session
            manifest = weekly_visual_snapshot.run_visual_snapshot(
                ui_base_url="https://xmc.example.com///",
                xid=55,
                output_dir=self.out_dir,
                commit_sha="sha999",
            )
        mock_session_cls.assert_called_once_with(
            ui_base_url="https://xmc.example.com",
            auth_token=None,
            headless=True,
            screenshots_dir=os.path.join(self.out_dir, "screenshots"),
        )
        self.assertEqual(manifest["ui_base_url"], "https://xmc.example.com")
        self.assertEqual(manifest["captures"][2]["route"], "/experiments/55/workUnits/2")
        goto_calls = session.page.goto.call_args_list
        self.assertTrue(goto_calls)
        for call_args in goto_calls:
            target_url = call_args[0][0]
            self.assertTrue(target_url.startswith("https://xmc.example.com/"))
            self.assertFalse(target_url.startswith("https://xmc.example.com//"))

    def test_run_visual_snapshot_and_main(self) -> None:
        mock_session = self._mock_session()
        fake_flags = types.SimpleNamespace(
            ui_base_url="https://xmc.example.com",
            output_dir=self.out_dir,
            commit_sha="sha777",
            auth_token="tok",
            headless=True,
        )
        with (
            mock.patch.object(
                weekly_visual_snapshot.ui_entities_validator,
                "XMCUIBrowserSession",
                return_value=mock_session,
            ),
            mock.patch.object(weekly_visual_snapshot, "FLAGS", fake_flags),
            mock.patch.object(
                weekly_visual_snapshot,
                "discover_experiment_and_workunit",
                return_value=("77", "4"),
            ),
        ):
            weekly_visual_snapshot.main(["weekly_visual_snapshot.py"])
        with open(os.path.join(self.out_dir, "manifest.json"), encoding="utf-8") as f_man:
            manifest = json.load(f_man)
        self.assertEqual(manifest["xid"], "77")
        self.assertEqual(manifest["captures"][2]["route"], "/experiments/77/workUnits/4")
        self.assertTrue(os.path.isfile(os.path.join(self.out_dir, "index.html")))

    def test_main_with_explicit_xid_flag(self) -> None:
        mock_session = self._mock_session()
        fake_flags = types.SimpleNamespace(
            xid="experiments/999",
            ui_base_url="https://xmc.example.com",
            output_dir=self.out_dir,
            commit_sha="sha999",
            auth_token=None,
            headless=True,
        )
        with (
            mock.patch.object(
                weekly_visual_snapshot.ui_entities_validator,
                "XMCUIBrowserSession",
                return_value=mock_session,
            ),
            mock.patch.object(weekly_visual_snapshot, "FLAGS", fake_flags),
            mock.patch.object(
                weekly_visual_snapshot, "discover_experiment_and_workunit"
            ) as mock_discover,
            mock.patch.object(
                weekly_visual_snapshot, "discover_workunit_id_from_page", return_value="8"
            ),
        ):
            weekly_visual_snapshot.main(["weekly_visual_snapshot.py"])
        mock_discover.assert_not_called()
        with open(os.path.join(self.out_dir, "manifest.json"), encoding="utf-8") as f_man:
            manifest = json.load(f_man)
        self.assertEqual(manifest["xid"], "999")
        self.assertEqual(manifest["captures"][2]["route"], "/experiments/999/workUnits/8")

    def test_load_gallery_template(self) -> None:
        template = weekly_visual_snapshot.load_gallery_template()
        self.assertIn("XMC Weekly Visual UI Gallery", template)
        self.assertIn("__MANIFEST_JSON__", template)


if __name__ == "__main__":
    unittest.main()
