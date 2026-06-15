#!/usr/bin/env python3
# Copyright 2026 Google LLC
# Helper script to retrieve comment details from GitHub API

import sys
import json
from github_utils import parse_comment_url, call_github_api


def main():
    if len(sys.argv) < 2:
        print("Usage: fetch_pr_comment.py <comment_url>")
        sys.exit(1)
        
    comment_url = sys.argv[1]
    parsed = parse_comment_url(comment_url)
    if not parsed:
        sys.stderr.write("Could not parse comment URL. Ensure it matches Github comment anchor patterns.\n")
        sys.exit(1)
        
    if parsed["type"] == "review_comment":
        endpoint = f"/repos/{parsed['owner']}/{parsed['repo']}/pulls/comments/{parsed['comment_id']}"
        data = call_github_api(endpoint, user_agent="XMC-Comment-Fetcher-Agent")
        
        result = {
            "type": "review_comment",
            "comment_id": data.get("id"),
            "author": data.get("user", {}).get("login"),
            "body": data.get("body"),
            "file_path": data.get("path"),
            "line": data.get("line") or data.get("original_line"),
            "diff_hunk": data.get("diff_hunk"),
            "commit_id": data.get("commit_id")
        }
    else:
        endpoint = f"/repos/{parsed['owner']}/{parsed['repo']}/issues/comments/{parsed['comment_id']}"
        data = call_github_api(endpoint, user_agent="XMC-Comment-Fetcher-Agent")
        result = {
            "type": "issue_comment",
            "comment_id": data.get("id"),
            "author": data.get("user", {}).get("login"),
            "body": data.get("body"),
            "file_path": None,
            "line": None,
            "diff_hunk": None,
            "commit_id": None
        }
        
    print(json.dumps(result, indent=2))

if __name__ == '__main__':
    main()
