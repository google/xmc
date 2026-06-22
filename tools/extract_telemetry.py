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

import argparse
import json
import re
from github_utils import call_github_api
from tool_utils import run_tool, HandledError

# A mapping of public GitHub usernames to internal Google LDAPs.
GITHUB_TO_LDAP_MAP = {
    "atyburcy": "atyburcy",
}

def _fetch_comments(owner: str, repo: str, since: str = None):
    """Fetches review comments from the repository via GitHub API."""
    endpoint = f"/repos/{owner}/{repo}/pulls/comments?per_page=100"
    if since:
        endpoint += f"&since={since}"
    try:
        return call_github_api(endpoint, user_agent="XMC-Telemetry-Extractor")
    except Exception as e:
        raise HandledError(
            f"Failed to fetch repository comments: {str(e)}",
            "Ensure the GITHUB_TOKEN environment variable is set and has appropriate read permissions."
        )

def _parse_telemetry(comments):
    """Parses and formats telemetry records from the raw comment objects."""
    records = []
    pattern = re.compile(r'<!--\s*xmc-telemetry:\s*(\{.*?\})\s*-->')
    for comment in comments:
        body = comment.get("body", "")
        match = pattern.search(body)
        if not match:
            continue
        try:
            data = json.loads(match.group(1))
            github_username = data.get("git_username")
            ldap = GITHUB_TO_LDAP_MAP.get(github_username, "external/unmapped")
            records.append({
                "date": data.get("date"),
                "time": data.get("time"),
                "github_username": github_username,
                "google_ldap": ldap,
                "pr_number": data.get("pr_number"),
                "comment_id": data.get("comment_id")
            })
        except (json.JSONDecodeError, KeyError):
            continue
    return records

def extract_telemetry(args):
    """Main orchestrator for extracting telemetry records."""
    comments = _fetch_comments(args.owner, args.repo, args.since)
    return _parse_telemetry(comments)

def main():
    parser = argparse.ArgumentParser(
        description="Extract usage telemetry from GitHub PR comments."
    )
    parser.add_argument("owner", help="GitHub repository owner (e.g. google).")
    parser.add_argument("repo", help="GitHub repository name (e.g. xmc).")
    parser.add_argument("--since", help="Only show telemetry since this date/time (ISO 8601 format: YYYY-MM-DDTHH:MM:SSZ).")
    run_tool(parser, extract_telemetry)

if __name__ == '__main__':
    main()
