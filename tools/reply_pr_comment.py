#!/usr/bin/env python3
# Copyright 2026 Google LLC
# Helper script to post a reply to a PR comment on GitHub

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
            "comment_id": int(match_review.group(4))
        }
    
    # Regex for issue comment: https://github.com/owner/repo/pull/num#issuecomment-123456
    match_issue = re.search(r'github\.com/([^/]+)/([^/]+)/pull/(\d+)#issuecomment-(\d+)', url)
    if match_issue:
        return {
            "type": "issue_comment",
            "owner": match_issue.group(1),
            "repo": match_issue.group(2),
            "pull_number": match_issue.group(3),
            "comment_id": int(match_issue.group(4))
        }
    return None

def post_github_api(endpoint, data, token=None):
    url = f"https://api.github.com{endpoint}"
    req = urllib.request.Request(url, method="POST")
    req.add_header("Accept", "application/vnd.github.v3+json")
    req.add_header("User-Agent", "XMC-Comment-Replier-Agent")
    req.add_header("Content-Type", "application/json")
    if token:
        req.add_header("Authorization", f"token {token}")
    
    body = json.dumps(data).encode('utf-8')
    try:
        with urllib.request.urlopen(req, data=body) as response:
            return json.loads(response.read().decode('utf-8'))
    except Exception as e:
        sys.stderr.write(f"Error calling GitHub API {url}: {e}\n")
        sys.exit(1)

def main():
    if len(sys.argv) < 2:
        print("Usage: reply_pr_comment.py <comment_url> [message]")
        sys.exit(1)
        
    comment_url = sys.argv[1]
    message = sys.argv[2] if len(sys.argv) > 2 else "code changed"
    
    parsed = parse_comment_url(comment_url)
    if not parsed:
        sys.stderr.write("Could not parse comment URL. Ensure it matches Github comment anchor patterns.\n")
        sys.exit(1)
        
    token = os.environ.get("GITHUB_TOKEN")
    if not token:
        sys.stderr.write("GITHUB_TOKEN environment variable is not set.\n")
        sys.exit(1)
        
    if parsed["type"] == "review_comment":
        # Replying to a specific review comment in a thread
        endpoint = f"/repos/{parsed['owner']}/{parsed['repo']}/pulls/{parsed['pull_number']}/comments"
        payload = {
            "body": message,
            "in_reply_to": parsed["comment_id"]
        }
    else:
        # Posting a general comment on the issue/PR thread
        endpoint = f"/repos/{parsed['owner']}/{parsed['repo']}/issues/{parsed['pull_number']}/comments"
        payload = {
            "body": message
        }
        
    response = post_github_api(endpoint, payload, token)
    print(json.dumps(response, indent=2))

if __name__ == '__main__':
    main()
