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

"""Unit tests for tools.release.dispatch_and_watch."""

import json
import subprocess
import tempfile
import unittest
from pathlib import Path
from typing import Any
from unittest.mock import patch

from tools.common import tool_utils
from tools.common.tool_utils import HandledError
from tools.release.dispatch_and_watch import (
    create_parser,
    dispatch_and_watch,
)


class DispatchAndWatchTest(unittest.TestCase):
    """Focused test suite for dispatch_and_watch."""

    def setUp(self) -> None:
        super().setUp()
        self.repo_dir = Path(self.enterContext(tempfile.TemporaryDirectory()))
        wf = self.repo_dir / ".github" / "workflows" / "nightly-examples-e2e.yml"
        wf.parent.mkdir(parents=True)
        wf.write_text(
            "matrix:\n  example:\n    - id: simple_job\n    - id: resource_dashboard_ui\n",
            encoding="utf-8",
        )
        self.mock_run = self.enterContext(patch("tools.release.dispatch_and_watch.run_command"))

    @staticmethod
    def _proc(stdout: str = "") -> subprocess.CompletedProcess[str]:
        return subprocess.CompletedProcess(args=[], returncode=0, stdout=stdout, stderr="")

    def _watched_run_mocks(self, run_id: int, sha: str) -> list[subprocess.CompletedProcess[str]]:
        run = {"databaseId": run_id, "headSha": sha, "createdAt": "2099-01-01T00:00:00Z"}
        return [
            self._proc(),
            self._proc(json.dumps([run])),
            self._proc(),
            self._proc(json.dumps({"conclusion": "success", "jobs": []})),
        ]

    @patch("tools.common.tool_utils.subprocess.run", side_effect=FileNotFoundError("gh"))
    def test_gh_missing(self, _mock_subproc: Any) -> None:
        self.mock_run.side_effect = tool_utils.run_command
        p = create_parser().parse_args(["--workflow=build.yml", "--target-sha=abc1234"])
        with self.assertRaisesRegex(HandledError, "Cannot run gh"):
            dispatch_and_watch(p)

    def test_find_existing_success(self) -> None:
        run_list = json.dumps(
            [
                {
                    "databaseId": 101,
                    "headSha": "abc12345",
                    "url": "https://github.com/google/xmc/actions/runs/101",
                }
            ]
        )
        self.mock_run.return_value = subprocess.CompletedProcess(
            args=[], returncode=0, stdout=run_list, stderr=""
        )
        p = create_parser().parse_args(
            [
                "--workflow",
                "build.yml",
                "--find-existing",
                "--target-sha",
                "abc12345",
            ]
        )
        res = dispatch_and_watch(p)
        self.assertEqual(res["run_id"], 101)
        self.assertEqual(res["conclusion"], "success")
        self.assertEqual(res["page_url"], "")

    def test_dispatch_and_watch_success(self) -> None:
        self.mock_run.side_effect = [
            subprocess.CompletedProcess(args=[], returncode=0, stdout=""),  # dispatch
            subprocess.CompletedProcess(
                args=[],
                returncode=0,
                stdout=json.dumps(
                    [
                        {
                            "databaseId": 202,
                            "headSha": "target_sha",
                            "url": "run_url",
                            "createdAt": "2099-01-01T00:00:00Z",
                        }
                    ]
                ),
            ),  # list
            subprocess.CompletedProcess(args=[], returncode=0, stdout=""),  # watch
            subprocess.CompletedProcess(
                args=[],
                returncode=0,
                stdout=json.dumps({"conclusion": "success", "jobs": []}),
            ),  # view
        ]
        p = create_parser().parse_args(["--workflow", "build.yml", "--target-sha", "target_sha"])
        res = dispatch_and_watch(p)
        self.assertEqual(res["run_id"], 202)
        self.assertEqual(res["conclusion"], "success")
        self.assertEqual(res["page_url"], "")
        dispatch_cmd = self.mock_run.call_args_list[0].args[0]
        self.assertEqual(dispatch_cmd, ["gh", "workflow", "run", "build.yml", "--ref", "main"])

    def test_dispatch_forwards_inputs_and_validates_examples_filter(self) -> None:
        self.mock_run.side_effect = self._watched_run_mocks(210, "target_sha")
        argv = [
            "--workflow=nightly-examples-e2e.yml",
            "--ref=fix/nightly-example",
            "--target-sha=target_sha",
            f"--repo-dir={self.repo_dir}",
            "--input=examples_filter=SIMPLE_JOB,resource_dashboard_ui",
            "--input=xmanager_version=",
        ]
        res = dispatch_and_watch(create_parser().parse_args(argv))
        self.assertEqual(res["conclusion"], "success")
        expected = [
            *["gh", "workflow", "run", "nightly-examples-e2e.yml", "--ref", "fix/nightly-example"],
            "-f",
            "examples_filter=SIMPLE_JOB,resource_dashboard_ui",
            "-f",
            "xmanager_version=",
        ]
        self.assertEqual(self.mock_run.call_args_list[0].args[0], expected)

    def test_auto_resolves_remote_sha_when_target_sha_omitted(self) -> None:
        state_path = self.repo_dir / "verify_state.json"
        self.mock_run.side_effect = [
            self._proc(),
            self._proc("deadbeef1234\n"),
            *self._watched_run_mocks(220, "deadbeef12345678"),
        ]
        argv = [
            "--workflow=nightly-examples-e2e.yml",
            "--ref=fix/nightly-slug",
            f"--repo-dir={self.repo_dir}",
            "--input=examples_filter=",
            f"--state-file={state_path}",
        ]
        res = dispatch_and_watch(create_parser().parse_args(argv))
        self.assertEqual((res["run_id"], res["conclusion"]), (220, "success"))
        cmds = [c.args[0] for c in self.mock_run.call_args_list[:2]]
        self.assertEqual(
            cmds,
            [
                ["git", "fetch", "--", "origin", "fix/nightly-slug"],
                ["git", "rev-parse", "--verify", "FETCH_HEAD^{commit}"],
            ],
        )
        self.assertEqual(json.loads(state_path.read_text())["target_sha"], "deadbeef1234")

    @patch("tools.common.tool_utils.subprocess.run")
    def test_remote_sha_resolution_errors(self, mock_subproc: Any) -> None:
        self.mock_run.side_effect = tool_utils.run_command
        p = create_parser().parse_args(["--workflow=build.yml", "--ref=fix/unpushed"])
        mock_subproc.side_effect = FileNotFoundError("git")
        with self.assertRaisesRegex(HandledError, "Cannot run git"):
            dispatch_and_watch(p)

        git_err = subprocess.CalledProcessError(128, ["git"], stderr="missing ref")
        mock_subproc.side_effect = [git_err]
        with self.assertRaisesRegex(HandledError, "git fetch -- origin fix/unpushed"):
            dispatch_and_watch(p)

        mock_subproc.side_effect = [self._proc(), git_err]
        with self.assertRaisesRegex(HandledError, "git rev-parse"):
            dispatch_and_watch(p)

    def test_preflight_rejects_bad_ref_inputs_and_examples_filter(self) -> None:
        repo = f"--repo-dir={self.repo_dir}"
        bad_refs = ("", "origin/main", "-upload-pack", "a:b", "a..b", "a//b", "a/")
        bad_inputs = ("examples_filter", "1bad=v", "valid_key\n=v")
        bad_cases = [
            *(["--workflow=build.yml", f"--ref={r}"] for r in bad_refs),
            *(["--workflow=build.yml", f"--input={i}"] for i in bad_inputs),
            ["--workflow=build.yml", "--input=k=1", "--input=k=2"],
            ["--workflow=build.yml", "--find-existing", "--input=k=1"],
            ["--workflow=nightly-examples-e2e.yml", repo, "--input=examples_filter=bad_id"],
            ["--workflow=examples-smoke.yml", repo, "--input=examples_filter=simple_job,"],
            [
                "--workflow=nightly-examples-e2e.yml",
                "--repo-dir=/missing",
                "--input=examples_filter=simple_job",
            ],
        ]
        for argv in bad_cases:
            with self.subTest(argv=argv), self.assertRaises(HandledError):
                dispatch_and_watch(create_parser().parse_args(argv))
        self.mock_run.assert_not_called()

    def test_dispatch_sha_mismatch(self) -> None:
        self.mock_run.side_effect = [
            subprocess.CompletedProcess(args=[], returncode=0, stdout=""),  # dispatch
            subprocess.CompletedProcess(
                args=[],
                returncode=0,
                stdout=json.dumps(
                    [
                        {
                            "databaseId": 303,
                            "headSha": "wrong_sha",
                            "url": "run_url",
                            "createdAt": "2099-01-01T00:00:00Z",
                        }
                    ]
                ),
            ),  # list
        ]
        p = create_parser().parse_args(
            [
                "--workflow",
                "build.yml",
                "--target-sha",
                "expected_sha",
                "--poll-attempts",
                "1",
                "--poll-interval",
                "0",
            ]
        )
        res = dispatch_and_watch(p)
        self.assertEqual(res["conclusion"], "sha_mismatch")
        self.assertEqual(res["page_url"], "")

    def test_dispatch_cancelled(self) -> None:
        self.mock_run.side_effect = [
            subprocess.CompletedProcess(args=[], returncode=0, stdout=""),  # dispatch
            subprocess.CompletedProcess(
                args=[],
                returncode=0,
                stdout=json.dumps(
                    [
                        {
                            "databaseId": 404,
                            "headSha": "sha",
                            "url": "run_url",
                            "createdAt": "2099-01-01T00:00:00Z",
                        }
                    ]
                ),
            ),  # list
            subprocess.CompletedProcess(args=[], returncode=1, stdout=""),  # watch
            subprocess.CompletedProcess(
                args=[],
                returncode=0,
                stdout=json.dumps({"conclusion": "cancelled", "jobs": []}),
            ),  # view
        ]
        p = create_parser().parse_args(["--workflow", "build.yml", "--target-sha", "sha"])
        res = dispatch_and_watch(p)
        self.assertEqual(res["conclusion"], "cancelled")
        self.assertEqual(res["page_url"], "")

    def test_page_url_extraction(self) -> None:
        # 1. Direct page_url in view_data
        self.mock_run.side_effect = [
            subprocess.CompletedProcess(args=[], returncode=0, stdout=""),  # dispatch
            subprocess.CompletedProcess(
                args=[],
                returncode=0,
                stdout=json.dumps(
                    [
                        {
                            "databaseId": 501,
                            "headSha": "sha",
                            "url": "run_url",
                            "createdAt": "2099-01-01T00:00:00Z",
                        }
                    ]
                ),
            ),  # list
            subprocess.CompletedProcess(args=[], returncode=0, stdout=""),  # watch
            subprocess.CompletedProcess(
                args=[],
                returncode=0,
                stdout=json.dumps(
                    {
                        "conclusion": "success",
                        "page_url": "https://google.github.io/xmc/",
                        "jobs": [],
                    }
                ),
            ),  # view
        ]
        p = create_parser().parse_args(
            ["--workflow", "nightly-visual-snapshot.yml", "--target-sha", "sha"]
        )
        res = dispatch_and_watch(p)
        self.assertEqual(res["page_url"], "https://google.github.io/xmc/")

        # 2. Extracted from jobs step outputs
        self.mock_run.side_effect = [
            subprocess.CompletedProcess(args=[], returncode=0, stdout=""),  # dispatch
            subprocess.CompletedProcess(
                args=[],
                returncode=0,
                stdout=json.dumps(
                    [
                        {
                            "databaseId": 502,
                            "headSha": "sha",
                            "url": "run_url",
                            "createdAt": "2099-01-01T00:00:00Z",
                        }
                    ]
                ),
            ),  # list
            subprocess.CompletedProcess(args=[], returncode=0, stdout=""),  # watch
            subprocess.CompletedProcess(
                args=[],
                returncode=0,
                stdout=json.dumps(
                    {
                        "conclusion": "success",
                        "jobs": [
                            {
                                "name": "Deploy to GitHub Pages",
                                "steps": [
                                    {
                                        "name": "deploy",
                                        "outputs": {
                                            "page_url": "https://google.github.io/xmc/gallery/"
                                        },
                                    }
                                ],
                            }
                        ],
                    }
                ),
            ),  # view
        ]
        res = dispatch_and_watch(p)
        self.assertEqual(res["page_url"], "https://google.github.io/xmc/gallery/")

        # 3. Extracted from job environment URL
        self.mock_run.side_effect = [
            subprocess.CompletedProcess(args=[], returncode=0, stdout=""),  # dispatch
            subprocess.CompletedProcess(
                args=[],
                returncode=0,
                stdout=json.dumps(
                    [
                        {
                            "databaseId": 503,
                            "headSha": "sha",
                            "url": "run_url",
                            "createdAt": "2099-01-01T00:00:00Z",
                        }
                    ]
                ),
            ),  # list
            subprocess.CompletedProcess(args=[], returncode=0, stdout=""),  # watch
            subprocess.CompletedProcess(
                args=[],
                returncode=0,
                stdout=json.dumps(
                    {
                        "conclusion": "success",
                        "jobs": [
                            {
                                "name": "Deploy Gallery to GitHub Pages",
                                "environment": {"url": "https://google.github.io/xmc/env-gallery/"},
                            }
                        ],
                    }
                ),
            ),  # view
        ]
        res = dispatch_and_watch(p)
        self.assertEqual(res["page_url"], "https://google.github.io/xmc/env-gallery/")

        # 4. Extracted from serialized JSON string step outputs
        self.mock_run.side_effect = [
            subprocess.CompletedProcess(args=[], returncode=0, stdout=""),  # dispatch
            subprocess.CompletedProcess(
                args=[],
                returncode=0,
                stdout=json.dumps(
                    [
                        {
                            "databaseId": 504,
                            "headSha": "sha",
                            "url": "run_url",
                            "createdAt": "2099-01-01T00:00:00Z",
                        }
                    ]
                ),
            ),  # list
            subprocess.CompletedProcess(args=[], returncode=0, stdout=""),  # watch
            subprocess.CompletedProcess(
                args=[],
                returncode=0,
                stdout=json.dumps(
                    {
                        "conclusion": "success",
                        "jobs": [
                            {
                                "name": "Deploy",
                                "steps": [
                                    {
                                        "name": "deploy",
                                        "outputs": json.dumps(
                                            {"page_url": "https://google.github.io/xmc/str-out/"}
                                        ),
                                    }
                                ],
                            }
                        ],
                    }
                ),
            ),  # view
        ]
        res = dispatch_and_watch(p)
        self.assertEqual(res["page_url"], "https://google.github.io/xmc/str-out/")

    def test_state_file_updates(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            state_path = Path(tmpdir) / "release_state.json"
            # Pre-populate state file with existing field
            state_path.write_text(json.dumps({"existing_key": "val"}), encoding="utf-8")

            # Test successful watched run
            self.mock_run.side_effect = [
                subprocess.CompletedProcess(args=[], returncode=0, stdout=""),  # dispatch
                subprocess.CompletedProcess(
                    args=[],
                    returncode=0,
                    stdout=json.dumps(
                        [
                            {
                                "databaseId": 601,
                                "headSha": "sha601",
                                "url": "run_url",
                                "createdAt": "2099-01-01T00:00:00Z",
                            }
                        ]
                    ),
                ),  # list
                subprocess.CompletedProcess(args=[], returncode=0, stdout=""),  # watch
                subprocess.CompletedProcess(
                    args=[],
                    returncode=0,
                    stdout=json.dumps({"conclusion": "success", "jobs": []}),
                ),  # view
            ]
            p = create_parser().parse_args(
                [
                    "--workflow",
                    "build.yml",
                    "--target-sha",
                    "sha601",
                    "--state-file",
                    str(state_path),
                ]
            )
            res = dispatch_and_watch(p)

            state = json.loads(state_path.read_text(encoding="utf-8"))
            self.assertEqual(state["existing_key"], "val")
            self.assertEqual(state["target_sha"], "sha601")
            self.assertEqual(state["last_run_id"], 601)
            self.assertEqual(state["conclusion"], "success")

            # Test sha_mismatch outcome
            self.mock_run.side_effect = [
                subprocess.CompletedProcess(args=[], returncode=0, stdout=""),  # dispatch
                subprocess.CompletedProcess(
                    args=[],
                    returncode=0,
                    stdout=json.dumps(
                        [
                            {
                                "databaseId": 602,
                                "headSha": "other_sha",
                                "url": "run_url",
                                "createdAt": "2099-01-01T00:00:00Z",
                            }
                        ]
                    ),
                ),  # list
            ]
            p = create_parser().parse_args(
                [
                    "--workflow",
                    "build.yml",
                    "--target-sha",
                    "expected_sha",
                    "--state-file",
                    str(state_path),
                    "--poll-attempts",
                    "1",
                    "--poll-interval",
                    "0",
                ]
            )
            res = dispatch_and_watch(p)

            state = json.loads(state_path.read_text(encoding="utf-8"))
            self.assertEqual(state["target_sha"], "expected_sha")
            self.assertEqual(state["last_run_id"], 602)
            self.assertEqual(state["conclusion"], "sha_mismatch")

            # Test find_existing outcome
            self.mock_run.side_effect = [
                subprocess.CompletedProcess(
                    args=[],
                    returncode=0,
                    stdout=json.dumps([{"databaseId": 603, "headSha": "sha603", "url": "run_url"}]),
                ),
            ]
            p = create_parser().parse_args(
                [
                    "--workflow",
                    "build.yml",
                    "--find-existing",
                    "--target-sha",
                    "sha603",
                    "--state-file",
                    str(state_path),
                ]
            )
            res = dispatch_and_watch(p)

            state = json.loads(state_path.read_text(encoding="utf-8"))
            self.assertEqual(state["target_sha"], "sha603")
            self.assertEqual(state["last_run_id"], 603)
            self.assertEqual(state["conclusion"], "success")

            # Test failure outcome persistence
            self.mock_run.side_effect = [
                subprocess.CompletedProcess(args=[], returncode=0, stdout=""),  # dispatch
                subprocess.CompletedProcess(
                    args=[],
                    returncode=0,
                    stdout=json.dumps(
                        [
                            {
                                "databaseId": 604,
                                "headSha": "sha604",
                                "url": "run_url",
                                "createdAt": "2099-01-01T00:00:00Z",
                            }
                        ]
                    ),
                ),  # list
                subprocess.CompletedProcess(args=[], returncode=1, stdout=""),  # watch
                subprocess.CompletedProcess(
                    args=[],
                    returncode=0,
                    stdout=json.dumps({"conclusion": "failure", "jobs": []}),
                ),  # view
            ]
            p = create_parser().parse_args(
                [
                    "--workflow",
                    "build.yml",
                    "--target-sha",
                    "sha604",
                    "--state-file",
                    str(state_path),
                ]
            )
            res = dispatch_and_watch(p)
            self.assertEqual(res["conclusion"], "failure")
            state = json.loads(state_path.read_text(encoding="utf-8"))
            self.assertEqual(state["target_sha"], "sha604")
            self.assertEqual(state["last_run_id"], 604)
            self.assertEqual(state["conclusion"], "failure")

            # Test cancelled outcome persistence
            self.mock_run.side_effect = [
                subprocess.CompletedProcess(args=[], returncode=0, stdout=""),  # dispatch
                subprocess.CompletedProcess(
                    args=[],
                    returncode=0,
                    stdout=json.dumps(
                        [
                            {
                                "databaseId": 605,
                                "headSha": "sha605",
                                "url": "run_url",
                                "createdAt": "2099-01-01T00:00:00Z",
                            }
                        ]
                    ),
                ),  # list
                subprocess.CompletedProcess(args=[], returncode=1, stdout=""),  # watch
                subprocess.CompletedProcess(
                    args=[],
                    returncode=0,
                    stdout=json.dumps({"conclusion": "cancelled", "jobs": []}),
                ),  # view
            ]
            p = create_parser().parse_args(
                [
                    "--workflow",
                    "build.yml",
                    "--target-sha",
                    "sha605",
                    "--state-file",
                    str(state_path),
                ]
            )
            res = dispatch_and_watch(p)
            self.assertEqual(res["conclusion"], "cancelled")
            state = json.loads(state_path.read_text(encoding="utf-8"))
            self.assertEqual(state["target_sha"], "sha605")
            self.assertEqual(state["last_run_id"], 605)
            self.assertEqual(state["conclusion"], "cancelled")


if __name__ == "__main__":
    unittest.main()
