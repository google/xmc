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

"""Extracts and parses merged Pull Requests metadata within a timeframe for release notes drafting."""

import argparse
import json
import re
import shutil
import subprocess
from datetime import datetime, timedelta, timezone
from typing import Any

try:
    from tools import tool_utils
except ImportError:
    import tool_utils


def parse_scope_from_title(title: str) -> str | None:
    """Extracts the conventional commit scope from the PR title if present."""
    match = re.search(r"^[a-z]+(?:\(([^)]+)\))?:", title)
    if match and match.group(1):
        return match.group(1).strip()
    return None


def parse_pr_body_sections(body: str) -> dict[str, str]:
    """Parses markdown headers in PR description into key sections."""
    if not body:
        return {}
    sections: dict[str, str] = {}
    current_section = None
    current_lines: list[str] = []

    for line in body.splitlines():
        header_match = re.match(r"^#{1,3}\s+(.+)$", line.strip())
        if header_match:
            if current_section:
                sections[current_section] = "\n".join(current_lines).strip()
            current_section = header_match.group(1).strip().lower()
            current_lines = []
        elif current_section:
            current_lines.append(line)

    if current_section:
        sections[current_section] = "\n".join(current_lines).strip()
    return sections


def _parse_iso_date(date_str: str, field_name: str) -> datetime:
    """Parses an ISO date string into a UTC datetime."""
    try:
        dt = datetime.fromisoformat(date_str.replace("Z", "+00:00"))
        if dt.tzinfo is None:
            dt = dt.replace(tzinfo=timezone.utc)
        return dt
    except ValueError as e:
        raise tool_utils.HandledError(
            f"Invalid ISO format for {field_name}: {date_str}",
            "Provide a valid ISO date (e.g. YYYY-MM-DD or YYYY-MM-DDTHH:MM:SSZ)",
        ) from e


def _calculate_time_window(
    since_arg: str | None, until_arg: str | None, days_arg: int
) -> tuple[datetime | None, datetime | None]:
    """Computes since and until datetime bounds."""
    now = datetime.now(timezone.utc)
    since_dt = _parse_iso_date(since_arg, "--since") if since_arg else None
    if not since_dt and days_arg:
        since_dt = now - timedelta(days=days_arg)

    until_dt = _parse_iso_date(until_arg, "--until") if until_arg else None
    return since_dt, until_dt


def _build_gh_search_cmd(since_str: str | None, until_str: str | None, limit: int) -> list[str]:
    """Constructs the gh pr list command with appropriate search terms."""
    gh_cmd = [
        "gh",
        "pr",
        "list",
        "--state",
        "merged",
        "--limit",
        str(limit),
        "--json",
        "number,title,mergedAt,author,url,labels,body,files",
    ]
    search_terms = []
    if since_str:
        search_terms.append(f"merged:>={since_str[:10]}")
    if until_str:
        search_terms.append(f"merged:<={until_str[:10]}")
    if search_terms:
        gh_cmd.extend(["--search", " ".join(search_terms)])
    return gh_cmd


def _parse_pr_item(item: dict[str, Any]) -> dict[str, Any]:
    """Extracts structured PR metadata from a single raw GitHub item."""
    title = item.get("title") or ""
    body = item.get("body") or ""
    labels = [
        lbl.get("name")
        for lbl in item.get("labels", [])
        if isinstance(lbl, dict) and lbl.get("name")
    ]
    files = [f.get("path") for f in item.get("files", []) if isinstance(f, dict) and f.get("path")]
    scope = parse_scope_from_title(title)

    return {
        "number": item.get("number"),
        "title": title,
        "scope": scope,
        "author": (item.get("author") or {}).get("login", "unknown"),
        "merged_at": item.get("mergedAt"),
        "url": item.get("url"),
        "labels": labels,
        "files": files,
        "sections": parse_pr_body_sections(body),
        "raw_body": body.strip(),
    }


def _extract_prs_from_gh(
    gh_cmd: list[str], since_dt: datetime | None, until_dt: datetime | None
) -> list[dict[str, Any]]:
    """Runs gh CLI and filters returned PRs by timestamp."""
    res = subprocess.run(gh_cmd, capture_output=True, text=True, check=True)
    raw_data = json.loads(res.stdout)
    prs = []
    for item in raw_data:
        merged_at = item.get("mergedAt")
        if merged_at:
            item_dt = datetime.fromisoformat(merged_at.replace("Z", "+00:00"))
            if since_dt and item_dt < since_dt:
                continue
            if until_dt and item_dt > until_dt:
                continue
        prs.append(_parse_pr_item(item))
    return prs


def extract_prs(args: argparse.Namespace) -> dict[str, Any]:
    """Extracts and parses merged pull requests within the specified timeframe."""
    now = datetime.now(timezone.utc)
    since_dt, until_dt = _calculate_time_window(args.since, args.until, args.days)

    since_str = since_dt.strftime("%Y-%m-%dT%H:%M:%SZ") if since_dt else None
    until_str = until_dt.strftime("%Y-%m-%dT%H:%M:%SZ") if until_dt else None

    if not shutil.which("gh"):
        raise tool_utils.HandledError(
            "gh CLI not found on system PATH",
            "Install GitHub CLI or ensure gh is present in PATH",
        )

    gh_cmd = _build_gh_search_cmd(since_str, until_str, args.limit)
    try:
        prs = _extract_prs_from_gh(gh_cmd, since_dt, until_dt)
    except subprocess.CalledProcessError as e:
        raise tool_utils.HandledError(
            f"GitHub CLI query failed: {e.stderr or e}",
            "Ensure gh CLI is authenticated with gh auth status",
        ) from e

    return {
        "timeframe": {
            "since": since_str,
            "until": until_str or now.strftime("%Y-%m-%dT%H:%M:%SZ"),
            "days_evaluated": args.days if not args.since else None,
        },
        "total_prs": len(prs),
        "prs": prs,
    }


def main():
    parser = argparse.ArgumentParser(
        description="Extracts and parses merged PR metadata for release notes drafting."
    )
    parser.add_argument(
        "--days",
        type=int,
        default=7,
        help="Number of preceding days to extract PRs for (default: 7).",
    )
    parser.add_argument(
        "--since", type=str, default=None, help="Start date/time (YYYY-MM-DD or ISO timestamp)."
    )
    parser.add_argument(
        "--until", type=str, default=None, help="End date/time (YYYY-MM-DD or ISO timestamp)."
    )
    parser.add_argument(
        "--limit",
        type=int,
        default=100,
        help="Maximum number of PRs to inspect (default: 100).",
    )
    tool_utils.run_tool(parser, extract_prs)


if __name__ == "__main__":
    main()
