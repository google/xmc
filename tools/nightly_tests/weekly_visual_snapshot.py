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

import contextlib
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
        route="/experiments/{xid}/workUnits/{wid}",
    ),
    GoldenRoute(
        id="06_resource_dashboard",
        title="Resource Observability Dashboard (ROD)",
        route="/resources",
    ),
)

# ui-contract: experiments_list_page.ng.html uses data-test-id="next-page-button" on mat-icon-button.
_EXPERIMENTS_NEXT_PAGE_SEL = 'button[data-test-id="next-page-button"]:not([disabled])'
# ui-contract: experiments_table.ng.html marks expandable status sub-rows with .detail-row.
_EXPERIMENTS_ROW_SEL = "tbody tr:not(.detail-row)"
# ui-contract: work_units_status_bar.ng.html renders .no-work-units when an experiment has 0 work units.
_NO_WORK_UNITS_BADGE_SEL = ".no-work-units"
# ui-contract: experiment_page.ng.html renders the experiment title as h1.experiment-title.
_EXPERIMENT_TITLE_SEL = "h1.experiment-title, .experiment-title"
# ui-contract: experiment_page.ng.html renders h1.experiment-title; errors surface in mat-snack-bar-container.
_EXPERIMENT_LOAD_SEL = "h1.experiment-title, .experiment-title, mat-snack-bar-container"
# ui-contract: experiment_page.ng.html renders mat-tab labels (e.g. "Work Units").
_WORKUNITS_TAB_SEL = (
    'mat-tab-header .mdc-tab:has-text("Work Units"), .mat-tab-label:has-text("Work Units")'
)
_WORKUNIT_LINK_SEL = 'xmc-work-units-table a[href*="/workUnits/"]'
# ui-contract: work_units_table.ng.html renders .no-work-units-message and .error-message on empty/error.
_WORKUNIT_TABLE_READY_SEL = (
    'xmc-work-units-table a[href*="/workUnits/"], '
    "xmc-work-units-table .no-work-units-message, "
    "xmc-work-units-table .error-message"
)
_WORKUNIT_DETAILS_SEL = "xmc-work-unit-details, xmc-work-unit-header"
# ui-contract: experiments_table.ng.html renders .no-experiments-message when empty.
_EXPERIMENTS_LIST_READY_SEL = "xmc-experiments-table tbody tr, .no-experiments-message"
_ROUTE_READY_SELECTORS: Mapping[str, str] = {
    "01_experiments_list": _EXPERIMENTS_LIST_READY_SEL,
    "03_experiment_workunits": _WORKUNIT_TABLE_READY_SEL,
    "05_workunit_details": _WORKUNIT_DETAILS_SEL,
}


def load_gallery_template() -> str:
    """Loads and returns the static HTML gallery template."""
    path = os.path.join(os.path.dirname(__file__), "gallery_template", "index.html")
    with open(path, encoding="utf-8") as f:
        return f.read()


def _locator_count(locator: Any) -> int:
    """Returns an integer element count from a Playwright Locator or mock."""
    try:
        count = locator.count()
        return count if isinstance(count, int) else 0
    except Exception:
        return 0


def _safe_inner_text(locator: Any) -> str:
    """Safely extracts inner_text as a string from a Playwright Locator or mock."""
    try:
        text = locator.inner_text()
        return text if isinstance(text, str) else ""
    except Exception:
        return ""


def _safe_get_attribute(locator: Any, attr: str) -> str:
    """Safely extracts an attribute string from a Playwright Locator or mock."""
    try:
        val = locator.get_attribute(attr)
        return val if isinstance(val, str) else ""
    except Exception:
        return ""


def _wait_for_selector_best_effort(page: Any, selector: str, timeout_ms: int) -> None:
    """Waits for a selector without aborting if the page is in an empty or error state."""
    with contextlib.suppress(Exception):
        page.wait_for_selector(selector, timeout=timeout_ms)


def _extract_xid_from_row(row: Any) -> str:
    """Extracts an experiment XID from a table row link or cell text."""
    links = row.locator('a[href*="/experiments/"]')
    for i in range(_locator_count(links)):
        href = _safe_get_attribute(links.nth(i), "href")
        if match := re.search(r"/experiments/(\d+)", href):
            return match.group(1)
    if match := re.search(r"\b(\d+)\b", _safe_inner_text(row)):
        return match.group(1)
    return ""


def discover_candidate_xids_from_page(page: Any) -> list[str]:
    """Discovers ordered experiment XIDs on /experiments that do not have zero work units."""
    candidates: list[str] = []
    seen: set[str] = set()
    try:
        rows = page.locator(_EXPERIMENTS_ROW_SEL)
        row_count = _locator_count(rows)
        for i in range(row_count):
            row = rows.nth(i)
            if _locator_count(row.locator(_NO_WORK_UNITS_BADGE_SEL)) > 0:
                continue
            xid = _extract_xid_from_row(row)
            if xid and xid not in seen:
                seen.add(xid)
                candidates.append(xid)
        if row_count > 0:
            return candidates
        links = page.locator('a[href*="/experiments/"]')
        for i in range(_locator_count(links)):
            href = _safe_get_attribute(links.nth(i), "href")
            if (match := re.search(r"/experiments/(\d+)", href)) and match.group(1) not in seen:
                seen.add(match.group(1))
                candidates.append(match.group(1))
    except Exception:
        return []
    return candidates


def _extract_workunit_id_from_href(href: str, xid: str | int = "") -> str:
    """Extracts a work unit ID from a link href, optionally enforcing matching XID."""
    norm_xid = str(xid).strip()
    if exp_match := re.search(r"/experiments/(\d+)/workUnits/([^/?#]+)", href):
        if norm_xid and exp_match.group(1) != norm_xid:
            return ""
        return exp_match.group(2)
    if wu_match := re.search(r"(?:^|/)workUnits/([^/?#]+)", href):
        return wu_match.group(1)
    return ""


def discover_workunit_ids_from_page(page: Any, xid: str | int = "") -> list[str]:
    """Discovers ordered, unique work unit IDs from the rendered Work Units table."""
    wids: list[str] = []
    seen: set[str] = set()
    try:
        links = page.locator(_WORKUNIT_LINK_SEL)
        if _locator_count(links) == 0:
            links = page.locator('a[href*="/workUnits/"]')
        for i in range(_locator_count(links)):
            href = _safe_get_attribute(links.nth(i), "href")
            wid = _extract_workunit_id_from_href(href, xid=xid) if href else ""
            if wid and wid not in seen:
                seen.add(wid)
                wids.append(wid)
    except Exception:
        return []
    return wids


def discover_workunit_id_from_page(page: Any, xid: str | int = "") -> str:
    """Discovers the first valid work unit ID from the rendered Work Units table."""
    wids = discover_workunit_ids_from_page(page, xid=xid)
    return wids[0] if wids else ""


def _navigate_to_path(
    session: ui_entities_validator.XMCUIBrowserSession,
    path: str,
    timeout_ms: int = 60000,
) -> str:
    """Normalizes ui_base_url and navigates to the target path."""
    clean_base_url = str(session.ui_base_url or "").strip().rstrip("/")
    clean_path = path if path.startswith("/") else f"/{path}"
    session.ui_base_url = clean_base_url
    session.page.goto(f"{clean_base_url}{clean_path}", wait_until="networkidle", timeout=timeout_ms)
    session.settle_and_wait_fonts()
    return clean_path


def _verify_workunit_details_loads(
    session: ui_entities_validator.XMCUIBrowserSession,
    xid: str | int,
    wid: str,
    timeout_ms: int = 60000,
) -> bool:
    """Returns True when /experiments/{xid}/workUnits/{wid} renders WorkUnit details."""
    try:
        _navigate_to_path(session, f"/experiments/{xid}/workUnits/{wid}", timeout_ms=timeout_ms)
        if _locator_count(session.page.locator(_WORKUNIT_DETAILS_SEL)) == 0:
            _wait_for_selector_best_effort(
                session.page, _WORKUNIT_DETAILS_SEL, timeout_ms=min(timeout_ms, 5000)
            )
        return _locator_count(session.page.locator(_WORKUNIT_DETAILS_SEL)) > 0
    except Exception:
        return False


def probe_experiment_workunit(
    session: ui_entities_validator.XMCUIBrowserSession,
    xid: str | int,
    timeout_ms: int = 60000,
) -> str:
    """Verifies an experiment has accessible work units and returns a valid workUnit ID."""
    if not str(xid or "").strip():
        return ""
    try:
        _navigate_to_path(session, f"/experiments/{xid}", timeout_ms=timeout_ms)
        _wait_for_selector_best_effort(
            session.page, _EXPERIMENT_LOAD_SEL, timeout_ms=min(timeout_ms, 5000)
        )
        if _locator_count(session.page.locator(_EXPERIMENT_TITLE_SEL)) == 0:
            return ""
        if _locator_count(session.page.locator(_WORKUNITS_TAB_SEL)) > 0:
            session.switch_tab("Work Units", timeout_ms=min(timeout_ms, 15000))
        _wait_for_selector_best_effort(
            session.page, _WORKUNIT_TABLE_READY_SEL, timeout_ms=min(timeout_ms, 10000)
        )
        for wid in discover_workunit_ids_from_page(session.page, xid=xid):
            if _verify_workunit_details_loads(session, xid, wid, timeout_ms=timeout_ms):
                return wid
    except Exception:
        return ""
    return ""


def _advance_experiments_page(
    session: ui_entities_validator.XMCUIBrowserSession,
    page_index: int,
    timeout_ms: int = 60000,
) -> bool:
    """Navigates to the requested zero-based page index on /experiments."""
    _navigate_to_path(session, "/experiments", timeout_ms=timeout_ms)
    _wait_for_selector_best_effort(
        session.page,
        _ROUTE_READY_SELECTORS["01_experiments_list"],
        timeout_ms=min(timeout_ms, 10000),
    )
    for _ in range(page_index):
        next_btn = session.page.locator(_EXPERIMENTS_NEXT_PAGE_SEL)
        if _locator_count(next_btn) == 0:
            return False
        next_btn.first.click()
        with contextlib.suppress(Exception):
            session.page.wait_for_load_state("networkidle", timeout=timeout_ms)
        session.settle_and_wait_fonts()
        _wait_for_selector_best_effort(
            session.page,
            _ROUTE_READY_SELECTORS["01_experiments_list"],
            timeout_ms=min(timeout_ms, 10000),
        )
    return True


def discover_experiment_and_workunit(
    session: ui_entities_validator.XMCUIBrowserSession,
    timeout_ms: int = 60000,
    max_pages: int = 5,
) -> tuple[str, str]:
    """Finds the first accessible experiment with non-empty work units and a valid workUnit ID."""
    for page_index in range(max_pages):
        if page_index > 0 and not _advance_experiments_page(
            session, page_index, timeout_ms=timeout_ms
        ):
            break
        for candidate_xid in discover_candidate_xids_from_page(session.page):
            if wid := probe_experiment_workunit(session, candidate_xid, timeout_ms=timeout_ms):
                return candidate_xid, wid
    return "", ""


def capture_single_route(
    session: ui_entities_validator.XMCUIBrowserSession,
    route_spec: GoldenRoute,
    xid: str | int,
    output_dir: str,
    timeout_ms: int = 60000,
    wid: str | int = "",
) -> dict[str, Any]:
    """Navigates to a single canonical route and saves a full-page screenshot."""
    resolved_route = route_spec.route.format(xid=xid, wid=wid)
    clean_route = _navigate_to_path(session, resolved_route, timeout_ms=timeout_ms)
    if route_spec.tab:
        session.switch_tab(route_spec.tab, timeout_ms=timeout_ms)
    if ready_sel := _ROUTE_READY_SELECTORS.get(route_spec.id):
        _wait_for_selector_best_effort(session.page, ready_sel, timeout_ms=min(timeout_ms, 10000))
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
    resolved_wid = ""
    checked_workunits_for_xid = False
    captures: list[dict[str, Any]] = []
    for route in routes:
        if "{xid}" in route.route and not resolved_xid:
            continue
        if "{wid}" in route.route and not resolved_wid:
            if not checked_workunits_for_xid:
                resolved_wid = probe_experiment_workunit(session, resolved_xid)
                checked_workunits_for_xid = True
            if not resolved_wid:
                continue
        captures.append(
            capture_single_route(session, route, resolved_xid, output_dir, wid=resolved_wid)
        )
        if route.route == "/experiments" and not resolved_xid:
            resolved_xid, resolved_wid = discover_experiment_and_workunit(session)
        elif "{xid}" in route.route and "{wid}" not in route.route and not resolved_wid:
            resolved_wid = discover_workunit_id_from_page(session.page, xid=resolved_xid)
            checked_workunits_for_xid = True
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
