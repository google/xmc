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

"""Lint that agent docs only reference repository paths that exist.

Scans tracked `.agents/**/*.md` and `AGENTS.md` files for repo-relative path
tokens (for example `tools/pr_review/review_pr_generate_audit.py`) inside inline
code spans and fenced code blocks, and fails when a token resolves to neither a
tracked file nor a tracked directory. Agents copy these paths verbatim into
shell commands, so a stale path surfaces as `exit 127` mid-task.

Placeholders are supported: a token containing `<...>`, `{...}`, `$`, `*`, `[`
or `...` is truncated at the placeholder and only its directory prefix must
exist. Paths created at runtime (virtual environments, generated output) and
illustrative examples are listed in ALLOWLIST with a justification; entries no
doc references fail the check so the list cannot go stale.

Docs are read from the working tree while existence is checked against the git
index, matching the other repository lint hooks.
"""

import argparse
import dataclasses
import re
import subprocess
import sys
from collections.abc import Iterable, Iterator
from pathlib import Path
from typing import Any

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.common.tool_utils import HandledError, run_tool  # noqa: E402

# Top-level repository directories whose paths agents copy into commands.
PATH_ROOTS = (
    ".agents",
    ".githooks",
    ".github",
    "api",
    "cmd",
    "deploy",
    "docs",
    "examples",
    "internal",
    "pkg",
    "scripts",
    "sdks",
    "test",
    "tools",
)

# Paths that legitimately do not exist in git, mapped to the reason. Entries
# also cover every path beneath them.
ALLOWLIST: dict[str, str] = {
    # Created at runtime.
    "tools/common/venv": "Tooling virtualenv created at runtime by tools/common/setup_venv.py.",
    "deploy/ctk_v2/xmanager-cloud-deployment.yaml": (
        "User-authored deployment overrides file; gitignored via `*-deployment.yaml`."
    ),
    # Illustrative examples that intentionally name hypothetical paths.
    "docs/ctk-installation-guide": "Branch-name example in github_authoring_and_prs.md.",
    "examples/xdash_dashboard/create_dashboard_test.py": (
        "Sample assertion payload in xmc_trajectory_comparison/SKILL.md."
    ),
    "pkg/auth/handler.go": "Sample review comment in xmc_review_pr/SKILL.md.",
    "pkg/controller/reconciler.go": "Stack plan template in xmc_pr_split_verification/SKILL.md.",
    "pkg/controller/reconciler_test.go": "Stack plan template in xmc_pr_split_verification.",
    "pkg/domain/handler.go": "Stack plan template in xmc_pr_split_verification/SKILL.md.",
    "pkg/domain/handler_test.go": "Stack plan template in xmc_pr_split_verification/SKILL.md.",
    "pkg/domain/model.go": "Stack plan template in xmc_pr_split_verification/SKILL.md.",
    "pkg/email/validator.go": "Domain naming example in core_engineering.md.",
    "pkg/time/formatter.go": "Domain naming example in core_engineering.md.",
    "pkg/url/parser.go": "Domain naming example in core_engineering.md.",
    "pkg/util": "Anti-pattern example in core_engineering.md.",
    "test/e2e/workflow_test.go": "Stack plan template in xmc_pr_split_verification/SKILL.md.",
    "tools/parse_x.py": "Routing table example in xmc_iterative_reflection/SKILL.md.",
}


def build_path_token_re(roots: Iterable[str]) -> re.Pattern[str]:
    """Compiles the path-token pattern for the given top-level roots.

    A token must not be preceded by characters that make it part of a larger
    path, URL, Bazel label, or identifier (e.g. `github.com/x/pkg/foo`,
    `//tools:x`). Root order does not matter: the mandatory `/` after the
    alternation makes the engine backtrack past a shorter root that prefixes a
    longer one.
    """
    return re.compile(
        r"(?<![\w./@~}-])(?:\./)?((?:"
        + "|".join(re.escape(root) for root in roots)
        + r")/[\w./<*{}$\[\]-]*)"
    )


_PATH_TOKEN_RE = build_path_token_re(PATH_ROOTS)
_INLINE_CODE_RE = re.compile(r"(`+)(.+?)\1")
_FENCE_RE = re.compile(r"^\s*(`{3,}|~{3,})(.*)$")
_PLACEHOLDER_RE = re.compile(r"[<{$*\[]|\.\.\.")
_TRAILING_PUNCTUATION = "."


@dataclasses.dataclass(frozen=True)
class Violation:
    """A path token in an agent doc that does not exist in the repository."""

    doc: str
    line: int
    path: str


def is_agent_doc(path: str) -> bool:
    """Returns whether a repo-relative path is an agent doc in scope."""
    return (
        path == "AGENTS.md"
        or path.endswith("/AGENTS.md")
        or (path.startswith(".agents/") and path.endswith(".md"))
    )


def closes_fence(line: str, opening: str) -> bool:
    """Returns whether a line closes a fenced block opened with `opening`.

    Per CommonMark, the closing run uses the same character, is at least as
    long as the opening run, and carries no info string, so a ```bash line
    nested inside a ```` block does not close it.
    """
    fence = _FENCE_RE.match(line)
    if not fence:
        return False
    run, rest = fence.group(1), fence.group(2)
    return run[0] == opening[0] and len(run) >= len(opening) and not rest.strip()


def iter_code_segments(text: str) -> Iterator[tuple[int, str]]:
    """Yields (1-based line number, code text) for code spans and fenced blocks."""
    opening = ""
    for lineno, line in enumerate(text.splitlines(), start=1):
        if opening:
            if closes_fence(line, opening):
                opening = ""
            else:
                yield lineno, line
            continue
        fence = _FENCE_RE.match(line)
        if fence:
            opening = fence.group(1)
            continue
        for match in _INLINE_CODE_RE.finditer(line):
            yield lineno, match.group(2)


def extract_path_tokens(code: str) -> list[str]:
    """Returns repo-relative path tokens found in a code segment."""
    return [match.group(1) for match in _PATH_TOKEN_RE.finditer(code)]


def resolve_checked_path(token: str) -> str:
    """Normalizes a path token to the portion that must exist in the repo.

    Placeholder tokens reduce to the directory before the first placeholder;
    trailing punctuation and slashes are dropped.
    """
    placeholder = _PLACEHOLDER_RE.search(token)
    if placeholder:
        token = token[: placeholder.start()]
        token = token[: token.rfind("/")] if "/" in token else token
    return token.rstrip(_TRAILING_PUNCTUATION).rstrip("/")


def tracked_paths(files: Iterable[str]) -> frozenset[str]:
    """Returns every tracked file plus every directory containing one."""
    paths: set[str] = set()
    for f in files:
        paths.add(f)
        parts = f.split("/")
        paths.update("/".join(parts[:i]) for i in range(1, len(parts)))
    return frozenset(paths)


def allowlist_entry_for(path: str, allowlist: dict[str, str]) -> str | None:
    """Returns the allowlist key covering a path, or None."""
    for entry in allowlist:
        if path == entry or path.startswith(entry + "/"):
            return entry
    return None


def find_violations(
    doc: str, text: str, existing: frozenset[str], allowlist: dict[str, str]
) -> tuple[list[Violation], set[str]]:
    """Returns violations in one doc and the allowlist entries it used."""
    violations: list[Violation] = []
    used: set[str] = set()
    for lineno, code in iter_code_segments(text):
        for token in extract_path_tokens(code):
            path = resolve_checked_path(token)
            if path in existing:
                continue
            entry = allowlist_entry_for(path, allowlist)
            if entry:
                used.add(entry)
                continue
            violations.append(Violation(doc=doc, line=lineno, path=path))
    return violations, used


def list_tracked_files(repo_root: Path) -> list[str]:
    """Lists git-tracked files relative to the repository root."""
    try:
        result = subprocess.run(
            ["git", "ls-files", "-z"], cwd=repo_root, capture_output=True, check=True
        )
    except (FileNotFoundError, subprocess.CalledProcessError) as e:
        raise HandledError(
            f"git ls-files failed in {repo_root}: {e}",
            "Run from inside the repository with git on PATH, or pass --repo-root.",
        ) from e
    return [f for f in result.stdout.decode("utf-8", errors="replace").split("\0") if f]


def scan_docs(
    repo_root: Path, docs: list[str], existing: frozenset[str], allowlist: dict[str, str]
) -> tuple[list[Violation], set[str]]:
    """Returns violations across docs and the allowlist entries they used.

    Tracked docs deleted from the working tree are skipped.
    """
    violations: list[Violation] = []
    used: set[str] = set()
    for doc in docs:
        doc_path = repo_root / doc
        if not doc_path.is_file():
            continue
        text = doc_path.read_text(encoding="utf-8", errors="replace")
        found, doc_used = find_violations(doc, text, existing, allowlist)
        violations.extend(found)
        used |= doc_used
    return violations, used


def check_agent_doc_paths(args: argparse.Namespace) -> dict[str, Any]:
    """Scans all tracked agent docs and fails on non-existent path references.

    Also fails on ALLOWLIST entries no doc references, so stale justifications
    are removed together with the doc text that needed them.
    """
    repo_root = Path(args.repo_root).resolve()
    files = list_tracked_files(repo_root)
    docs = sorted(f for f in files if is_agent_doc(f))
    violations, used = scan_docs(repo_root, docs, tracked_paths(files), ALLOWLIST)
    if violations:
        raise HandledError(
            f"{len(violations)} agent doc path reference(s) do not exist in the repository.",
            "Fix the path in the doc, use a placeholder such as `<name>` for illustrative "
            "paths, or add the path to ALLOWLIST in tools/lint/check_agent_doc_paths.py "
            "with a justification if it is created at runtime.",
            details={"violations": [dataclasses.asdict(v) for v in violations]},
        )
    unused = sorted(set(ALLOWLIST) - used)
    if unused:
        raise HandledError(
            f"{len(unused)} ALLOWLIST entry(ies) are no longer referenced by any agent doc.",
            "Remove the stale entries from ALLOWLIST in tools/lint/check_agent_doc_paths.py.",
            details={"unused_allowlist": unused},
        )
    return {"docs_scanned": len(docs)}


def main() -> None:
    """CLI entrypoint."""
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--repo-root", default=str(_REPO_ROOT), help="Repository root.")
    run_tool(parser, check_agent_doc_paths)


if __name__ == "__main__":
    main()
