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

"""Unit tests for precommit_runner."""

from __future__ import annotations

import contextlib
import io
import os
import subprocess
import sys
import tempfile
import unittest
from collections.abc import Iterator
from pathlib import Path
from unittest import mock

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.lint import (  # noqa: E402
    precommit_deploy_checks as deploy_checks,
    precommit_gates as gates,
    precommit_runner as runner,
)


class LanguageChecksTest(unittest.TestCase):
    def test_check_bazel_invokes_buildifier(self):
        ctx = gates.RunContext(mode="fix")
        with (
            mock.patch.object(gates, "resolve_go_tool", return_value="/bin/buildifier"),
            mock.patch.object(gates, "git_ls_files", return_value=["BUILD.bazel"]),
            mock.patch.object(
                gates,
                "exec_cmd",
                side_effect=[
                    subprocess.CompletedProcess([], 0, "", ""),  # Google warning probe
                    subprocess.CompletedProcess([], 0, "", ""),  # fix pass
                    subprocess.CompletedProcess([], 0, "", ""),  # warn pass
                ],
            ) as exec_mock,
            mock.patch("sys.stdout", new_callable=io.StringIO),
        ):
            runner.check_bazel(ctx)
        self.assertFalse(ctx.failed)
        self.assertEqual(exec_mock.call_count, 3)
        self.assertTrue(exec_mock.call_args_list[0].kwargs["stdin_devnull"])
        self.assertIn(
            "-warnings=-LANG_proto_library-single-deps,-inline-deps",
            exec_mock.call_args_list[2].args[0],
        )

        ctx_oss = gates.RunContext(mode="check")
        with (
            mock.patch.object(gates, "resolve_go_tool", return_value="/bin/buildifier"),
            mock.patch.object(gates, "git_ls_files", return_value=["BUILD.bazel"]),
            mock.patch.object(
                gates,
                "exec_cmd",
                side_effect=[
                    subprocess.CompletedProcess([], 1, "", ""),  # OSS buildifier rejects flag
                    subprocess.CompletedProcess([], 0, "", ""),
                ],
            ) as exec_oss,
            mock.patch("sys.stdout", new_callable=io.StringIO),
        ):
            runner.check_bazel(ctx_oss)
        self.assertFalse(ctx_oss.failed)
        self.assertNotIn(
            "-warnings=-LANG_proto_library-single-deps,-inline-deps",
            exec_oss.call_args_list[1].args[0],
        )

    def test_check_go_runs_all_subchecks_in_check_mode(self):
        ctx = gates.RunContext(mode="check")
        with (
            mock.patch.object(runner.shutil, "which", return_value="/usr/bin/go"),
            mock.patch.object(gates, "resolve_go_tool", return_value="/bin/golangci-lint"),
            mock.patch.object(runner.Path, "is_dir", return_value=True),
            mock.patch.object(gates, "run_mode_command") as run_mode,
            mock.patch.object(
                gates,
                "exec_cmd",
                side_effect=[
                    subprocess.CompletedProcess([], 0, stdout="pkg/a.go\n", stderr=""),  # gofmt
                    subprocess.CompletedProcess([], 1, stdout="", stderr=""),  # determinism
                ],
            ),
            mock.patch("sys.stdout", new_callable=io.StringIO),
        ):
            runner.check_go(ctx)
        self.assertTrue(ctx.failed)
        self.assertEqual(run_mode.call_count, 2)

        ctx_win = gates.RunContext(mode="check")
        with (
            mock.patch.object(runner.shutil, "which", return_value=None),
            mock.patch.object(gates, "resolve_go_tool", return_value=None),
            mock.patch.object(runner.os, "sep", "\\"),
            mock.patch.object(
                gates,
                "exec_cmd",
                return_value=subprocess.CompletedProcess(
                    [], 0, stdout="pkg\\vendor\\a.go\n", stderr=""
                ),
            ),
            mock.patch("sys.stdout", new_callable=io.StringIO),
        ):
            runner.check_go(ctx_win)
        self.assertFalse(ctx_win.failed)

    def test_check_go_skips_determinism_without_go_and_chunks_gofmt(self):
        ctx_nogo = gates.RunContext(mode="check")
        with (
            mock.patch.object(runner.shutil, "which", return_value=None),
            mock.patch.object(gates, "resolve_go_tool", return_value=None),
            mock.patch.object(runner.Path, "is_dir", return_value=True),
            mock.patch.object(gates, "exec_cmd") as exec_mock,
            mock.patch("sys.stdout", new_callable=io.StringIO),
        ):
            runner.check_go(ctx_nogo)
        self.assertFalse(ctx_nogo.failed)
        exec_mock.assert_not_called()

        ctx_fix = gates.RunContext(mode="fix")
        many_go = [f"pkg/f{i}.go" for i in range(501)]
        with (
            mock.patch.object(
                runner.shutil,
                "which",
                side_effect=lambda name: "/usr/bin/gofmt" if name == "gofmt" else None,
            ),
            mock.patch.object(gates, "resolve_go_tool", return_value=None),
            mock.patch.object(gates, "collect_target_files", return_value=many_go) as collect_mock,
            mock.patch.object(
                gates, "exec_cmd", return_value=subprocess.CompletedProcess([], 0, "", "")
            ) as exec_fix,
            mock.patch("sys.stdout", new_callable=io.StringIO),
        ):
            runner.check_go(ctx_fix)
        # 2 gofmt -w chunks (500 + 1)
        collect_mock.assert_called_once()
        self.assertEqual(exec_fix.call_count, 2)
        self.assertEqual(len(exec_fix.call_args_list[0].args[0]), 503)
        self.assertEqual(len(exec_fix.call_args_list[1].args[0]), 4)

    def test_check_go_fails_on_gofmt_and_go_mod_tidy_errors_in_fix_mode(self):
        ctx = gates.RunContext(mode="fix")
        with (
            mock.patch.object(
                runner.shutil,
                "which",
                side_effect=lambda name: name if name in ("gofmt", "go") else None,
            ),
            mock.patch.object(gates, "resolve_go_tool", return_value=None),
            mock.patch.object(gates, "collect_target_files", return_value=["a.go"]),
            mock.patch.object(
                gates,
                "exec_cmd",
                side_effect=[
                    subprocess.CompletedProcess([], 1, stdout="", stderr=""),  # gofmt -w
                    subprocess.CompletedProcess([], 1, stdout="", stderr=""),  # determinism
                    subprocess.CompletedProcess([], 1, stdout="", stderr=""),  # go mod tidy
                ],
            ),
            mock.patch("sys.stdout", new_callable=io.StringIO),
        ):
            runner.check_go(ctx)
        self.assertTrue(ctx.failed)

    def test_check_ui_chunks_and_license(self):
        ctx = gates.RunContext(mode="fix")
        with (
            mock.patch.object(
                gates,
                "git_staged_paths",
                return_value=["cmd/ui/app.ts", "cmd/ui/index.html", "cmd/ui/s.scss"],
            ),
            mock.patch.object(gates, "staged_paths_match", side_effect=[False, True]),
            mock.patch.object(
                gates, "resolve_ui_binary", side_effect=["/p/prettier", "/p/eslint", "/p/tsc"]
            ),
            mock.patch.object(gates, "run_mode_command") as run_mode,
            mock.patch.object(
                gates, "exec_cmd", return_value=subprocess.CompletedProcess([], 0, "", "")
            ) as exec_mock,
            mock.patch("sys.stdout", new_callable=io.StringIO),
        ):
            runner.check_ui(ctx)
        self.assertFalse(ctx.failed)
        self.assertEqual(run_mode.call_count, 2)
        self.assertEqual(run_mode.call_args_list[0].kwargs["chunk_size"], 200)
        self.assertEqual(
            run_mode.call_args_list[0].kwargs["files"],
            ["cmd/ui/app.ts", "cmd/ui/index.html", "cmd/ui/s.scss"],
        )
        self.assertEqual(run_mode.call_args_list[1].args[1], ["/p/eslint"])
        self.assertEqual(run_mode.call_args_list[1].kwargs["cwd"], gates.REPO_ROOT / "cmd/ui")
        self.assertEqual(run_mode.call_args_list[1].kwargs["fix_args"], ["--fix"])
        self.assertEqual(run_mode.call_args_list[1].kwargs["files"], ["app.ts"])
        exec_mock.assert_called_once_with(
            ["npm", "run", "--silent", "typecheck"], cwd=gates.REPO_ROOT / "cmd/ui"
        )

        ctx_cfg = gates.RunContext(mode="fix")
        with (
            mock.patch.object(gates, "git_staged_paths", return_value=["cmd/ui/eslint.config.js"]),
            mock.patch.object(gates, "staged_paths_match", side_effect=[True, False]),
            mock.patch.object(gates, "resolve_ui_binary", return_value="/p/eslint"),
            mock.patch.object(gates, "run_mode_command") as run_mode_cfg,
            mock.patch("sys.stdout", new_callable=io.StringIO),
        ):
            runner.check_ui(ctx_cfg)
        self.assertEqual(run_mode_cfg.call_args.kwargs["files"], ["."])

        ctx_check = gates.RunContext(mode="check")
        with (
            mock.patch.object(gates, "resolve_go_tool", return_value="/bin/addlicense"),
            mock.patch.object(
                gates, "exec_cmd", return_value=subprocess.CompletedProcess([], 1, "", "")
            ),
            mock.patch("sys.stdout", new_callable=io.StringIO),
        ):
            runner.check_license(ctx_check)
        self.assertTrue(ctx_check.failed)

        ctx_fix_lic = gates.RunContext(mode="fix")
        with (
            mock.patch.object(gates, "resolve_go_tool", return_value="/bin/addlicense"),
            mock.patch.object(gates, "git_staged_paths", return_value=["tools/a.py"]),
            mock.patch.object(
                gates, "exec_cmd", return_value=subprocess.CompletedProcess([], 1, "", "")
            ),
            mock.patch("sys.stdout", new_callable=io.StringIO),
        ):
            runner.check_license(ctx_fix_lic)
        self.assertTrue(ctx_fix_lic.failed)

    def test_check_python_uses_repo_dot_in_check_and_fails_closed_in_fix(self):
        ctx = gates.RunContext(mode="check")
        with (
            mock.patch.object(
                gates,
                "resolve_local_or_docker",
                return_value=(["docker", "run", "ruff"], True),
            ),
            mock.patch.object(gates, "run_mode_command") as run_mode,
            mock.patch("sys.stdout", new_callable=io.StringIO),
        ):
            runner.check_python(ctx)
        self.assertEqual(run_mode.call_count, 2)
        self.assertIn("--no-cache", run_mode.call_args_list[0].args[1])
        self.assertEqual(run_mode.call_args_list[0].kwargs["files"], ["."])

        ctx_fix = gates.RunContext(mode="fix")
        with (
            mock.patch.object(gates, "collect_target_files", return_value=["a.py"]),
            mock.patch.object(gates, "resolve_local_or_docker", return_value=(["ruff"], False)),
            mock.patch.object(
                gates, "exec_cmd", return_value=subprocess.CompletedProcess([], 1, "", "")
            ),
            mock.patch("sys.stdout", new_callable=io.StringIO),
        ):
            runner.check_python(ctx_fix)
        self.assertTrue(ctx_fix.failed)

    def test_resolve_mypy_cmd_and_check_mypy(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            repo = Path(tmpdir)
            venv_mypy = repo / "tools/common/venv/bin/mypy"
            venv_mypy.parent.mkdir(parents=True)
            venv_mypy.write_text("#!/bin/sh\n", encoding="utf-8")
            with mock.patch.object(gates, "REPO_ROOT", repo):
                self.assertEqual(runner._resolve_mypy_cmd(), [str(venv_mypy)])

            venv_mypy.unlink()
            with (
                mock.patch.object(gates, "REPO_ROOT", repo),
                mock.patch.object(gates, "resolve_binary", return_value="/usr/local/bin/mypy"),
            ):
                self.assertEqual(runner._resolve_mypy_cmd(), ["/usr/local/bin/mypy"])

            with (
                mock.patch.object(gates, "REPO_ROOT", repo),
                mock.patch.object(gates, "resolve_binary", return_value=None),
                mock.patch.object(
                    gates, "exec_cmd", return_value=subprocess.CompletedProcess([], 1, "", "")
                ),
                mock.patch("sys.stdout", new_callable=io.StringIO),
            ):
                self.assertEqual(runner._resolve_mypy_cmd(), [])

        ctx_fix = gates.RunContext(mode="fix")
        with (
            mock.patch.object(gates, "staged_paths_match", return_value=False),
            mock.patch.object(runner, "_resolve_mypy_cmd") as resolve_mypy,
        ):
            runner.check_mypy(ctx_fix)
        resolve_mypy.assert_not_called()

        ctx_check = gates.RunContext(mode="check")
        with (
            mock.patch.object(runner, "_resolve_mypy_cmd", return_value=[]),
            mock.patch("sys.stdout", new_callable=io.StringIO),
        ):
            runner.check_mypy(ctx_check)
        self.assertTrue(ctx_check.failed)

    def test_check_workflows_fix_scope_and_success(self):
        ctx_skip = gates.RunContext(mode="fix")
        with (
            mock.patch.object(
                gates,
                "git_staged_paths",
                return_value=[".github/scripts/foo.js", "docs/.github/workflows/ci.yml"],
            ) as staged_mock,
            mock.patch.object(gates, "resolve_go_tool") as resolve_mock,
        ):
            runner.check_workflows(ctx_skip)
        staged_mock.assert_called_once_with("AMD", no_renames=True)
        resolve_mock.assert_not_called()

        ctx_empty = gates.RunContext(mode="check")
        with (
            mock.patch.object(gates, "git_ls_files", return_value=[]),
            mock.patch.object(gates, "resolve_go_tool") as empty_resolve,
        ):
            runner.check_workflows(ctx_empty)
        empty_resolve.assert_not_called()

        ctx_ok = gates.RunContext(mode="fix")
        with (
            mock.patch.object(
                gates, "git_staged_paths", return_value=[".github/actions/foo/action.yml"]
            ),
            mock.patch.object(
                gates, "resolve_go_tool", return_value="/bin/actionlint"
            ) as go_resolve,
            mock.patch.object(
                gates, "resolve_binary", return_value="/bin/shellcheck"
            ) as sc_resolve,
            mock.patch.object(
                gates, "exec_cmd", return_value=subprocess.CompletedProcess([], 0, "", "")
            ) as exec_mock,
            mock.patch("sys.stdout", new_callable=io.StringIO),
        ):
            runner.check_workflows(ctx_ok)
        self.assertFalse(ctx_ok.failed)
        go_resolve.assert_called_once_with(
            ctx_ok,
            "actionlint",
            "actionlint not found. Workflow linting skipped.",
            runner._ACTIONLINT_HINT,
        )
        sc_resolve.assert_called_once_with("shellcheck", runner._SHELLCHECK_EXTRA_DIRS)
        self.assertEqual(
            exec_mock.call_args.args[0], ["/bin/actionlint", "-shellcheck=/bin/shellcheck"]
        )
        self.assertEqual(exec_mock.call_args.kwargs["cwd"], gates.REPO_ROOT)

    def test_check_workflows_missing_tools_and_failures(self):
        ctx_missing_al = gates.RunContext(mode="fix")
        with (
            mock.patch.object(gates, "staged_paths_match", return_value=True),
            mock.patch.object(gates, "resolve_go_tool", return_value=None),
            mock.patch.object(gates, "exec_cmd") as exec_skip,
        ):
            runner.check_workflows(ctx_missing_al)
        exec_skip.assert_not_called()

        for mode, rc, expect_failed in (("fix", 0, False), ("check", 0, True), ("check", 1, True)):
            ctx = gates.RunContext(mode=mode)
            with (
                mock.patch.object(
                    gates, "git_ls_files", return_value=[".github/workflows/lint.yml"]
                ),
                mock.patch.object(gates, "staged_paths_match", return_value=True),
                mock.patch.object(gates, "resolve_go_tool", return_value="/bin/actionlint"),
                mock.patch.object(gates, "resolve_binary", return_value=None),
                mock.patch.object(
                    gates, "exec_cmd", return_value=subprocess.CompletedProcess([], rc, "", "")
                ) as exec_mock,
                mock.patch("sys.stdout", new_callable=io.StringIO) as buf,
            ):
                runner.check_workflows(ctx)
            self.assertEqual(ctx.failed, expect_failed)
            self.assertEqual(exec_mock.call_args.args[0], ["/bin/actionlint"])
            self.assertEqual(exec_mock.call_args.kwargs["cwd"], gates.REPO_ROOT)
            self.assertIn(runner._SHELLCHECK_HINT, buf.getvalue())

    def test_check_shell_staged_filtering_and_early_return(self):
        ctx_empty = gates.RunContext(mode="fix")
        with (
            mock.patch.object(gates, "git_staged_paths", return_value=["b.py"]) as staged_mock,
            mock.patch.object(gates, "resolve_binary") as sc_resolve,
            mock.patch.object(gates, "exec_cmd") as exec_skip,
        ):
            runner.check_shell(ctx_empty)
        staged_mock.assert_called_once_with("ACMR")
        sc_resolve.assert_not_called()
        exec_skip.assert_not_called()

        ctx_sh = gates.RunContext(mode="fix")
        with (
            mock.patch.object(
                gates, "git_staged_paths", return_value=["a.sh", "sub/b.bash", "c.py"]
            ) as staged_ok,
            mock.patch.object(gates, "resolve_binary", return_value="/bin/shellcheck"),
            mock.patch.object(
                gates, "exec_cmd", return_value=subprocess.CompletedProcess([], 0, "", "")
            ) as exec_mock,
            mock.patch("sys.stdout", new_callable=io.StringIO),
        ):
            runner.check_shell(ctx_sh)
        self.assertFalse(ctx_sh.failed)
        staged_ok.assert_called_once_with("ACMR")
        self.assertEqual(exec_mock.call_args.kwargs["cwd"], gates.REPO_ROOT)
        self.assertEqual(
            exec_mock.call_args.args[0],
            ["/bin/shellcheck", "-S", "style", "-e", "SC1091", "--", "a.sh", "sub/b.bash"],
        )

    def test_check_shell_chunking_failures_and_missing_binary(self):
        many_sh = [f"scripts/s{i}.sh" for i in range(501)]
        ctx_sh_fail = gates.RunContext(mode="check")
        with (
            mock.patch.object(gates, "git_ls_files", return_value=many_sh),
            mock.patch.object(gates, "resolve_binary", return_value="/bin/shellcheck"),
            mock.patch.object(
                gates, "exec_cmd", return_value=subprocess.CompletedProcess([], 1, "", "")
            ) as sh_fail_exec,
            mock.patch("sys.stdout", new_callable=io.StringIO),
        ):
            runner.check_shell(ctx_sh_fail)
        self.assertTrue(ctx_sh_fail.failed)
        self.assertEqual(sh_fail_exec.call_count, 2)
        self.assertEqual(
            sh_fail_exec.call_args_list[0].args[0][:6],
            ["/bin/shellcheck", "-S", "style", "-e", "SC1091", "--"],
        )

        ctx_sh_missing = gates.RunContext(mode="check")
        with (
            mock.patch.object(gates, "git_ls_files", return_value=["a.sh"]),
            mock.patch.object(gates, "resolve_binary", return_value=None),
            mock.patch("sys.stdout", new_callable=io.StringIO) as buf,
        ):
            runner.check_shell(ctx_sh_missing)
        self.assertTrue(ctx_sh_missing.failed)
        self.assertIn(runner._SHELLCHECK_HINT, buf.getvalue())

    def test_check_nonui_js(self):
        ctx_cfg = gates.RunContext(mode="fix")
        with (
            mock.patch.object(
                gates, "git_staged_paths", return_value=["tools/lint/eslint.nonui.config.mjs"]
            ),
            mock.patch.object(
                gates, "git_ls_files", return_value=[".github/scripts/a.js"]
            ) as cfg_ls,
            mock.patch.object(gates, "resolve_ui_binary", return_value="eslint"),
            mock.patch.object(
                gates, "exec_cmd", return_value=subprocess.CompletedProcess([], 0, "", "")
            ) as exec_mock,
            mock.patch("sys.stdout", new_callable=io.StringIO),
        ):
            runner.check_nonui_js(ctx_cfg)
        self.assertFalse(ctx_cfg.failed)
        cfg_ls.assert_called_once_with(runner._NONUI_JS_GLOBS)
        self.assertEqual(exec_mock.call_args.kwargs["cwd"], gates.REPO_ROOT)
        self.assertEqual(
            exec_mock.call_args.args[0],
            [
                "eslint",
                "--no-config-lookup",
                "-c",
                "tools/lint/eslint.nonui.config.mjs",
                "--fix",
                ".github/scripts/a.js",
            ],
        )

        ctx_chk = gates.RunContext(mode="check")
        with (
            mock.patch.object(
                gates, "git_ls_files", return_value=[".github/scripts/a.js"]
            ) as chk_ls,
            mock.patch.object(gates, "resolve_ui_binary", return_value="eslint"),
            mock.patch.object(
                gates, "exec_cmd", return_value=subprocess.CompletedProcess([], 0, "", "")
            ) as chk_exec,
            mock.patch("sys.stdout", new_callable=io.StringIO),
        ):
            runner.check_nonui_js(ctx_chk)
        self.assertFalse(ctx_chk.failed)
        chk_ls.assert_called_once_with(runner._NONUI_JS_GLOBS)
        self.assertEqual(chk_exec.call_args.kwargs["cwd"], gates.REPO_ROOT)
        self.assertEqual(
            chk_exec.call_args.args[0],
            [
                "eslint",
                "--no-config-lookup",
                "-c",
                "tools/lint/eslint.nonui.config.mjs",
                ".github/scripts/a.js",
            ],
        )

        ctx_fail = gates.RunContext(mode="fix")
        staged_paths = [
            ".github/scripts/a.js",
            "tools/nightly_tests/sub/b.js",
            "cmd/experiment_state_server/static/c.js",
            "cmd/ui/b.js",
        ]
        with (
            mock.patch.object(gates, "git_staged_paths", return_value=staged_paths),
            mock.patch.object(gates, "resolve_ui_binary", return_value="eslint"),
            mock.patch.object(
                gates, "exec_cmd", return_value=subprocess.CompletedProcess([], 1, "", "")
            ) as fail_exec,
            mock.patch("sys.stdout", new_callable=io.StringIO),
        ):
            runner.check_nonui_js(ctx_fail)
        self.assertTrue(ctx_fail.failed)
        self.assertEqual(fail_exec.call_args.kwargs["cwd"], gates.REPO_ROOT)
        self.assertEqual(
            fail_exec.call_args.args[0],
            [
                "eslint",
                "--no-config-lookup",
                "-c",
                "tools/lint/eslint.nonui.config.mjs",
                "--fix",
                ".github/scripts/a.js",
                "tools/nightly_tests/sub/b.js",
                "cmd/experiment_state_server/static/c.js",
            ],
        )

    def test_check_nonui_js_skips_and_missing_binary(self):
        ctx_skip = gates.RunContext(mode="fix")
        with (
            mock.patch.object(gates, "git_staged_paths", return_value=["cmd/ui/b.js", "README.md"]),
            mock.patch.object(gates, "resolve_ui_binary") as skip_resolve,
            mock.patch.object(gates, "exec_cmd") as skip_exec,
        ):
            runner.check_nonui_js(ctx_skip)
        self.assertFalse(ctx_skip.failed)
        skip_resolve.assert_not_called()
        skip_exec.assert_not_called()

        ctx_missing_chk = gates.RunContext(mode="check")
        with (
            mock.patch.object(gates, "git_ls_files", return_value=[".github/scripts/a.js"]),
            mock.patch.object(gates.shutil, "which", return_value=None),
            mock.patch.object(gates, "exec_cmd") as missing_chk_exec,
            mock.patch("sys.stdout", new_callable=io.StringIO),
        ):
            runner.check_nonui_js(ctx_missing_chk)
        self.assertTrue(ctx_missing_chk.failed)
        missing_chk_exec.assert_not_called()

        ctx_missing_fix = gates.RunContext(mode="fix")
        with (
            mock.patch.object(gates, "git_staged_paths", return_value=[".github/scripts/a.js"]),
            mock.patch.object(gates.shutil, "which", return_value=None),
            mock.patch.object(gates, "exec_cmd") as missing_fix_exec,
            mock.patch("sys.stdout", new_callable=io.StringIO),
        ):
            runner.check_nonui_js(ctx_missing_fix)
        self.assertFalse(ctx_missing_fix.failed)
        missing_fix_exec.assert_not_called()


class OrchestrationAndMainTest(unittest.TestCase):
    @contextlib.contextmanager
    def _patch_linter_phase_subchecks(self, *, fail_ctx: bool = False) -> Iterator[None]:
        def _maybe_fail(ctx: gates.RunContext) -> None:
            if fail_ctx:
                ctx.fail("lint")

        with contextlib.ExitStack() as stack:
            stack.enter_context(mock.patch.object(runner, "check_bazel", side_effect=_maybe_fail))
            for target, attr in (
                (runner, "check_go"),
                (runner, "check_ui"),
                (runner, "check_license"),
                (runner, "check_python"),
                (runner, "check_mypy"),
                (runner, "check_workflows"),
                (runner, "check_shell"),
                (runner, "check_nonui_js"),
                (deploy_checks, "check_deploy"),
            ):
                stack.enter_context(mock.patch.object(target, attr))
            stack.enter_context(mock.patch.object(gates, "run_python_gate"))
            yield

    def test_linter_phase_blocks_on_snapshot_fail_no_autostage_and_restage_fail(self):
        with (
            mock.patch.dict(os.environ, {}, clear=False),
            mock.patch.object(runner, "_autostage", return_value=False),
            mock.patch("sys.stdout", new_callable=io.StringIO) as snap_buf,
        ):
            self.assertFalse(runner._run_linter_phase("fix"))
        self.assertIn("Unable to snapshot", snap_buf.getvalue())

        with (
            mock.patch.dict(os.environ, {"XMC_PRECOMMIT_NO_AUTOSTAGE": "1"}, clear=False),
            self._patch_linter_phase_subchecks(),
            mock.patch.object(
                gates, "exec_cmd", return_value=subprocess.CompletedProcess([], 1, "", "")
            ),
            mock.patch("sys.stdout", new_callable=io.StringIO) as dirty_buf,
        ):
            self.assertFalse(runner._run_linter_phase("fix"))
        self.assertIn("Linters fixed formatting", dirty_buf.getvalue())

        with (
            mock.patch.dict(os.environ, {}, clear=False),
            self._patch_linter_phase_subchecks(),
            mock.patch.object(runner, "_autostage", side_effect=[True, False]) as auto_mock,
            mock.patch("sys.stdout", new_callable=io.StringIO) as buf,
        ):
            ok = runner._run_linter_phase("fix")
        self.assertFalse(ok)
        self.assertEqual(auto_mock.call_count, 2)
        self.assertIn("unstaged files", buf.getvalue())

    def test_linter_phase_run_modes_and_git_diff_error(self):
        with (
            mock.patch.dict(os.environ, {"XMC_PRECOMMIT_NO_AUTOSTAGE": "0"}, clear=False),
            self._patch_linter_phase_subchecks(),
            mock.patch.object(runner, "_autostage", return_value=True) as auto_mock,
            mock.patch("sys.stdout", new_callable=io.StringIO),
        ):
            self.assertTrue(runner._run_linter_phase("fix"))
        self.assertEqual([c.args[0] for c in auto_mock.call_args_list], ["snapshot", "restage"])

        with (
            self._patch_linter_phase_subchecks(),
            mock.patch.object(runner, "_autostage") as check_auto_mock,
            mock.patch("sys.stdout", new_callable=io.StringIO),
        ):
            self.assertTrue(runner._run_linter_phase("check"))
        check_auto_mock.assert_not_called()

        with (
            self._patch_linter_phase_subchecks(fail_ctx=True),
            mock.patch.object(runner, "_autostage") as fail_auto_mock,
            mock.patch("sys.stdout", new_callable=io.StringIO) as fail_buf,
        ):
            self.assertFalse(runner._run_linter_phase("check"))
        fail_auto_mock.assert_not_called()
        self.assertIn("Linter checks failed!", fail_buf.getvalue())

        with (
            mock.patch.dict(os.environ, {"XMC_PRECOMMIT_NO_AUTOSTAGE": "1"}, clear=False),
            self._patch_linter_phase_subchecks(),
            mock.patch.object(
                gates, "exec_cmd", return_value=subprocess.CompletedProcess([], 2, "", "")
            ),
            mock.patch("sys.stdout", new_callable=io.StringIO) as err_buf,
        ):
            self.assertFalse(runner._run_linter_phase("fix"))
        self.assertIn("git diff failed.", err_buf.getvalue())

    def test_test_phase_missing_bazel_docs_skip_and_lockfile(self):
        ctx_no_bazel_env = mock.patch.object(runner.shutil, "which", return_value=None)
        with ctx_no_bazel_env, mock.patch("sys.stdout", new_callable=io.StringIO):
            self.assertFalse(runner._run_test_phase("check"))

        with (
            mock.patch.object(gates, "git_staged_paths", return_value=["README.md", "docs/a.md"]),
            mock.patch.object(runner.shutil, "which") as which_mock,
            mock.patch("sys.stdout", new_callable=io.StringIO),
        ):
            self.assertTrue(runner._run_test_phase("fix"))
        which_mock.assert_not_called()

        with (
            mock.patch.object(runner.shutil, "which", return_value="/usr/bin/bazel"),
            mock.patch.object(
                gates,
                "exec_cmd",
                side_effect=[
                    subprocess.CompletedProcess([], 0, "", ""),  # lockfile clean
                    subprocess.CompletedProcess([], 0, "", ""),  # run_affected_tests
                    subprocess.CompletedProcess([], 0, "", ""),  # checkout lockfile
                ],
            ) as exec_mock,
            mock.patch("sys.stdout", new_callable=io.StringIO),
        ):
            self.assertTrue(runner._run_test_phase("check"))
        self.assertEqual(exec_mock.call_count, 3)

        with (
            mock.patch.object(runner.shutil, "which", return_value="/usr/bin/bazel"),
            mock.patch.object(
                gates,
                "exec_cmd",
                side_effect=[
                    subprocess.CompletedProcess([], 1, "", ""),  # lockfile already dirty
                    subprocess.CompletedProcess([], 1, "", ""),  # failing tests
                ],
            ) as fail_exec,
            mock.patch("sys.stdout", new_callable=io.StringIO),
        ):
            self.assertFalse(runner._run_test_phase("check"))
        self.assertEqual(fail_exec.call_count, 2)

    def test_main_check_flag_and_exit_codes(self):
        with (
            mock.patch.object(runner, "_run_linter_phase", return_value=True) as linter,
            mock.patch.object(runner, "_run_test_phase", return_value=True) as tests,
            mock.patch("sys.stdout", new_callable=io.StringIO),
        ):
            self.assertEqual(runner.main(["--check"]), 0)
        linter.assert_called_once_with("check")
        tests.assert_called_once_with("check")

        with (
            mock.patch.object(runner, "_run_linter_phase", return_value=False),
            mock.patch.object(runner, "_run_test_phase") as tests_skipped,
            mock.patch("sys.stdout", new_callable=io.StringIO),
        ):
            self.assertEqual(runner.main([]), 1)
        tests_skipped.assert_not_called()


if __name__ == "__main__":
    unittest.main()
