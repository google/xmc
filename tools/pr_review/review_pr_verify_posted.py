#!/usr/bin/env python3
# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Helper CLI script to verify that 100% of approved PR review comments were posted inline to GitHub."""

import argparse
import json
import re
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import Any

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.common.tool_utils import HandledError, parse_positive_int, run_tool  # noqa: E402
from tools.pr_review.github_utils import (  # noqa: E402
    GithubAPIError,
    GithubAuthError,
    get_authenticated_login,
    list_all_pages,
    list_review_threads,
)

_USER_AGENT = "XMC-PR-Review-Agent"

# Item statuses that mark a checklist item as approved for posting.
_APPROVED_STATUS = re.compile(r"\b(approved|confirmed|posted)\b|\[x\]", re.IGNORECASE)

# Telemetry marker that review_pr_post_inline_comment.py appends to every comment.
_TELEMETRY_MARKER = re.compile(r"<!--\s*xmc-telemetry:.*?-->", re.DOTALL)


@dataclass(frozen=True)
class _LiveState:
    """Live GitHub state of a PR that checklist items are verified against."""

    login: str
    comments: list[dict[str, Any]]
    resolved_comment_ids: frozenset[int]


def _parse_table_column_indices(headers: list[str]) -> dict[str, int]:
    """Resolves header column indices for dynamic markdown table parsing."""
    return {
        "id": next(
            (idx for idx, h in enumerate(headers) if h in ("item", "item id", "id", "#")),
            -1,
        ),
        "status": next((idx for idx, h in enumerate(headers) if "status" in h), -1),
        "path": next(
            (
                idx
                for idx, h in enumerate(headers)
                if any(k in h for k in ("file", "path", "target"))
            ),
            -1,
        ),
        "line": next((idx for idx, h in enumerate(headers) if "line" in h), -1),
        "rule": next((idx for idx, h in enumerate(headers) if "rule" in h), -1),
        "proposed": next(
            (
                idx
                for idx, h in enumerate(headers)
                if any(k in h for k in ("proposed", "comment", "summary", "description"))
            ),
            -1,
        ),
    }


def _parse_markdown_table_items(lines: list[str]) -> list[dict[str, Any]]:
    """Extracts approved review items from dynamic markdown tables."""
    items: list[dict[str, Any]] = []
    i = 0
    while i < len(lines):
        line = lines[i]
        is_table_header = (
            line.startswith("|")
            and line.endswith("|")
            and i + 1 < len(lines)
            and re.match(r"^\|[\s\-:|]+\|$", lines[i + 1])
        )
        if is_table_header:
            headers = [col.strip().lower() for col in line.strip("|").split("|")]
            cols = _parse_table_column_indices(headers)
            i += 2
            while i < len(lines) and lines[i].startswith("|") and lines[i].endswith("|"):
                row = [c.strip() for c in lines[i].strip("|").split("|")]
                if cols["status"] != -1 and cols["status"] < len(row):
                    raw_status = row[cols["status"]]
                    if _APPROVED_STATUS.search(raw_status):
                        raw_id = (
                            row[cols["id"]]
                            if cols["id"] != -1 and cols["id"] < len(row)
                            else f"#{len(items) + 1}"
                        )
                        raw_path = (
                            row[cols["path"]]
                            if cols["path"] != -1 and cols["path"] < len(row)
                            else ""
                        )
                        raw_line = (
                            row[cols["line"]]
                            if cols["line"] != -1 and cols["line"] < len(row)
                            else "0"
                        )
                        raw_rule = (
                            row[cols["rule"]]
                            if cols["rule"] != -1 and cols["rule"] < len(row)
                            else ""
                        )
                        raw_proposed = (
                            row[cols["proposed"]]
                            if cols["proposed"] != -1 and cols["proposed"] < len(row)
                            else ""
                        )

                        clean_path = re.sub(r"[`*]", "", raw_path).strip()
                        line_match = re.search(r"\d+", raw_line)
                        clean_line = int(line_match.group(0)) if line_match else 0
                        if clean_path and clean_line > 0:
                            items.append(
                                {
                                    "type": "initial_inline",
                                    "item_id": raw_id.strip(),
                                    "status": raw_status.strip(),
                                    "file_path": clean_path,
                                    "line": clean_line,
                                    "rule": raw_rule.strip(),
                                    "proposed": raw_proposed.strip(),
                                }
                            )
                i += 1
        else:
            i += 1
    return items


def _parse_re_review_thread_items(content: str) -> list[dict[str, Any]]:
    """Extracts re-review thread blocks with recommended actions."""
    items: list[dict[str, Any]] = []
    thread_pattern = re.compile(
        r"###\s+Thread ID\s+`(\d+)`\s+\(`([^`]+)`:L(\d+)\)(?:(?!###|---).)*?Recommended Action[^`:]*[:`]*\s*`?\[(RESOLVE|RESPOND)[^`\]]*\]`?",
        re.DOTALL,
    )
    for match in thread_pattern.finditer(content):
        thread_id, file_path, line, action = match.groups()
        items.append(
            {
                "type": "re_review_thread",
                "thread_id": int(thread_id),
                "file_path": file_path.strip(),
                "line": int(line),
                "action": action.upper(),
            }
        )
    return items


def parse_checklist_approved_items(content: str) -> list[dict[str, Any]]:
    """Parse approved comment or resolution items from the markdown audit artifact."""
    lines = [line.strip() for line in content.splitlines()]
    items = _parse_markdown_table_items(lines)

    # Fallback to simple regex if dynamic table parsing did not find any items
    if not items:
        table_pattern = re.compile(
            r"\|\s*([^\s|]+)\s*\|\s*(Approved|Confirmed|Posted)\s*\|\s*`?([^`|]+)`?\s*\|\s*`?(\d+)`?\s*\|\s*([^|]+)\s*\|\s*([^|]+)\s*\|",
            re.IGNORECASE,
        )
        for match in table_pattern.finditer(content):
            item_id, status, file_path, line, rule, proposed = match.groups()
            items.append(
                {
                    "type": "initial_inline",
                    "item_id": item_id.strip(),
                    "status": status.strip(),
                    "file_path": file_path.strip(),
                    "line": int(line.strip()),
                    "rule": rule.strip(),
                    "proposed": proposed.strip(),
                }
            )

    items.extend(_parse_re_review_thread_items(content))
    return items


def _item_key(item: dict[str, Any]) -> str:
    """Returns the normalized ID of a checklist item (`#1` and `1` are equal)."""
    return _normalize_id(item.get("item_id") or item.get("thread_id"))


def _normalize_id(raw_id: Any) -> str:
    """Normalizes an item ID for comparison by dropping whitespace and a leading `#`."""
    return str(raw_id).strip().lstrip("#").strip()


def load_sidecar(sidecar_path: Path) -> dict[str, Any] | None:
    """Loads the JSON sidecar written by review_pr_generate_audit.py.

    Args:
        sidecar_path: Path of the `.json` sidecar.

    Returns:
        The sidecar document, or None if the file does not exist.

    Raises:
        HandledError: If the sidecar is not valid JSON or has no `items` list.
    """
    if not sidecar_path.is_file():
        return None
    try:
        sidecar = json.loads(sidecar_path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as e:
        raise HandledError(
            f"Audit sidecar '{sidecar_path}' is not valid JSON: {e}",
            "Regenerate it with review_pr_generate_audit.py or fix the JSON syntax.",
        ) from e
    if not isinstance(sidecar, dict) or not isinstance(sidecar.get("items"), list):
        raise HandledError(
            f"Audit sidecar '{sidecar_path}' has no 'items' list.",
            "Regenerate it with review_pr_generate_audit.py.",
        )
    return sidecar


def approved_sidecar_items(sidecar: dict[str, Any]) -> list[dict[str, Any]]:
    """Returns the actionable sidecar items in the shape of the markdown parsers.

    Initial inline items count when their status is Approved, Confirmed, or
    Posted and they target a file line; re-review threads count when their
    action is RESOLVE or RESPOND. Integer strings are accepted for `line` and
    `thread_id`; items with malformed values are skipped.
    """
    items: list[dict[str, Any]] = []
    for item in sidecar["items"]:
        if not isinstance(item, dict):
            continue
        status = str(item.get("status") or "")
        if item.get("type") == "initial_inline":
            line = parse_positive_int(item.get("line"))
            if _APPROVED_STATUS.search(status) and item.get("file_path") and line:
                items.append({**item, "item_id": str(item.get("item_id")), "line": line})
        elif item.get("type") == "re_review_thread":
            action = str(item.get("action") or "").upper()
            thread_id = parse_positive_int(item.get("thread_id"))
            if action in ("RESOLVE", "RESPOND") and thread_id:
                items.append({**item, "thread_id": thread_id, "action": action})
    return items


def _normalize_body(text: str) -> str:
    """Normalizes comment text for matching.

    Drops the telemetry marker and markdown backticks/asterisks, collapses
    whitespace, and casefolds.
    """
    without_marker = _TELEMETRY_MARKER.sub("", text)
    return " ".join(re.sub(r"[`*]", "", without_marker).split()).casefold()


def _is_initial_inline_posted(item: dict[str, Any], live: _LiveState) -> bool:
    """Checks whether the authenticated user posted the item's comment on its file line.

    A live comment matches when it targets the item's path and line, is authored
    by the authenticated user, and its normalized body contains the item's
    normalized `proposed` text (any body matches when `proposed` is empty).
    """
    proposed = _normalize_body(str(item.get("proposed") or ""))
    for lc in live.comments:
        lc_line = lc.get("line") or lc.get("original_line")
        if lc.get("path") != item["file_path"] or str(lc_line) != str(item["line"]):
            continue
        if (lc.get("user") or {}).get("login") != live.login:
            continue
        if proposed in _normalize_body(str(lc.get("body") or "")):
            return True
    return False


def _is_re_review_thread_posted(
    item: dict[str, Any],
    live: _LiveState,
) -> tuple[bool, str | None]:
    """Verifies that a re-review thread action (RESPOND or RESOLVE) is fulfilled.

    RESPOND requires a live reply to the thread's root comment. RESOLVE requires
    GitHub to report the thread resolved; a reply or a local `Posted` / `[x]`
    status is not sufficient.
    """
    thread_id = item["thread_id"]
    if item["action"] == "RESPOND":
        replies = [lc for lc in live.comments if lc.get("in_reply_to_id") == thread_id]
        if replies:
            return True, None
        return False, f"Recommended response on Thread ID `{thread_id}` has not been posted"

    if item["action"] == "RESOLVE":
        if thread_id in live.resolved_comment_ids:
            return True, None
        return (
            False,
            f"RESOLVE action on Thread ID `{thread_id}` has not been verified (the review thread is not resolved on GitHub)",
        )

    return False, f"Unknown action '{item.get('action')}' on Thread ID `{thread_id}`"


def _verify_all_checklist_items(
    items: list[dict[str, Any]],
    live: _LiveState,
) -> tuple[list[dict[str, Any]], list[str]]:
    """Verifies each checklist item against the live GitHub state.

    Returns:
        The verified items and a description of every unposted item.
    """
    unposted: list[str] = []
    verified: list[dict[str, Any]] = []
    for item in items:
        if item["type"] == "initial_inline":
            if _is_initial_inline_posted(item, live):
                verified.append(item)
            else:
                unposted.append(
                    f"Approved comment '{item['item_id']}' on {item['file_path']}:L{item['line']} missing from live PR diff"
                    f" (no comment by @{live.login} there contains the proposed text)"
                )
        elif item["type"] == "re_review_thread":
            ok, err = _is_re_review_thread_posted(item, live)
            if ok:
                verified.append(item)
            elif err:
                unposted.append(err)
    return verified, unposted


def _mark_items_as_posted(content: str, item_ids: set[str]) -> str:
    """Rewrites the status of the given table items from Approved/Confirmed to Posted."""
    updated_content = content
    for item_id in item_ids:
        pattern = re.compile(
            rf"(\|\s*#?{re.escape(item_id)}\s*\|\s*)(Approved|Confirmed)(\s*\|)",
            re.IGNORECASE,
        )
        updated_content = pattern.sub(r"\1Posted\3", updated_content)
    return updated_content


class _Checklist:
    """An audit checklist: its markdown artifact and optional JSON sidecar."""

    def __init__(self, checklist_file: str):
        path = Path(checklist_file)
        if not path.is_file():
            raise HandledError(
                f"Audit checklist file '{checklist_file}' does not exist.",
                "Ensure the checklist artifact file path is valid and present in the conversation artifact directory.",
            )
        is_sidecar = path.suffix == ".json"
        self.path = path
        self.sidecar_path = path if is_sidecar else path.with_suffix(".json")
        self.sidecar = load_sidecar(self.sidecar_path)
        # Given the sidecar directly, still use its sibling markdown if present.
        markdown_path = path.with_suffix(".md") if is_sidecar else path
        self.markdown_path = markdown_path if markdown_path.is_file() else None
        self.content = self.markdown_path.read_text(encoding="utf-8") if self.markdown_path else ""

    def approved_items(self) -> list[dict[str, Any]]:
        """Returns approved items from the sidecar, else parsed from the markdown."""
        if self.sidecar is not None:
            return approved_sidecar_items(self.sidecar)
        return parse_checklist_approved_items(self.content)

    def record_posted(self, item_ids: set[str]) -> None:
        """Sets the status of the given items to Posted in the sidecar and markdown."""
        if self.sidecar is not None:
            for item in self.sidecar["items"]:
                if isinstance(item, dict) and _item_key(item) in item_ids:
                    item["status"] = "Posted"
            self.sidecar_path.write_text(
                json.dumps(self.sidecar, indent=2) + "\n", encoding="utf-8"
            )
        if self.markdown_path is not None:
            updated = _mark_items_as_posted(self.content, item_ids)
            self.markdown_path.write_text(updated, encoding="utf-8")


def _require_items(checklist: _Checklist) -> list[dict[str, Any]]:
    """Returns the approved checklist items, failing loudly when there are none."""
    items = checklist.approved_items()
    if items:
        return items
    source = "sidecar" if checklist.sidecar is not None else "markdown"
    source_path = checklist.sidecar_path if checklist.sidecar is not None else checklist.path
    raise HandledError(
        f"No approved items found in the audit checklist {source} '{source_path}'.",
        "Record approved items in the JSON sidecar written by review_pr_generate_audit.py"
        " ('initial_inline' items with status 'Approved', or 're_review_thread' items with"
        " action 'RESOLVE'/'RESPOND'). The markdown fallback only reads status tables and"
        " '### Thread ID' blocks.",
    )


def _mark_posted(checklist: _Checklist, raw_ids: list[str]) -> dict[str, Any]:
    """Records the given approved items as Posted without contacting GitHub."""
    known = {_item_key(item) for item in _require_items(checklist)}
    requested = {_normalize_id(raw_id) for raw_id in raw_ids}
    unknown = sorted(requested - known)
    if unknown:
        raise HandledError(
            f"Unknown or unapproved checklist item(s): {', '.join(unknown)}.",
            f"Pass IDs of approved items. Approved item IDs: {', '.join(sorted(known))}.",
        )
    checklist.record_posted(requested)
    return {
        "status": "success",
        "checklist_file": str(checklist.path.resolve()),
        "marked_posted": sorted(requested),
    }


def _fetch_resolved_comment_ids(
    args: argparse.Namespace, items: list[dict[str, Any]]
) -> frozenset[int]:
    """Returns the database IDs of comments in resolved review threads.

    Queries GitHub's review threads only when an item has a RESOLVE action.
    """
    if not any(item.get("action") == "RESOLVE" for item in items):
        return frozenset()
    threads = list_review_threads(args.owner, args.repo, args.pr_number, user_agent=_USER_AGENT)
    return frozenset(
        comment["databaseId"]
        for thread in threads
        if thread.get("isResolved")
        for comment in (thread.get("comments") or {}).get("nodes") or []
        if comment and comment.get("databaseId")
    )


def _fetch_live_state(args: argparse.Namespace, items: list[dict[str, Any]]) -> _LiveState:
    """Fetches the authenticated login, every live review comment, and resolved threads."""
    try:
        return _LiveState(
            login=get_authenticated_login(user_agent=_USER_AGENT),
            comments=list_all_pages(
                f"/repos/{args.owner}/{args.repo}/pulls/{args.pr_number}/comments",
                user_agent=_USER_AGENT,
            ),
            resolved_comment_ids=_fetch_resolved_comment_ids(args, items),
        )
    except (GithubAuthError, GithubAPIError) as e:
        raise HandledError(
            f"Failed to fetch live GitHub review state for PR #{args.pr_number}: {e}",
            "Check GitHub API connectivity and authentication credentials.",
        ) from e


def verify_review_comments_posted(args: argparse.Namespace) -> dict[str, Any]:
    """Verify approved audit checklist items against actual posted inline GitHub comments.

    Reads items from the JSON sidecar next to the checklist (markdown fallback),
    fails when no approved items are found, and records verified items as
    Posted. With `--mark-posted`, only records the given items as Posted.
    """
    checklist = _Checklist(args.checklist_file)
    mark_posted_ids = getattr(args, "mark_posted", None)
    if isinstance(mark_posted_ids, list) and mark_posted_ids:
        return _mark_posted(checklist, mark_posted_ids)

    items = _require_items(checklist)
    live = _fetch_live_state(args, items)
    verified, unposted = _verify_all_checklist_items(items, live)
    checklist.record_posted({_item_key(item) for item in verified})

    if unposted:
        raise HandledError(
            f"Review Comment Verification Failed: {len(unposted)} approved item(s) were not found on GitHub.",
            "Unposted items:\n"
            + "\n".join(f"- {u}" for u in unposted)
            + "\n\nExecute review_pr_post_inline_comment.py for missing items before finalizing review.",
        )

    return {
        "status": "success",
        "owner": args.owner,
        "repo": args.repo,
        "pr_number": args.pr_number,
        "checklist_file": str(checklist.path.resolve()),
        "items_source": "sidecar" if checklist.sidecar is not None else "markdown",
        "total_checklist_items": len(items),
        "posted_items": len(verified),
        "unposted_items": [],
    }


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Verify programmatically that 100% of approved PR review comments were posted inline to GitHub."
    )
    parser.add_argument("owner", help="GitHub repository owner (e.g. google).")
    parser.add_argument("repo", help="GitHub repository name (e.g. xmc).")
    parser.add_argument("pr_number", type=int, help="GitHub Pull Request number.")
    parser.add_argument(
        "-c",
        "--checklist-file",
        required=True,
        help=(
            "Path to the audit checklist markdown artifact (its '.json' sidecar is read when"
            " present) or to the sidecar itself."
        ),
    )
    parser.add_argument(
        "--mark-posted",
        nargs="+",
        metavar="ITEM_ID",
        help=(
            "Record the given approved item IDs (e.g. '#1' or a thread ID) as Posted in the"
            " sidecar and markdown, without contacting GitHub."
        ),
    )
    return parser


if __name__ == "__main__":
    run_tool(build_parser(), verify_review_comments_posted)
