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

import os
import subprocess
import unittest


class TestUpgradeDevEnv(unittest.TestCase):
    def setUp(self):
        self.repo_root = os.path.abspath(
            os.path.join(os.path.dirname(__file__), "..")
        )
        self.script_path = os.path.join(self.repo_root, "tools", "upgrade_dev_env.sh")
        self.current_head = subprocess.check_output(
            ["git", "rev-parse", "HEAD"], cwd=self.repo_root, text=True
        ).strip()

    def run_script(self, args):
        return subprocess.run(
            [self.script_path] + args,
            cwd=self.repo_root,
            capture_output=True,
            text=True,
        )

    def test_help_flag(self):
        result = self.run_script(["--help"])
        self.assertEqual(result.returncode, 0)
        self.assertIn("--git-ref", result.stdout)
        self.assertIn("--tag", result.stdout)
        self.assertIn("--branch", result.stdout)
        self.assertIn("checkout", result.stdout)

    def test_only_checkout_no_ref(self):
        result = self.run_script(["--only", "checkout"])
        self.assertEqual(result.returncode, 0)
        self.assertIn(
            "No --git-ref specified; continuing upgrade with current git working state",
            result.stdout,
        )

    def test_only_checkout_current_head_git_ref(self):
        result = self.run_script(["--git-ref", self.current_head, "--only", "checkout"])
        self.assertEqual(result.returncode, 0)
        self.assertIn(
            f"Repository is already checked out at git ref '{self.current_head}'",
            result.stdout,
        )

    def test_only_checkout_current_head_tag_alias(self):
        result = self.run_script(["--tag", self.current_head, "--only", "checkout"])
        self.assertEqual(result.returncode, 0)
        self.assertIn(
            f"Repository is already checked out at git ref '{self.current_head}'",
            result.stdout,
        )

    def test_only_checkout_current_head_branch_alias(self):
        result = self.run_script(["--branch", self.current_head, "--only", "checkout"])
        self.assertEqual(result.returncode, 0)
        self.assertIn(
            f"Repository is already checked out at git ref '{self.current_head}'",
            result.stdout,
        )

    def test_only_checkout_nonexistent_ref(self):
        result = self.run_script(
            ["--git-ref", "nonexistent-ref-xyz-99999", "--only", "checkout"]
        )
        self.assertEqual(result.returncode, 1)
        self.assertIn(
            "Git reference 'nonexistent-ref-xyz-99999' does not exist locally or on remote origin.",
            result.stderr,
        )

    def test_only_checkout_remote_ref(self):
        remote_ref = "test-remote-only-ref-unit"
        subprocess.run(
            ["git", "update-ref", f"refs/remotes/origin/{remote_ref}", self.current_head],
            cwd=self.repo_root,
            check=True,
        )
        try:
            result = self.run_script(["--git-ref", remote_ref, "--only", "checkout"])
            self.assertEqual(result.returncode, 0)
            self.assertIn(
                f"Repository is already checked out at git ref '{remote_ref}'",
                result.stdout,
            )
        finally:
            subprocess.run(
                ["git", "update-ref", "-d", f"refs/remotes/origin/{remote_ref}"],
                cwd=self.repo_root,
                check=False,
            )


if __name__ == "__main__":
    unittest.main()
