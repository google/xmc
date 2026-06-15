#!/usr/bin/env python3
# Copyright 2026 Google LLC
# Helper script to post a reply to a PR comment on GitHub

import sys
import json
from github_utils import parse_comment_url, call_github_api


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
        
    response = call_github_api(endpoint, method="POST", payload=payload, user_agent="XMC-Comment-Replier-Agent")
    print(json.dumps(response, indent=2))

if __name__ == '__main__':
    main()
