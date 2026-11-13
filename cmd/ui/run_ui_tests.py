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
import shlex
import subprocess
import sys

# Installs the UI dependencies the same way as .github/actions/run-ui-tests.
NPM_CI_COMMAND = "npm ci --legacy-peer-deps"


def missing_dependencies_error(ui_dir: str) -> str | None:
    """Returns why `npx ng test` cannot run in ui_dir, or None if the Angular CLI is installed.

    Without this check, a checkout that never ran `npm ci` (e.g. a fresh git worktree) fails
    with an opaque npx error instead of the command that fixes it.
    """
    if os.path.exists(os.path.join(ui_dir, "node_modules", ".bin", "ng")):
        return None
    return (
        f"The UI dependencies are not installed: {ui_dir}/node_modules/.bin/ng is missing. "
        f"Run `cd {ui_dir} && {NPM_CI_COMMAND}`, then re-run the UI tests."
    )


def main() -> None:
    """Runs Angular UI unit tests in physical directory."""
    # Resolve the real physical directory of this script on the host system
    real_script_path = os.path.realpath(__file__)
    real_ui_dir = os.path.dirname(real_script_path)
    if error := missing_dependencies_error(real_ui_dir):
        print(error, file=sys.stderr)
        sys.exit(1)

    # Query passwd database for the actual host user's home directory
    try:
        user_home = pwd.getpwuid(os.getuid()).pw_dir
    except Exception:
        user_home = os.path.expanduser("~")

    # Ensure ~/.local/bin is in PATH if npx/npm/node are installed there
    env = dict(os.environ)
    local_bin = os.path.join(user_home, ".local", "bin")
    current_path = env.get("PATH", "")
    if local_bin not in current_path.split(os.pathsep):
        env["PATH"] = f"{local_bin}{os.pathsep}{current_path}" if current_path else local_bin

    # Check if NVM exists in the user's real home and source it
    nvm_sh = os.path.join(user_home, ".nvm", "nvm.sh")
    if os.path.exists(nvm_sh):
        cmd_args = [
            "bash",
            "-c",
            f"source {shlex.quote(nvm_sh)} && npx ng test --watch=false",
        ]
    else:
        cmd_args = ["npx", "ng", "test", "--watch=false"]

    print(f"Executing Angular tests in physical workspace: {real_ui_dir}")

    # Run without shell=True while sourcing nvm cleanly when present.
    res = subprocess.run(
        cmd_args,
        cwd=real_ui_dir,
        env=env,
        check=False,
    )
    sys.exit(res.returncode)


if __name__ == "__main__":
    main()
