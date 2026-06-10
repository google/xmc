#!/usr/bin/env python3
# Copyright 2026 Google LLC
# Helper script to retrieve comment details from GitHub API

import sys
import os
import re
import json
import urllib.request
import urllib.parse

def parse_comment_url(url):
    # Regex for review comment: https://github.com/owner/repo/pull/num#discussion_r123456
    match_review = re.search(r'github\.com/([^/]+)/([^/]+)/pull/(\d+)#discussion_r(\d+)', url)
    if match_review:
        return {
            "type": "review_comment",
            "owner": match_review.group(1),
            "repo": match_review.group(2),
            "pull_number": match_review.group(3),
            "comment_id": match_review.group(4)
        }
    
    # Regex for issue comment: https://github.com/owner/repo/pull/num#issuecomment-123456
    match_issue = re.search(r'github\.com/([^/]+)/([^/]+)/pull/(\d+)#issuecomment-(\d+)', url)
    if match_issue:
        return {
            "type": "issue_comment",
            "owner": match_issue.group(1),
            "repo": match_issue.group(2),
            "pull_number": match_issue.group(3),
            "comment_id": match_issue.group(4)
        }
    return None

def fetch_github_api(endpoint, token=None):
    url = f"https://api.github.com{endpoint}"
    req = urllib.request.Request(url)
    req.add_header("Accept", "application/vnd.github.v3+json")
    req.add_header("User-Agent", "XMC-Comment-Fetcher-Agent")
    if token:
        req.add_header("Authorization", f"token {token}")
    
    try:
        with urllib.request.urlopen(req) as response:
            return json.loads(response.read().decode('utf-8'))
    except Exception as e:
        sys.stderr.write(f"Error calling GitHub API {url}: {e}\n")
        sys.exit(1)

def main():
    if len(sys.argv) < 2:
        print("Usage: fetch_pr_comment.py <comment_url>")
        sys.exit(1)
        
    comment_url = sys.argv[1]
    parsed = parse_comment_url(comment_url)
    if not parsed:
        sys.stderr.write("Could not parse comment URL. Ensure it matches Github comment anchor patterns.\n")
        sys.exit(1)
        
    token = os.environ.get("GITHUB_TOKEN")
    
    if parsed["type"] == "review_comment":
        endpoint = f"/repos/{parsed['owner']}/{parsed['repo']}/pulls/comments/{parsed['comment_id']}"
        data = fetch_github_api(endpoint, token)
        
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
        data = fetch_github_api(endpoint, token)
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
