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

"""Tests for precommit_autostage."""

import argparse
import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.common.tool_utils import HandledError  # noqa: E402
from tools.lint import precommit_autostage as cli  # noqa: E402


class PlanRestageTest(unittest.TestCase):
    def test_classifies_paths(self):
        snapshot = cli.Snapshot(
            staged=frozenset({"staged.go", "partial.go"}),
            unstaged_hashes={"partial.go": "p0", "unstaged.go": "u0", "untouched.go": "t0"},
        )
        current = {"partial.go": "p1", "unstaged.go": "u1", "untouched.go": "t0"}
        modified = ["staged.go", "partial.go", "unstaged.go", "untouched.go", "clean.go"]

        plan = cli.plan_restage(snapshot, modified, current)

        self.assertEqual(plan.restage, ("staged.go",))
        self.assertEqual(plan.blocked, ("clean.go", "partial.go", "unstaged.go"))

    def test_deleted_unstaged_path_stays_ignored(self):
        snapshot = cli.Snapshot(staged=frozenset(), unstaged_hashes={"gone.go": None})

        plan = cli.plan_restage(snapshot, ["gone.go"], {"gone.go": None})

        self.assertEqual(plan, cli.RestagePlan(restage=(), blocked=()))

    def test_snapshot_json_round_trip(self):
        snapshot = cli.Snapshot(frozenset({"b", "a"}), {"c": "h", "d": None})

        self.assertEqual(cli.Snapshot.from_json(snapshot.to_json()), snapshot)


class GitRepoTest(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.repo = Path(tmp.name) / "repo"
        self.repo.mkdir()
        self.state = Path(tmp.name) / "state.json"
        self._git("init", "-q")
        self._git("config", "user.email", "test@example.com")
        self._git("config", "user.name", "Test")
        self._git("config", "commit.gpgsign", "false")
        for name in ("staged.py", "partial.py", "other.py", "clean.py"):
            self._write(name, "x = 1\n")
        self._git("add", ".")
        self._git("commit", "-q", "--no-verify", "-m", "init")

    def _git(self, *args: str) -> str:
        return subprocess.run(
            ["git", *args], cwd=self.repo, check=True, capture_output=True, text=True
        ).stdout

    def _write(self, name: str, content: str) -> None:
        (self.repo / name).write_text(content, encoding="utf-8")

    def _staged_content(self, name: str) -> str:
        return self._git("show", f":{name}")

    def _run(self, command: str) -> dict:
        args = argparse.Namespace(command=command, state=str(self.state), repo_root=str(self.repo))
        return cli.run(args)

    def test_restages_only_fully_staged_fixed_files(self):
        self._write("staged.py", "x=2\n")
        self._git("add", "staged.py")
        self._write("other.py", "y = 1\n")  # Unrelated unstaged work.

        self._run("snapshot")
        self._write("staged.py", "x = 2\n")  # Formatter fix.
        result = self._run("restage")

        self.assertEqual(result, {"restaged": ["staged.py"], "blocked": []})
        self.assertEqual(self._staged_content("staged.py"), "x = 2\n")
        self.assertEqual(self._staged_content("other.py"), "x = 1\n")

    def test_blocks_partially_staged_and_previously_clean_files(self):
        self._write("partial.py", "x=2\n")
        self._git("add", "partial.py")
        self._write("partial.py", "x=2\nz=3\n")  # Unstaged hunk on top.

        self._run("snapshot")
        self._write("partial.py", "x = 2\nz = 3\n")  # Formatter fix.
        self._write("clean.py", "x = 1  # fixed\n")  # Fixer touched an unstaged file.
        with self.assertRaises(HandledError) as ctx:
            self._run("restage")

        self.assertEqual(
            ctx.exception.details, {"restaged": [], "blocked": ["clean.py", "partial.py"]}
        )
        self.assertEqual(self._staged_content("partial.py"), "x=2\n")
        self.assertEqual(self._staged_content("clean.py"), "x = 1\n")

    def test_snapshot_writes_state(self):
        self._write("staged.py", "x = 2\n")
        self._git("add", "staged.py")
        self._write("other.py", "y = 1\n")

        result = self._run("snapshot")

        self.assertEqual(result, {"staged": 1, "unstaged": 1})
        data = json.loads(self.state.read_text(encoding="utf-8"))
        self.assertEqual(data["staged"], ["staged.py"])
        self.assertEqual(list(data["unstaged_hashes"]), ["other.py"])

    def test_restages_many_paths_literally(self):
        # Paths travel over stdin; "*" must not match sibling files as a glob.
        names = [f"many_{i:04d}_{'x' * 100}.py" for i in range(1000)] + ["glob*.py"]
        self._write("globber.py", "x=1\n")
        self._git("add", "globber.py")
        self._git("commit", "-q", "--no-verify", "-m", "add globber")
        for name in names:
            self._write(name, "x=1\n")
        self._git("add", *names)

        self._run("snapshot")
        for name in names + ["globber.py"]:
            self._write(name, "x = 1\n")
        with self.assertRaises(HandledError) as ctx:
            self._run("restage")

        details = ctx.exception.details
        assert details is not None
        self.assertEqual(details["blocked"], ["globber.py"])
        self.assertEqual(sorted(details["restaged"]), sorted(names))
        self.assertEqual(self._staged_content("glob*.py"), "x = 1\n")

    def test_restage_without_snapshot_raises_handled_error(self):
        with self.assertRaisesRegex(HandledError, "Cannot read pre-commit snapshot state"):
            self._run("restage")

    def test_restage_with_empty_snapshot_raises_handled_error(self):
        self.state.write_text("", encoding="utf-8")

        with self.assertRaisesRegex(HandledError, "Cannot read pre-commit snapshot state"):
            self._run("restage")

    def test_git_failure_raises_handled_error(self):
        args = argparse.Namespace(
            command="snapshot", state=str(self.state), repo_root=str(self.repo / "missing")
        )
        with self.assertRaisesRegex(HandledError, "Cannot run git") as ctx:
            cli.run(args)
        self.assertIn("XMC_PRECOMMIT_NO_AUTOSTAGE", ctx.exception.instruction)

    @unittest.skipUnless(sys.platform.startswith("linux"), "needs byte-string file names")
    def test_restages_non_utf8_path(self):
        name = os.fsdecode(b"bad\xff.py")
        self._write(name, "x = 1\n")
        self._git("add", name)
        self._run("snapshot")
        self._write(name, "x=2\n")

        self.assertEqual(self._run("restage"), {"restaged": [name], "blocked": []})
        self.assertEqual(self._staged_content(name), "x=2\n")


if __name__ == "__main__":
    unittest.main()
