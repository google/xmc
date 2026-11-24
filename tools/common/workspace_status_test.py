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

"""Unit tests for tools.common.workspace_status."""

from __future__ import annotations

import io
import os
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from tools.common import workspace_status
from tools.common.release_tags import PRE_RELEASE_TAG_GLOB, RELEASE_TAG_GLOBS


class WorkspaceStatusTest(unittest.TestCase):
    def test_env_override_takes_precedence_over_git(self) -> None:
        with (
            mock.patch.dict(
                os.environ, {workspace_status.ENV_VERSION_VAR: "  1.2.3  "}, clear=False
            ),
            mock.patch("subprocess.run") as mock_run,
        ):
            self.assertEqual(workspace_status.resolve_version(), "1.2.3")
            mock_run.assert_not_called()

    def test_invalid_env_override_with_whitespace_falls_through(self) -> None:
        with (
            mock.patch.dict(
                os.environ, {workspace_status.ENV_VERSION_VAR: "1.2.3 beta"}, clear=False
            ),
            mock.patch(
                "subprocess.run",
                return_value=subprocess.CompletedProcess([], 0, stdout="1.1.0\n"),
            ),
            mock.patch("sys.stderr", new_callable=io.StringIO) as err,
        ):
            self.assertEqual(workspace_status.resolve_version(warn=True), "1.1.0")
            self.assertIn("Ignoring invalid XMC_VERSION", err.getvalue())

    def test_git_describe_uses_shared_release_tag_globs(self) -> None:
        env = {k: v for k, v in os.environ.items() if k != workspace_status.ENV_VERSION_VAR}
        with (
            mock.patch.dict(os.environ, env, clear=True),
            mock.patch(
                "subprocess.run",
                return_value=subprocess.CompletedProcess([], 0, stdout="1.1.0-14-g0aef3d886\n"),
            ) as mock_run,
        ):
            self.assertEqual(workspace_status.resolve_version(), "1.1.0-14-g0aef3d886")

        called_cmd = mock_run.call_args.args[0]
        self.assertEqual(called_cmd[:4], ["git", "describe", "--tags", "--dirty"])
        for glob in RELEASE_TAG_GLOBS:
            self.assertIn(glob, called_cmd)
        self.assertIn(PRE_RELEASE_TAG_GLOB, called_cmd)

    def test_git_describe_skips_nightly_and_prerelease_tags_in_real_repo(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            repo = Path(tmpdir)
            git_env = {
                **os.environ,
                "GIT_AUTHOR_NAME": "Test",
                "GIT_AUTHOR_EMAIL": "test@example.com",
                "GIT_COMMITTER_NAME": "Test",
                "GIT_COMMITTER_EMAIL": "test@example.com",
            }
            git_env.pop(workspace_status.ENV_VERSION_VAR, None)

            def run_git(*args: str) -> None:
                subprocess.run(
                    ["git", *args], cwd=repo, env=git_env, check=True, capture_output=True
                )

            run_git("init")
            (repo / "a.txt").write_text("v1\n", encoding="utf-8")
            run_git("add", "a.txt")
            run_git("commit", "-m", "initial")
            run_git("tag", "1.1.0")

            # Tag a newer commit with nightly and prerelease tags; both must be skipped.
            (repo / "b.txt").write_text("v2\n", encoding="utf-8")
            run_git("add", "b.txt")
            run_git("commit", "-m", "follow-up")
            run_git("tag", "nightly-2026-11-23.130")
            run_git("tag", "1.2.0-rc.1")

            with mock.patch.dict(os.environ, git_env, clear=True):
                version = workspace_status.resolve_version(cwd=repo)
            self.assertRegex(version, r"^1\.1\.0-1-g[0-9a-f]+$")
            self.assertLessEqual(len(f"xmc-{version}"), 63)

    def test_main_prints_fallback_when_git_unavailable(self) -> None:
        env = {k: v for k, v in os.environ.items() if k != workspace_status.ENV_VERSION_VAR}
        with (
            mock.patch.dict(os.environ, env, clear=True),
            mock.patch("subprocess.run", side_effect=FileNotFoundError("git not found")),
            mock.patch("sys.stdout", new_callable=io.StringIO) as out,
            mock.patch("sys.stderr", new_callable=io.StringIO) as err,
        ):
            workspace_status.main()
        self.assertEqual(
            out.getvalue(), f"STABLE_APP_VERSION {workspace_status.FALLBACK_VERSION}\n"
        )
        self.assertIn("Error determining version from git describe", err.getvalue())


if __name__ == "__main__":
    unittest.main()
