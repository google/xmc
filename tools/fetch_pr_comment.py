#!/usr/bin/env python3
# Copyright 2026 Google LLC
# Helper script to retrieve comment details from GitHub API

import argparse
from tool_utils import run_tool, HandledError
from github_utils import parse_comment_url, call_github_api, GithubAuthError, GithubAPIError

def fetch_comment(args):
    comment_url = args.comment_url
    parsed = parse_comment_url(comment_url)
    if not parsed or parsed["type"] != "review_comment":
        raise HandledError(
            "Could not parse Pull Request review comment URL.",
            "Ensure the URL matches Github PR review comment anchor patterns (e.g. 'https://github.com/owner/repo/pull/num#discussion_r123456')."
        )
        
    try:
        endpoint = f"/repos/{parsed['owner']}/{parsed['repo']}/pulls/comments/{parsed['comment_id']}"
        data = call_github_api(endpoint, user_agent="XMC-Comment-Fetcher-Agent")
        
        return {
            "type": "review_comment",
            "comment_id": data.get("id"),
            "author": data.get("user", {}).get("login"),
            "body": data.get("body"),
            "file_path": data.get("path"),
            "line": data.get("line") or data.get("original_line"),
            "diff_hunk": data.get("diff_hunk"),
            "commit_id": data.get("commit_id")
        }
    except GithubAuthError as e:
        raise HandledError(
            str(e),
            "Please authenticate using the 'gh' CLI (run 'gh auth login') or set the GITHUB_TOKEN environment variable."
        )
    except GithubAPIError as e:
        raise HandledError(
            str(e),
            "Ensure the repository and comment ID exist, and that your credentials have appropriate permissions to access the comments."
        )

def main():
    parser = argparse.ArgumentParser(
        description="Retrieve comment details (author, body, file path, line, diff hunk, commit id) from a GitHub Pull Request review comment URL."
    )
    parser.add_argument("comment_url", help="The URL of the GitHub PR review comment.")
    run_tool(parser, fetch_comment)

if __name__ == '__main__':
    main()
