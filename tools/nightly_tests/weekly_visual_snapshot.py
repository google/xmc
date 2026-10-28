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

"""Weekly Visual UI Audit screenshot capturer and static gallery builder."""

from __future__ import annotations

import dataclasses
import datetime
import json
import os
import re
from collections.abc import Mapping, Sequence
from typing import Any

from absl import flags

from tools.nightly_tests.verification import (
    endpoint_resolution,
    ess_session,
    ui_entities_validator,
    verifier_cli,
)

FLAGS = flags.FLAGS

flags.DEFINE_string("output_dir", "/tmp/visual-gallery", "Output directory for gallery")
flags.DEFINE_string("commit_sha", None, "Git commit SHA for manifest metadata")


@dataclasses.dataclass(frozen=True)
class GoldenRoute:
    """Specification for a canonical XMC UI page route to capture and inspect."""

    id: str
    title: str
    route: str
    tab: str | None = None


GOLDEN_ROUTES: tuple[GoldenRoute, ...] = (
    GoldenRoute(
        id="01_experiments_list",
        title="Experiments List Table",
        route="/experiments",
    ),
    GoldenRoute(
        id="03_experiment_workunits",
        title="Experiment Work Units Tab",
        route="/experiments/{xid}",
        tab="Work Units",
    ),
    GoldenRoute(
        id="05_workunit_details",
        title="WorkUnit Details & Status",
        route="/experiments/{xid}/workUnits/1",
    ),
    GoldenRoute(
        id="06_resource_dashboard",
        title="Resource Observability Dashboard (ROD)",
        route="/resources",
    ),
)


def load_gallery_template() -> str:
    """Loads and returns the static HTML gallery template."""
    path = os.path.join(os.path.dirname(__file__), "gallery_template", "index.html")
    with open(path, encoding="utf-8") as f:
        return f.read()


def discover_xid_from_page(page: Any) -> str:
    """Discovers the first experiment XID from the rendered /experiments page DOM."""
    try:
        links = page.locator('a[href*="/experiments/"]')
        for i in range(links.count()):
            href = str(links.nth(i).get_attribute("href") or "")
            if match := re.search(r"/experiments/(\d+)", href):
                return match.group(1)
        rows = page.locator("tbody tr")
        if rows.count() > 0:
            if match := re.search(r"\b(\d+)\b", str(rows.first.inner_text() or "")):
                return match.group(1)
    except Exception:
        return ""
    return ""


def capture_single_route(
    session: ui_entities_validator.XMCUIBrowserSession,
    route_spec: GoldenRoute,
    xid: str | int,
    output_dir: str,
    timeout_ms: int = 60000,
) -> dict[str, Any]:
    """Navigates to a single canonical route and saves a full-page screenshot."""
    resolved_route = route_spec.route.format(xid=xid)
    clean_base_url = str(session.ui_base_url or "").strip().rstrip("/")
    clean_route = resolved_route if resolved_route.startswith("/") else f"/{resolved_route}"
    session.ui_base_url = clean_base_url
    session.page.goto(
        f"{clean_base_url}{clean_route}", wait_until="networkidle", timeout=timeout_ms
    )
    session.settle_and_wait_fonts()
    if route_spec.tab:
        session.switch_tab(route_spec.tab, timeout_ms=timeout_ms)
    filename = f"{route_spec.id}.png"
    screenshot_path = os.path.join(output_dir, "screenshots", filename)
    os.makedirs(os.path.dirname(screenshot_path), exist_ok=True)
    session.page.screenshot(path=screenshot_path, full_page=True)
    return {
        "id": route_spec.id,
        "title": route_spec.title,
        "route": clean_route,
        "filename": filename,
    }


def capture_all_routes(
    session: ui_entities_validator.XMCUIBrowserSession,
    output_dir: str = "/tmp/visual-gallery",
    xid: str | int = "",
    routes: Sequence[GoldenRoute] = GOLDEN_ROUTES,
) -> list[dict[str, Any]]:
    """Captures all configured canonical routes and returns their manifest records."""
    os.makedirs(os.path.join(output_dir, "screenshots"), exist_ok=True)
    resolved_xid = verifier_cli.parse_xid(str(xid) if xid is not None else "", required=False)
    captures: list[dict[str, Any]] = []
    for route in routes:
        if "{xid}" in route.route and not resolved_xid:
            continue
        captures.append(capture_single_route(session, route, resolved_xid, output_dir))
        if route.route == "/experiments" and not resolved_xid:
            resolved_xid = discover_xid_from_page(session.page)
    return captures


def build_manifest(
    ui_base_url: str,
    xid: str | int,
    commit_sha: str,
    captures: Sequence[Mapping[str, Any]],
    generated_at: str | None = None,
) -> dict[str, Any]:
    """Constructs the structured manifest dictionary for the static visual gallery."""
    ts = generated_at or datetime.datetime.now(datetime.UTC).isoformat()
    clean_ui_url = str(ui_base_url or "").strip().rstrip("/")
    return {
        "generated_at": ts,
        "commit_sha": commit_sha or "unknown",
        "ui_base_url": clean_ui_url,
        "xid": str(xid),
        "captures": [dict(c) for c in captures],
    }


def write_gallery_artifacts(output_dir: str, manifest: Mapping[str, Any]) -> tuple[str, str]:
    """Writes manifest.json and self-contained index.html to output_dir."""
    os.makedirs(output_dir, exist_ok=True)
    manifest_path = os.path.join(output_dir, "manifest.json")
    index_path = os.path.join(output_dir, "index.html")
    manifest_json = json.dumps(dict(manifest), indent=2)
    with open(manifest_path, "w", encoding="utf-8") as f_man:
        f_man.write(manifest_json + "\n")
    safe_embedded_json = manifest_json.replace("</", "\\u003c/")
    html_content = load_gallery_template().replace("__MANIFEST_JSON__", safe_embedded_json)
    with open(index_path, "w", encoding="utf-8") as f_html:
        f_html.write(html_content)
    return manifest_path, index_path


def run_visual_snapshot(
    ui_base_url: str,
    xid: str | int = "",
    output_dir: str = "/tmp/visual-gallery",
    commit_sha: str | None = None,
    auth_token: str | None = None,
    headless: bool = True,
) -> dict[str, Any]:
    """Executes the end-to-end weekly visual UI screenshot capture and gallery build."""
    os.environ.setdefault("XMC_USER_EMAIL", "test@example.com")
    clean_ui_url = str(ui_base_url or "").strip().rstrip("/")
    resolved_sha = commit_sha or os.environ.get("GITHUB_SHA", "local")
    clean_xid = verifier_cli.parse_xid(str(xid) if xid is not None else "", required=False)
    with ui_entities_validator.XMCUIBrowserSession(
        ui_base_url=clean_ui_url,
        auth_token=auth_token,
        headless=headless,
        screenshots_dir=os.path.join(output_dir, "screenshots"),
    ) as session:
        captures = capture_all_routes(session, output_dir=output_dir, xid=clean_xid)
    resolved_xid = clean_xid or next(
        (
            m.group(1)
            for cap in captures
            if (m := re.search(r"/experiments/(\d+)", str(cap.get("route", ""))))
        ),
        "",
    )
    manifest = build_manifest(clean_ui_url, resolved_xid, resolved_sha, captures)
    write_gallery_artifacts(output_dir, manifest)
    return manifest


def main(argv: Sequence[str]) -> None:
    """CLI entrypoint for weekly_visual_snapshot."""
    verifier_cli.validate_argv(argv)
    raw_ui_url = (
        verifier_cli.get_flag(FLAGS, "ui_base_url") or endpoint_resolution.resolve_ui_base_url()
    )
    clean_ui_url = str(raw_ui_url or "").strip().rstrip("/")
    with ess_session.handle_verification_errors():
        run_visual_snapshot(
            ui_base_url=clean_ui_url,
            xid=verifier_cli.parse_xid(verifier_cli.get_flag(FLAGS, "xid"), required=False),
            output_dir=verifier_cli.get_flag(FLAGS, "output_dir", "/tmp/visual-gallery"),
            commit_sha=verifier_cli.get_flag(FLAGS, "commit_sha"),
            auth_token=verifier_cli.get_flag(FLAGS, "auth_token"),
            headless=verifier_cli.get_flag(FLAGS, "headless", True),
        )


if __name__ == "__main__":
    verifier_cli.run_main(main)
