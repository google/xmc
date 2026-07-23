#!/usr/bin/env python3
# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Dynamic Prompt Token Evaluator for .agents Knowledge Architecture.

Usage:
  python3 ./tools/measure_prompt_tokens.py "<developer prompt>"

Examples:
  python3 ./tools/measure_prompt_tokens.py "Implement feature in golang"
  python3 ./tools/measure_prompt_tokens.py "Fix PR comment https://github.com/google/xmc/pull/15#discussion_r123"
  python3 ./tools/measure_prompt_tokens.py "Upgrade XMC cluster environment"

This script dynamically evaluates prompt context loading before and after PR changes:
- Before PR (Un-optimized Baseline): Pre-fetches all rules & skills via markdown links -> ~14.55k tokens.
- After PR (Optimized Architecture): Evaluates prompt against workflow_triggers.yaml -> ~1.9k-3k tokens.
"""

import glob
import os
import re
import sys

BYTES_PER_TOKEN = 4.0

# Pre-PR Un-optimized Baseline (All 13 legacy flat files pre-fetched via markdown links)
PRE_PR_BASELINE_BYTES = 58203
PRE_PR_BASELINE_TOKENS = int(PRE_PR_BASELINE_BYTES / BYTES_PER_TOKEN)  # ~14,550 tokens


def get_file_tokens(file_path: str) -> int:
    if not os.path.isfile(file_path):
        return 0
    return int(os.path.getsize(file_path) / BYTES_PER_TOKEN)


def extract_markdown_links(file_path: str) -> list[str]:
    """Finds all relative markdown link targets [title](relative_path.md) inside a markdown file."""
    if not os.path.isfile(file_path):
        return []
    with open(file_path, encoding="utf-8", errors="ignore") as f:
        content = f.read()

    dir_name = os.path.dirname(file_path)
    raw_links = re.findall(r"\[.*?\]\(([^)]+\.md)\)", content)
    resolved_paths = []
    for link in raw_links:
        if link.startswith("http://") or link.startswith("https://"):
            continue
        full_path = os.path.normpath(os.path.join(dir_name, link))
        if os.path.exists(full_path):
            resolved_paths.append(full_path)
    return resolved_paths


def evaluate_baseline_preload(root_index: str = ".agents/AGENTS.md") -> list[str]:
    """Recursively resolves all pre-fetched markdown links starting from AGENTS.md."""
    visited = set()
    queue = [root_index]

    if os.path.exists(root_index):
        visited.add(os.path.normpath(root_index))

    while queue:
        curr = queue.pop(0)
        links = extract_markdown_links(curr)
        for link in links:
            if link not in visited:
                visited.add(link)
                queue.append(link)

    for skill_file in glob.glob(".agents/skills/**/*.md", recursive=True):
        visited.add(os.path.normpath(skill_file))

    return sorted(visited)


def evaluate_dynamic_router(prompt: str) -> list[str]:
    """Evaluates prompt against workflow_triggers.yaml if present."""
    triggers_file = ".agents/rules/triggers/workflow_triggers.yaml"

    if not os.path.exists(triggers_file):
        return evaluate_baseline_preload()

    prompt_lower = prompt.lower()
    selected_files = set()

    # Tier 1 Universal Core
    core_files = [
        ".agents/AGENTS.md",
        ".agents/rules/core/core_engineering.md",
        ".agents/rules/core/anti_yolo_policy.md",
        ".agents/rules/core/skills_and_automation.md",
    ]
    for c in core_files:
        if os.path.exists(c):
            selected_files.add(os.path.normpath(c))

    import yaml

    try:
        with open(triggers_file) as f:
            data = yaml.safe_load(f)

        triggers = data.get("triggers", [])
        for trigger in triggers:
            matchers = trigger.get("matchers", {})
            keywords = matchers.get("keywords", [])
            urls = matchers.get("url_patterns", [])

            matched = False
            for kw in keywords:
                if kw.lower() in prompt_lower:
                    matched = True
                    break
            if not matched:
                for pattern in urls:
                    if re.search(pattern, prompt, re.IGNORECASE):
                        matched = True
                        break

            if matched:
                for r in trigger.get("rules_to_load", []):
                    if os.path.exists(r):
                        selected_files.add(os.path.normpath(r))
                s = trigger.get("skill_to_invoke")
                if s and os.path.exists(s):
                    selected_files.add(os.path.normpath(s))
    except Exception:
        pass

    # Check language rules match
    lang_map = {
        "go": ".agents/rules/languages/golang.md",
        "golang": ".agents/rules/languages/golang.md",
        "python": ".agents/rules/languages/python.md",
        "py": ".agents/rules/languages/python.md",
        "typescript": ".agents/rules/languages/typescript.md",
        "ts": ".agents/rules/languages/typescript.md",
        "shell": ".agents/rules/languages/shell.md",
        "bash": ".agents/rules/languages/shell.md",
    }
    for kw, lang_file in lang_map.items():
        if re.search(r"\b" + re.escape(kw) + r"\b", prompt_lower):
            if os.path.exists(lang_file):
                selected_files.add(os.path.normpath(lang_file))

    return sorted(selected_files)


def main():
    prompt = sys.argv[1] if len(sys.argv) >= 2 else "Implement feature in golang"

    print("=" * 80)
    print(f' 🔍 Prompt Context Evaluator: "{prompt}"')
    print("=" * 80)

    triggers_file = ".agents/rules/triggers/workflow_triggers.yaml"
    is_optimized = os.path.exists(triggers_file)

    if is_optimized:
        print(" [State: POST-PR OPTIMIZED ARCHITECTURE (workflow_triggers.yaml active)]")
        loaded_files = evaluate_dynamic_router(prompt)
        baseline_tokens = PRE_PR_BASELINE_TOKENS
    else:
        print(" [State: PRE-PR BASELINE (Recursive Link Pre-Fetch active)]")
        loaded_files = evaluate_baseline_preload()
        baseline_tokens = sum(get_file_tokens(f) for f in loaded_files)

    current_tokens = sum(get_file_tokens(f) for f in loaded_files)

    print("\n Loaded Context Files for this Prompt:")
    for f in loaded_files:
        print(f"   • {f:<55} ({get_file_tokens(f):>5} tokens)")

    print("-" * 80)
    print(f" Prompt Token Throughput : {current_tokens:,} tokens")

    if is_optimized:
        saved = baseline_tokens - current_tokens
        pct = (saved / baseline_tokens) * 100
        print(
            f" Pre-PR Baseline Comparison : {baseline_tokens:,} tokens -> {current_tokens:,} tokens"
        )
        print(f" PR Benchmark Impact       : -{saved:,} tokens (-{pct:.1f}% reduction)")
    print("=" * 80)


if __name__ == "__main__":
    main()
