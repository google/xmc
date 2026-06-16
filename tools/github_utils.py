# Copyright 2026 Google LLC
# Shared GitHub API helper utilities

import os
import re
import json
import urllib.request
import urllib.parse
import subprocess
import shutil
import sys

class GithubAuthError(Exception):
    """Raised when GitHub authentication fails."""
    pass

class GithubAPIError(Exception):
    """Raised when a GitHub API call fails."""
    pass

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
    return None

def call_github_api(endpoint, method="GET", payload=None, user_agent="XMC-Github-Agent"):
    # Method 1: Try gh CLI
    if shutil.which("gh"):
        try:
            cmd = ["gh", "api", "-X", method, endpoint]
            input_data = None
            if payload:
                cmd += ["--input", "-"]
                input_data = json.dumps(payload)
            
            res = subprocess.run(
                cmd,
                input=input_data,
                capture_output=True,
                text=True,
                check=True
            )
            return json.loads(res.stdout)
        except (subprocess.CalledProcessError, json.JSONDecodeError) as e:
            sys.stderr.write(f"Warning: GitHub CLI API call failed: {e}. Falling back...\n")
            
    # Method 2: Try GITHUB_TOKEN
    token = os.environ.get("GITHUB_TOKEN")
    if token:
        try:
            url = f"https://api.github.com{endpoint}"
            req = urllib.request.Request(url, method=method)
            req.add_header("Accept", "application/vnd.github.v3+json")
            req.add_header("User-Agent", user_agent)
            req.add_header("Authorization", f"token {token}")
            
            body = json.dumps(payload).encode('utf-8') if payload else None
            if body:
                req.add_header("Content-Type", "application/json")
                
            with urllib.request.urlopen(req, data=body) as response:
                return json.loads(response.read().decode('utf-8'))
        except Exception as e:
            sys.stderr.write(f"Warning: GITHUB_TOKEN API call failed: {e}. Falling back...\n")
            
    # Method 3: Try Unauthenticated (GET only)
    if method == "GET":
        try:
            url = f"https://api.github.com{endpoint}"
            req = urllib.request.Request(url, method=method)
            req.add_header("Accept", "application/vnd.github.v3+json")
            req.add_header("User-Agent", user_agent)
            with urllib.request.urlopen(req) as response:
                return json.loads(response.read().decode('utf-8'))
        except Exception as e:
            raise GithubAPIError(f"Unauthenticated GitHub API call failed: {e}")
    else:
        raise GithubAuthError("GitHub authentication failed. No credentials found (gh CLI or GITHUB_TOKEN).")
