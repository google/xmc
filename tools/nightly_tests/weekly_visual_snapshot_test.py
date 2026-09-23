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
import tempfile
import types
import unittest
from unittest import mock

from tools.nightly_tests import weekly_visual_snapshot


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

    def test_discover_xid_from_page(self) -> None:
        page_with_link = mock.MagicMock()
        links = mock.MagicMock(count=mock.MagicMock(return_value=1))
        links.nth.return_value.get_attribute.return_value = "/experiments/108"
        page_with_link.locator.return_value = links
        self.assertEqual(weekly_visual_snapshot.discover_xid_from_page(page_with_link), "108")

        page_empty = mock.MagicMock()
        page_empty.locator.return_value = mock.MagicMock(count=mock.MagicMock(return_value=0))
        self.assertEqual(weekly_visual_snapshot.discover_xid_from_page(page_empty), "")

    def test_capture_all_routes_saves_screenshots(self) -> None:
        session = self._mock_session()
        caps = weekly_visual_snapshot.capture_all_routes(session, output_dir=self.out_dir, xid=42)
        self.assertEqual(len(caps), 4)
        for cap in caps:
            self.assertTrue(
                os.path.isfile(os.path.join(self.out_dir, "screenshots", cap["filename"]))
            )
        self.assertEqual(caps[1]["route"], "/experiments/42")
        session.switch_tab.assert_called_once_with("Work Units", timeout_ms=60000)

    def test_capture_all_routes_auto_discovers_or_skips_xid_routes(self) -> None:
        session = self._mock_session()
        with mock.patch.object(weekly_visual_snapshot, "discover_xid_from_page", return_value="88"):
            caps_found = weekly_visual_snapshot.capture_all_routes(
                session, output_dir=self.out_dir, xid=""
            )
        self.assertEqual(caps_found[1]["route"], "/experiments/88")

        with mock.patch.object(weekly_visual_snapshot, "discover_xid_from_page", return_value=""):
            caps_skipped = weekly_visual_snapshot.capture_all_routes(
                session, output_dir=self.out_dir, xid=""
            )
        self.assertEqual(
            [c["id"] for c in caps_skipped], ["01_experiments_list", "06_resource_dashboard"]
        )

    def test_build_manifest_and_write_gallery_artifacts(self) -> None:
        captures = [{"id": "01", "title": "T </script>", "route": "/exp", "filename": "01.png"}]
        manifest = weekly_visual_snapshot.build_manifest(
            "https://xmc.example.com/", 99, "abc1234", captures, generated_at="2026-09-23T00:00:00Z"
        )
        self.assertEqual(manifest["commit_sha"], "abc1234")
        self.assertEqual(manifest["captures"], captures)

        man_path, html_path = weekly_visual_snapshot.write_gallery_artifacts(self.out_dir, manifest)
        with open(man_path, encoding="utf-8") as f_m, open(html_path, encoding="utf-8") as f_h:
            loaded = json.load(f_m)
            html = f_h.read()
        self.assertEqual(loaded["commit_sha"], "abc1234")
        self.assertIn("XMC Weekly Visual UI Gallery", html)
        self.assertIn("T <\\/script>", html)

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
            mock.patch.object(weekly_visual_snapshot, "discover_xid_from_page", return_value="77"),
        ):
            weekly_visual_snapshot.main(["weekly_visual_snapshot.py"])
        with open(os.path.join(self.out_dir, "manifest.json"), encoding="utf-8") as f_man:
            manifest = json.load(f_man)
        self.assertEqual(manifest["xid"], "77")
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
            mock.patch.object(weekly_visual_snapshot, "discover_xid_from_page") as mock_discover,
        ):
            weekly_visual_snapshot.main(["weekly_visual_snapshot.py"])
        mock_discover.assert_not_called()
        with open(os.path.join(self.out_dir, "manifest.json"), encoding="utf-8") as f_man:
            self.assertEqual(json.load(f_man)["xid"], "999")

    def test_load_gallery_template(self) -> None:
        template = weekly_visual_snapshot.load_gallery_template()
        self.assertIn("XMC Weekly Visual UI Gallery", template)
        self.assertIn("__MANIFEST_JSON__", template)


if __name__ == "__main__":
    unittest.main()
