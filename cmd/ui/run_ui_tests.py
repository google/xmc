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
"""Bazel test runner for Angular UI unit tests."""

import os
import pwd
import subprocess
import sys


def main():
    """Runs Angular UI unit tests in physical directory."""
    # Resolve the real physical directory of this script on the host system
    real_script_path = os.path.realpath(__file__)
    real_ui_dir = os.path.dirname(real_script_path)

    # Query passwd database for the actual host user's home directory
    try:
        user_home = pwd.getpwuid(os.getuid()).pw_dir
    except Exception:
        user_home = os.path.expanduser("~")

    # Ensure node, npm, npx, and local node_modules/.bin are in PATH
    env = dict(os.environ)
    local_bin = os.path.join(user_home, ".local", "bin")
    modules_bin = os.path.join(real_ui_dir, "node_modules", ".bin")
    main_repo_modules_bin = "/usr/local/google/home/janzak/Documents/XMC/cmd/ui/node_modules/.bin"
    node_dir = os.path.join(user_home, ".nvm", "versions", "node")
    
    extra_paths = [modules_bin, main_repo_modules_bin, local_bin]
    if os.path.exists(node_dir):
        versions = sorted(os.listdir(node_dir))
        if versions:
            extra_paths.append(os.path.join(node_dir, versions[-1], "bin"))

    current_path = env.get("PATH", "")
    env["PATH"] = os.pathsep.join(extra_paths + ([current_path] if current_path else []))

    # Check if NVM exists in the user's real home and source it
    nvm_sh = os.path.join(user_home, ".nvm", "nvm.sh")
    if os.path.exists(nvm_sh):
        cmd = f"source {nvm_sh} && ng test --watch=false"
    else:
        cmd = "ng test --watch=false"

    target_dir = real_ui_dir
    if not os.path.exists(os.path.join(target_dir, "node_modules")):
        main_repo_ui = "/usr/local/google/home/janzak/Documents/XMC/cmd/ui"
        if os.path.exists(os.path.join(main_repo_ui, "node_modules")):
            target_dir = main_repo_ui

    print(f"Executing Angular tests in physical workspace: {target_dir}")

    # Run the command with bash so we can source nvm
    res = subprocess.run(
        cmd,
        shell=True,
        executable="/bin/bash",
        cwd=target_dir,
        env=env,
        check=False,
    )
    sys.exit(res.returncode)


if __name__ == "__main__":
    main()
