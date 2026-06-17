# Copyright 2026 Google LLC
# Shared GitHub API helper utilities

import urllib.error
import urllib.request
import json
import subprocess
import shutil
import os

# --- High-Level Orchestrator (Under 30 lines) ---

def call_github_api(endpoint, method="GET", payload=None, user_agent="XMC-Github-Agent"):
    errors = []
    
    # 1. Try gh CLI
    if shutil.which("gh"):
        try:
            return _call_api_with_gh(endpoint, method, payload, user_agent)
        except (subprocess.CalledProcessError, json.JSONDecodeError) as e:
            errors.append(("gh_cli", e))
            
    # 2. Try GITHUB_TOKEN
    token = os.environ.get("GITHUB_TOKEN")
    if token:
        try:
            return _call_api_with_token(endpoint, method, payload, token, user_agent)
        except Exception as e:
            errors.append(("token_auth", e))
            
    # 3. Try Unauthenticated (GET only)
    if method == "GET":
        try:
            return _call_api_unauthenticated(endpoint, user_agent)
        except Exception as e:
            errors.append(("unauthenticated", e))
    else:
        errors.append(("unauthenticated", ValueError("Unauthenticated requests not supported for non-GET methods.")))

    # 4. Handle Failure (Orchestrate error raising)
    _raise_detailed_error(errors)

# --- Lower-Level Execution Helpers ---

def _call_api_with_gh(endpoint, method, payload, user_agent):
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

def _call_api_with_token(endpoint, method, payload, token, user_agent):
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

def _call_api_unauthenticated(endpoint, user_agent):
    url = f"https://api.github.com{endpoint}"
    req = urllib.request.Request(url, method="GET")
    req.add_header("Accept", "application/vnd.github.v3+json")
    req.add_header("User-Agent", user_agent)
    with urllib.request.urlopen(req) as response:
        return json.loads(response.read().decode('utf-8'))

# --- Error Analysis Helper ---

def _raise_detailed_error(errors):
    is_auth_failure = False
    
    for source, err in errors:
        if source == "token_auth" and isinstance(err, urllib.error.HTTPError):
            if err.code in [401, 403]:
                is_auth_failure = True
                break
        elif source == "gh_cli" and isinstance(err, subprocess.CalledProcessError):
            stderr = err.stderr.decode('utf-8', errors='ignore') if isinstance(err.stderr, bytes) else str(err.stderr)
            if any(kw in stderr.lower() for kw in ["auth", "login", "credentials"]):
                is_auth_failure = True
                break

    error_summary = "; ".join(f"[{source}] {type(err).__name__}: {err}" for source, err in errors)
    
    if is_auth_failure:
        raise GithubAuthError(f"GitHub authentication failed: {error_summary}")
    else:
        raise GithubAPIError(f"GitHub API call failed: {error_summary}")
