#!/usr/bin/env python3
import argparse
import os
import subprocess
import sys

# List of files that, if changed, require running all tests.
GLOBAL_BUILD_FILES = {
    "WORKSPACE",
    "MODULE.bazel",
    "MODULE.bazel.lock",
    "go.mod",
    "go.sum",
    ".bazelrc",
    "compat.bzl",
    "import.bzl",
    "oci.bzl",
}

# File extensions that represent source files that might affect tests.
SRC_EXTENSIONS = {
    ".go", ".py", ".ts", ".js", ".tsx", ".jsx", ".proto", 
    ".json", ".scss", ".css", ".html", ".sh", ".sql"
}

def run_cmd(args):
    res = subprocess.run(args, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    if res.returncode != 0:
        raise Exception(f"Command {' '.join(args)} failed with exit code {res.returncode}: {res.stderr}")
    return res.stdout.strip()

def get_changed_files(base_ref):
    if not base_ref or base_ref == "0000000000000000000000000000000000000000":
        raise Exception("Invalid base reference")
    try:
        # Get merge base first for pull requests
        merge_base = run_cmd(["git", "merge-base", base_ref, "HEAD"])
        output = run_cmd(["git", "diff", "--name-only", f"{merge_base}...HEAD"])
    except Exception:
        # Fallback to standard diff if merge-base fails
        output = run_cmd(["git", "diff", "--name-only", f"{base_ref}...HEAD"])
    
    return [line.strip() for line in output.splitlines() if line.strip()]

def get_tracked_sources():
    output = run_cmd(["bazel", "query", "kind('source file', deps(//...))"])
    sources = set()
    for line in output.splitlines():
        line = line.strip()
        if line.startswith("//"):
            # Convert //path/to:file -> path/to/file or //:file -> file
            pkg_file = line[2:]
            if ":" in pkg_file:
                pkg, file = pkg_file.split(":", 1)
                path = f"{pkg}/{file}" if pkg else file
            else:
                path = pkg_file
            sources.add(path)
    return sources

def get_packages():
    output = run_cmd(["bazel", "query", "buildfiles(//...)"])
    packages = set()
    for line in output.splitlines():
        line = line.strip()
        if line.startswith("//"):
            # e.g. //internal/auth:BUILD.bazel or //:BUILD.bazel
            pkg_part = line[2:].split(":")[0]
            packages.add(pkg_part)
    return packages

def find_closest_package(file_path, packages):
    dirname = os.path.dirname(file_path)
    while dirname:
        if dirname in packages:
            return dirname
        dirname = os.path.dirname(dirname)
    if "" in packages:
        return ""
    return None

def main():
    parser = argparse.ArgumentParser(description="Determine affected Bazel tests based on changed files.")
    parser.add_argument("--base", default="origin/main", help="Base commit/branch to diff against")
    args = parser.parse_args()

    try:
        changed_files = get_changed_files(args.base)
    except Exception as e:
        print(f"WARNING: Failed to get changed files: {e}", file=sys.stderr)
        # Fallback to all tests on error
        print("//...")
        return

    # Check if any global config files changed
    for f in changed_files:
        if f in GLOBAL_BUILD_FILES or f.startswith(".github/workflows/"):
            print(f"INFO: Global build/config file {f} changed. Running all tests.", file=sys.stderr)
            print("//...")
            return

    try:
        tracked_sources = get_tracked_sources()
        packages = get_packages()
    except Exception as e:
        print(f"WARNING: Failed to run Bazel queries: {e}", file=sys.stderr)
        print("//...")
        return

    targets = set()
    run_cmd_tests = False
    for f in changed_files:
        if f.startswith("internal/"):
            run_cmd_tests = True

        basename = os.path.basename(f)
        if basename in ("BUILD", "BUILD.bazel") or f.endswith(".bzl"):
            pkg = os.path.dirname(f)
            targets.add(f"//{pkg}:all")
            continue

        # If it's a tracked source file, add its label
        if f in tracked_sources:
            pkg = find_closest_package(f, packages)
            if pkg is not None:
                rel_path = os.path.relpath(f, pkg) if pkg else f
                targets.add(f"//{pkg}:{rel_path}")
            continue

        # Check if it is a deleted/untracked file with a source extension
        _, ext = os.path.splitext(f)
        if ext in SRC_EXTENSIONS:
            pkg = find_closest_package(f, packages)
            if pkg is not None:
                targets.add(f"//{pkg}:all")

    if not targets:
        if run_cmd_tests:
            print("//cmd/...")
        else:
            print("INFO: No source code or build file changes detected. No tests to run.", file=sys.stderr)
        return

    # Construct the Bazel query to find reverse dependencies of tests
    targets_str = " ".join(targets)
    query = f"tests(rdeps(//..., set({targets_str})))"
    
    try:
        test_targets = run_cmd(["bazel", "query", query])
        test_targets_list = [t.strip() for t in test_targets.splitlines() if t.strip()]
        if run_cmd_tests:
            test_targets_list.append("//cmd/...")
        
        # De-duplicate targets
        test_targets_list = sorted(list(set(test_targets_list)))

        if test_targets_list:
            for target in test_targets_list:
                print(target)
        else:
            print("INFO: No tests depend on the changed files.", file=sys.stderr)
    except Exception as e:
        print(f"WARNING: Bazel query failed: {e}", file=sys.stderr)
        # Fallback to all tests
        print("//...")

if __name__ == "__main__":
    main()
