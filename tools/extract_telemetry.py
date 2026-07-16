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

"""Tool for extracting usage telemetry from GitHub PR comments."""

import argparse
import json
import re

from github_utils import call_github_api
from tool_utils import HandledError, run_tool


def _fetch_comments(owner: str, repo: str, since: str = None):
    """Fetches review comments from the repository via GitHub API."""
    comments = []
    page = 1
    while True:
        endpoint = f"/repos/{owner}/{repo}/pulls/comments?per_page=100&page={page}"
        if since:
            endpoint += f"&since={since}"
        try:
            batch = call_github_api(endpoint, user_agent="XMC-Telemetry-Extractor")
        except Exception as e:
            raise HandledError(
                f"Failed to fetch repository comments: {str(e)}",
                "Ensure the GITHUB_TOKEN environment variable is set and has"
                " appropriate read permissions.",
            )
        if not batch:
            break
        comments.extend(batch)
        if len(batch) < 100:
            break
        page += 1
    return comments


def _parse_telemetry(comments):
    """Parses and formats telemetry records from the raw comment objects."""
    records = []
    pattern = re.compile(r"<!--\s*xmc-telemetry:\s*(\{.*?\})\s*-->")
    for comment in comments:
        body = comment.get("body") or ""
        match = pattern.search(body)
        if not match:
            continue
        try:
            data = json.loads(match.group(1))
            if not isinstance(data, dict):
                continue
            records.append(
                {
                    "date": data.get("date"),
                    "time": data.get("time"),
                    "github_username": data.get("git_username"),
                    "pr_number": data.get("pr_number"),
                    "comment_id": data.get("comment_id"),
                    "skill_name": data.get("skill_name") or "NA",
                    "skill_commit_id": data.get("skill_commit_id") or "NA",
                }
            )
        except (json.JSONDecodeError, KeyError, AttributeError):
            continue
    return records


def extract_telemetry(args):
    """Main orchestrator for extracting telemetry records."""
    comments = _fetch_comments(args.owner, args.repo, args.since)
    records = _parse_telemetry(comments)
    if args.output:
        try:
            with open(args.output, "w") as f:
                json.dump(records, f, indent=2)
            return f"Successfully wrote {len(records)} records to {args.output}"
        except OSError as e:
            raise HandledError(
                f"Failed to write output file: {str(e)}",
                "Ensure the directory path exists and you have write permissions.",
            )
    return records


def main():
    """Main entrypoint for extract_telemetry tool."""
    parser = argparse.ArgumentParser(
        description="Extract usage telemetry from GitHub PR comments."
    )
    parser.add_argument("owner", help="GitHub repository owner (e.g. google).")
    parser.add_argument("repo", help="GitHub repository name (e.g. xmc).")
    parser.add_argument(
        "--since",
        help=(
            "Only show telemetry since this date/time (ISO 8601 format:"
            " YYYY-MM-DDTHH:MM:SSZ)."
        ),
    )
    parser.add_argument(
        "-o",
        "--output",
        help="Path to write the extracted telemetry raw JSON file.",
    )
    run_tool(parser, extract_telemetry)


if __name__ == "__main__":
    main()
