# Copyright 2026 Google LLC
# Shared GitHub API helper utilities

import os
import re
import json
import urllib.request
import urllib.parse
import subprocess
import shutil

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
    errors = []
    
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
        except subprocess.CalledProcessError as e:
            errors.append(f"GitHub CLI failed (exit code {e.returncode}): {e.stderr.strip() if e.stderr else str(e)}")
        except json.JSONDecodeError as e:
            errors.append(f"GitHub CLI succeeded but output was not valid JSON: {e}")
            
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
            errors.append(f"GITHUB_TOKEN request failed: {e}")
            
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
            errors.append(f"Unauthenticated GET request failed: {e}")
    else:
        errors.append("Unauthenticated requests are not supported for non-GET methods.")

    # If we reached this point, all attempted methods failed.
    # We raise an appropriate exception with all accumulated errors.
    error_summary = "; ".join(errors)
    
    # If any credential was present but failed, or if no credentials were found and method is POST
    has_credentials = shutil.which("gh") or token
    if not has_credentials and method != "GET":
        raise GithubAuthError(f"GitHub authentication failed. No credentials found (gh CLI or GITHUB_TOKEN). Details: {error_summary}")
    else:
        # Credentials were present but failed, or API call failed.
        # If the failure is likely auth-related (e.g. 401/403/bad credentials), raise GithubAuthError,
        # otherwise raise GithubAPIError.
        auth_keywords = ["401", "unauthorized", "403", "authentication", "permission", "credentials"]
        if any(kw in error_summary.lower() for kw in auth_keywords):
            raise GithubAuthError(f"GitHub authentication failed: {error_summary}")
        else:
            raise GithubAPIError(f"GitHub API call failed: {error_summary}")
