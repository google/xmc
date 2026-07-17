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

"""Helper script to run golangci-lint on Bazel go_path and map paths back."""

import json
import os
import subprocess
import sys
import xml.etree.ElementTree as ET


def run_cmd(cmd, cwd=None):
    res = subprocess.run(cmd, shell=True, capture_output=True, text=True, cwd=cwd)
    return res.returncode, res.stdout, res.stderr


def find_linter():
    ret, out, _ = run_cmd("which golangci-lint")
    if ret == 0:
        return out.strip()
    home = os.path.expanduser("~")
    path = os.path.join(home, "go", "bin", "golangci-lint")
    if os.path.exists(path):
        return path
    return None


def main():
    workspace_root = os.path.realpath(os.getcwd())

    # 1. Build go_path
    print("Building xmc_go_path...")
    ret, out, err = run_cmd("bazel build //:xmc_go_path")
    if ret != 0:
        print("Failed to build xmc_go_path:")
        print(err)
        sys.exit(1)

    # 2. Query Bazel for go_library targets to build the importpath map
    print("Querying Bazel for package mappings...")
    ret, out, err = run_cmd(
        'bazel query \'kind("go_library", //...) + kind("go_proto_library", //...)\' --output=xml'
    )
    if ret != 0:
        print("Failed to query Bazel:")
        print(err)
        sys.exit(1)

    importpath_map = {}  # importpath -> (target_label, package_dir)
    try:
        root = ET.fromstring(out)
        for rule in root.findall("rule"):
            target_name = rule.attrib.get("name")
            # package dir is target label without target name and leading //
            pkg_dir = target_name.split(":")[0].lstrip("/")
            for string_attr in rule.findall("string"):
                if string_attr.attrib.get("name") == "importpath":
                    importpath = string_attr.attrib.get("value")
                    importpath_map[importpath] = (target_name, pkg_dir)
    except Exception as e:
        print("Failed to parse Bazel query output XML:", e)
        sys.exit(1)

    # 3. Run golangci-lint
    print("Running golangci-lint...")
    go_path_dir = os.path.realpath(os.path.join(workspace_root, "bazel-bin", "xmc_go_path"))
    run_dir = os.path.realpath(os.path.join(go_path_dir, "src", "xmanager_cloud"))

    if not os.path.exists(run_dir):
        print(f"Error: go_path src directory not found at {run_dir}")
        print("Ensure //:xmc_go_path built correctly.")
        sys.exit(1)

    # Setup environment for GOPATH mode
    env = os.environ.copy()
    env["GO111MODULE"] = "off"
    env["GOPATH"] = go_path_dir

    linter_bin = find_linter()
    if not linter_bin:
        print("Error: golangci-lint not found in PATH or ~/go/bin/")
        sys.exit(1)

    cmd = f"{linter_bin} run --out-format json ./..."
    res = subprocess.run(cmd, shell=True, capture_output=True, text=True, cwd=run_dir, env=env)

    # We parse the JSON output
    try:
        # golangci-lint might exit with non-zero if it found issues,
        # but it should still output JSON if it ran successfully.
        lint_res = json.loads(res.stdout)
    except json.JSONDecodeError:
        print("Failed to parse golangci-lint output as JSON:")
        print("STDOUT:", res.stdout)
        print("STDERR:", res.stderr)
        sys.exit(1)

    issues = lint_res.get("Issues", [])
    if not issues:
        print("No lint issues found.")
        sys.exit(0)

    # 4. Map paths and print issues
    has_errors = False
    query_cache = {}

    for issue in issues:
        file_path = issue.get("Pos", {}).get("Filename", "")
        line = issue.get("Pos", {}).get("Line", 0)
        col = issue.get("Pos", {}).get("Column", 0)
        message = issue.get("Text", "")
        linter = issue.get("FromLinter", "")

        original_file = None

        abs_file_path = os.path.realpath(os.path.join(run_dir, file_path))
        src_dir = os.path.realpath(os.path.join(go_path_dir, "src"))
        rel_to_src = os.path.relpath(abs_file_path, src_dir)

        if rel_to_src.startswith("xmanager_cloud/"):
            rel_path = rel_to_src[len("xmanager_cloud/") :]
        else:
            rel_path = rel_to_src

        source_tree_path = os.path.join(workspace_root, rel_path)
        if os.path.exists(source_tree_path) and not os.path.isdir(source_tree_path):
            original_file = rel_path
        else:
            # Find the longest matching importpath prefix
            best_match = ""
            best_target = None
            best_pkg_dir = None

            # Construct import path prefix from directory of the file
            file_dir = os.path.dirname(rel_path)
            file_import_path = "xmanager_cloud/" + file_dir

            for importpath, (target, pkg_dir) in importpath_map.items():
                # Check if file_import_path starts with importpath
                # We append '/' to avoid partial matches (e.g. cmd/x matches cmd/xid)
                if (file_import_path + "/").startswith(importpath + "/"):
                    if len(importpath) > len(best_match):
                        best_match = importpath
                        best_target = target
                        best_pkg_dir = pkg_dir

            if best_target:
                filename = os.path.basename(rel_path)
                file_target = f"{best_target.split(':')[0]}:{filename}"

                if file_target in query_cache:
                    original_file = query_cache[file_target]
                else:
                    query = f'kind("source file", deps({file_target}))'
                    q_ret, q_out, q_err = run_cmd(f"bazel query '{query}'")
                    if q_ret == 0:
                        for line_out in q_out.splitlines():
                            line_out = line_out.strip()
                            if line_out.startswith("//") and not line_out.startswith("@"):
                                parts = line_out.lstrip("/").split(":")
                                if len(parts) == 2:
                                    orig = os.path.join(parts[0], parts[1])
                                    if os.path.exists(os.path.join(workspace_root, orig)):
                                        original_file = orig
                                        query_cache[file_target] = orig
                                        break
                    if not original_file:
                        original_file = os.path.join(best_pkg_dir, filename)
                        query_cache[file_target] = original_file
            else:
                original_file = rel_path

        print(f"{original_file}:{line}:{col}: {message} ({linter})")
        has_errors = True

    if has_errors:
        sys.exit(1)


if __name__ == "__main__":
    main()
