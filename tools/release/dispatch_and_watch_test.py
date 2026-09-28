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
from unittest.mock import patch

from tools.common.tool_utils import HandledError
from tools.release.dispatch_and_watch import (
    create_parser,
    dispatch_and_watch,
)


class DispatchAndWatchTest(unittest.TestCase):
    """Focused test suite for dispatch_and_watch."""

    @patch("tools.release.dispatch_and_watch.shutil.which", return_value=None)
    def test_gh_missing(self, mock_which):
        p = create_parser().parse_args(["--workflow", "build.yml"])
        with self.assertRaises(HandledError):
            dispatch_and_watch(p)

    @patch("tools.release.dispatch_and_watch.shutil.which", return_value="/bin/gh")
    @patch("tools.release.dispatch_and_watch.run_command")
    def test_find_existing_success(self, mock_run, mock_which):
        run_list = json.dumps(
            [
                {
                    "databaseId": 101,
                    "headSha": "abc12345",
                    "url": "https://github.com/google/xmc/actions/runs/101",
                }
            ]
        )
        mock_run.return_value = subprocess.CompletedProcess(
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

    @patch("tools.release.dispatch_and_watch.shutil.which", return_value="/bin/gh")
    @patch("tools.release.dispatch_and_watch.run_command")
    def test_dispatch_and_watch_success(self, mock_run, mock_which):
        mock_run.side_effect = [
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

    @patch("tools.release.dispatch_and_watch.shutil.which", return_value="/bin/gh")
    @patch("tools.release.dispatch_and_watch.run_command")
    def test_dispatch_sha_mismatch(self, mock_run, mock_which):
        mock_run.side_effect = [
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

    @patch("tools.release.dispatch_and_watch.shutil.which", return_value="/bin/gh")
    @patch("tools.release.dispatch_and_watch.run_command")
    def test_dispatch_cancelled(self, mock_run, mock_which):
        mock_run.side_effect = [
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

    @patch("tools.release.dispatch_and_watch.shutil.which", return_value="/bin/gh")
    @patch("tools.release.dispatch_and_watch.run_command")
    def test_page_url_extraction(self, mock_run, mock_which):
        # 1. Direct page_url in view_data
        mock_run.side_effect = [
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
        mock_run.side_effect = [
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
        mock_run.side_effect = [
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
        mock_run.side_effect = [
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

    @patch("tools.release.dispatch_and_watch.shutil.which", return_value="/bin/gh")
    @patch("tools.release.dispatch_and_watch.run_command")
    def test_state_file_updates(self, mock_run, mock_which):
        with tempfile.TemporaryDirectory() as tmpdir:
            state_path = Path(tmpdir) / "release_state.json"
            # Pre-populate state file with existing field
            state_path.write_text(json.dumps({"existing_key": "val"}), encoding="utf-8")

            # Test successful watched run
            mock_run.side_effect = [
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
            mock_run.side_effect = [
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
            mock_run.side_effect = [
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
            mock_run.side_effect = [
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
            mock_run.side_effect = [
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
