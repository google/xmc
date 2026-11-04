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

"""Shared GitHub API helper utilities."""

import json
import os
import re
import shutil
import subprocess
import urllib.error
import urllib.request
from typing import Any

from tools.common.tool_utils import run_command


class GithubAuthError(Exception):
    """Raised when GitHub authentication fails."""


class GithubAPIError(Exception):
    """Raised when a GitHub API call fails."""


def parse_comment_url(url: str):
    """Parses GitHub review comment URL into component dictionary."""
    match_review = re.search(r"github\.com/([^/]+)/([^/]+)/pull/(\d+)#discussion_r(\d+)", url)
    if match_review:
        return {
            "type": "review_comment",
            "owner": match_review.group(1),
            "repo": match_review.group(2),
            "pull_number": match_review.group(3),
            "comment_id": int(match_review.group(4)),
        }
    return None


def call_github_api(
    endpoint: str,
    method: str = "GET",
    payload=None,
    user_agent: str = "XMC-Github-Agent",
):
    """Orchestrates GitHub API call trying gh CLI, GITHUB_TOKEN, or unauthenticated."""
    errors: list[tuple[str, Exception]] = []

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
        errors.append(
            (
                "unauthenticated",
                ValueError("Unauthenticated requests not supported for non-GET methods."),
            )
        )

    # 4. Handle Failure
    _raise_detailed_error(errors)
    return None


def _call_api_with_gh(endpoint, method, payload, _user_agent):
    """Calls GitHub API using gh CLI binary."""
    cmd = ["gh", "api", "-X", method, endpoint]
    input_data = None
    if payload:
        cmd += ["--input", "-"]
        input_data = json.dumps(payload)

    res = run_command(
        cmd, capture_output=True, log_cmd=False, log_on_error=False, input_text=input_data
    )
    return json.loads(res.stdout)


def _call_api_with_token(endpoint, method, payload, token, user_agent):
    """Calls GitHub API via HTTP Request using token authorization."""
    url = f"https://api.github.com{endpoint}"
    req = urllib.request.Request(url, method=method)
    req.add_header("Accept", "application/vnd.github.v3+json")
    req.add_header("User-Agent", user_agent)
    req.add_header("Authorization", f"token {token}")

    body = json.dumps(payload).encode("utf-8") if payload else None
    if body:
        req.add_header("Content-Type", "application/json")

    with urllib.request.urlopen(req, data=body) as response:
        return json.loads(response.read().decode("utf-8"))


def _call_api_unauthenticated(endpoint, user_agent):
    """Calls GitHub API via unauthenticated HTTP GET Request."""
    url = f"https://api.github.com{endpoint}"
    req = urllib.request.Request(url, method="GET")
    req.add_header("Accept", "application/vnd.github.v3+json")
    req.add_header("User-Agent", user_agent)
    with urllib.request.urlopen(req) as response:
        return json.loads(response.read().decode("utf-8"))


def _raise_detailed_error(errors):
    """Analyzes errors from authentication attempts and raises detailed error."""
    is_auth_failure = False

    for source, err in errors:
        if source == "token_auth" and isinstance(err, urllib.error.HTTPError):
            if err.code in [401, 403]:
                is_auth_failure = True
                break
        elif source == "gh_cli" and isinstance(err, subprocess.CalledProcessError):
            stderr = (
                err.stderr.decode("utf-8", errors="ignore")
                if isinstance(err.stderr, bytes)
                else str(err.stderr)
            )
            if any(kw in stderr.lower() for kw in ["auth", "login", "credentials"]):
                is_auth_failure = True
                break
        elif source == "unauthenticated" and isinstance(err, ValueError):
            if "Unauthenticated requests not supported" in str(err):
                is_auth_failure = True
                break

    err_msgs = []
    for source, err in errors:
        if isinstance(err, subprocess.CalledProcessError) and err.stderr:
            stderr_str = (
                err.stderr.decode("utf-8", errors="ignore")
                if isinstance(err.stderr, bytes)
                else str(err.stderr)
            )
            err_msgs.append(
                f"[{source}] {type(err).__name__}: {err} (stderr: {stderr_str.strip()})"
            )
        else:
            err_msgs.append(f"[{source}] {type(err).__name__}: {err}")
    error_summary = "; ".join(err_msgs)

    if is_auth_failure:
        raise GithubAuthError(f"GitHub authentication failed: {error_summary}")
    raise GithubAPIError(f"GitHub API call failed: {error_summary}")


def get_authenticated_login(user_agent: str) -> str:
    """Returns the GitHub login of the authenticated user (`GET /user`).

    Args:
        user_agent: User-Agent header for token / unauthenticated HTTP calls.

    Returns:
        The authenticated user's GitHub login.

    Raises:
        GithubAuthError: If GitHub authentication fails.
        GithubAPIError: If the API call fails or the response has no string login.
    """
    user = call_github_api("/user", user_agent=user_agent)
    login = user.get("login") if isinstance(user, dict) else None
    if not isinstance(login, str) or not login:
        raise GithubAPIError("GitHub /user response has no login.")
    return login


def list_all_pages(endpoint: str, user_agent: str = "XMC-Github-Agent") -> list[Any]:
    """Fetches every page of a paginated GitHub REST list endpoint.

    Requests 100 items per page and stops at the first short, empty, or
    non-list page.

    Args:
        endpoint: REST endpoint path, optionally with a query string.
        user_agent: User-Agent header for token / unauthenticated HTTP calls.

    Returns:
        The concatenated items of all pages.

    Raises:
        GithubAuthError: If GitHub authentication fails.
        GithubAPIError: If an API call fails.
    """
    results: list[Any] = []
    sep = "&" if "?" in endpoint else "?"
    page = 1
    while True:
        page_data = call_github_api(
            f"{endpoint}{sep}page={page}&per_page=100", user_agent=user_agent
        )
        if not page_data or not isinstance(page_data, list):
            return results
        results.extend(page_data)
        if len(page_data) < 100:
            return results
        page += 1


_REVIEW_THREADS_QUERY = """
query($owner: String!, $repo: String!, $pr: Int!, $cursor: String) {
  repository(owner: $owner, name: $repo) {
    pullRequest(number: $pr) {
      reviewThreads(first: 100, after: $cursor) {
        pageInfo {
          hasNextPage
          endCursor
        }
        nodes {
          id
          isResolved
          path
          line
          comments(first: 100) {
            nodes {
              databaseId
              body
              url
              author {
                login
              }
            }
          }
        }
      }
    }
  }
}
"""


def list_review_threads(
    owner: str,
    repo: str,
    pr_number: int,
    user_agent: str = "XMC-Github-Agent",
) -> list[dict[str, Any]]:
    """Lists all review threads on a Pull Request via GraphQL, following pagination.

    Args:
        owner: Repository owner (e.g. 'google').
        repo: Repository name (e.g. 'xmc').
        pr_number: Pull Request number.
        user_agent: User-Agent header for token / unauthenticated HTTP calls.

    Returns:
        Raw GraphQL thread nodes (`id`, `isResolved`, `path`, `line`, and
        `comments.nodes` with `databaseId`, `body`, `url`, `author.login`), in
        GitHub's thread order, with null thread nodes dropped. Only the first
        100 comments of each thread are returned; comment nodes may be null.

    Raises:
        GithubAPIError: If the GraphQL query returns errors or no data.
    """
    threads: list[dict[str, Any]] = []
    cursor = None
    while True:
        data = call_github_api(
            "/graphql",
            method="POST",
            payload={
                "query": _REVIEW_THREADS_QUERY,
                "variables": {
                    "owner": owner,
                    "repo": repo,
                    "pr": int(pr_number),
                    "cursor": cursor,
                },
            },
            user_agent=user_agent,
        )
        if not data or "errors" in data or data.get("data") is None:
            error_msg = data.get("errors") if data else "No response data"
            raise GithubAPIError(f"GraphQL query failed: {error_msg}")

        repository = data["data"].get("repository") or {}
        pull_request = repository.get("pullRequest") or {}
        review_threads = pull_request.get("reviewThreads") or {}
        threads.extend(node for node in review_threads.get("nodes") or [] if node)
        page_info = review_threads.get("pageInfo") or {}
        cursor = page_info.get("endCursor")
        if not page_info.get("hasNextPage") or not cursor:
            return threads


def _lookup_thread_id_for_comment(
    owner: str,
    repo: str,
    pr_number: int,
    comment_id: int,
    user_agent: str,
) -> str:
    """Finds the GraphQL review thread node ID for a given comment database ID."""
    for thread in list_review_threads(owner, repo, pr_number, user_agent=user_agent):
        for comment in (thread.get("comments") or {}).get("nodes") or []:
            if comment and comment.get("databaseId") == comment_id:
                thread_id = thread.get("id")
                if thread_id:
                    return str(thread_id)

    raise GithubAPIError(
        f"Could not find review thread for comment ID {comment_id} in PR #{pr_number}"
    )


def resolve_review_thread(
    owner: str,
    repo: str,
    pr_number: int,
    thread_or_comment_id: str | int,
    user_agent: str = "XMC-Github-Agent",
) -> dict[str, Any]:
    """Resolves a GitHub Pull Request review comment thread via GraphQL API.

    Accepts either:
    - A GraphQL review thread Node ID (e.g. 'PRRT_kwDOSdiTps6ayDyJ')
    - A numeric comment ID / database ID (e.g. 3820938617) or comment URL
    """
    target = str(thread_or_comment_id).strip()
    if target.startswith("PRRT_"):
        thread_node_id = target
    else:
        parsed = parse_comment_url(target)
        try:
            comment_id = parsed["comment_id"] if parsed else int(target)
        except ValueError as e:
            raise GithubAPIError(f"Invalid thread or comment ID format: '{target}'") from e
        thread_node_id = _lookup_thread_id_for_comment(
            owner, repo, pr_number, comment_id, user_agent
        )

    mutation = """
    mutation($threadId: ID!) {
      resolveReviewThread(input: {threadId: $threadId}) {
        thread {
          id
          isResolved
        }
      }
    }
    """
    res = call_github_api(
        "/graphql",
        method="POST",
        payload={"query": mutation, "variables": {"threadId": thread_node_id}},
        user_agent=user_agent,
    )
    if not res or "errors" in res or res.get("data") is None:
        error_msg = res.get("errors") if res else "No response data"
        raise GithubAPIError(f"GraphQL mutation failed: {error_msg}")
    res_thread = res["data"].get("resolveReviewThread", {}).get("thread", {})
    return {
        "status": "success",
        "thread_id": thread_node_id,
        "is_resolved": res_thread.get("isResolved", True),
    }
