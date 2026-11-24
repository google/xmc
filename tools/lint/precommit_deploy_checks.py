#!/usr/bin/env python3
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

"""Terraform, Helm, and Trivy pre-commit checks under `deploy/` and `test/helm/`."""

from __future__ import annotations

import argparse
import os
import shutil
import sys
from collections.abc import Sequence
from pathlib import Path

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.lint import precommit_gates as gates  # noqa: E402


def check_terraform(ctx: gates.RunContext) -> None:
    """Runs `terraform fmt` locally or via Docker across `deploy/`."""
    files = gates.collect_target_files(
        ctx.mode, staged_regex=r"\.tf$", roots=("deploy",), suffixes=(".tf",)
    )
    if not files:
        return
    resolved = gates.resolve_local_or_docker(
        ctx,
        "terraform",
        docker_image="hashicorp/terraform:latest",
        mount="/workspace",
        missing_label="Terraform formatting check skipped.",
    )
    if not resolved:
        return
    print("--> Running Terraform Formatter (terraform fmt)...")
    gates.run_mode_command(
        ctx,
        resolved[0],
        check_args=["fmt", "-check", *files],
        fix_args=["fmt", *files],
        failure_msg="Terraform formatting check failed.",
        ignore_fix_error=True,
        cwd=gates.REPO_ROOT,
    )


def check_tflint(ctx: gates.RunContext) -> None:
    """Runs `tflint` locally or via Docker across `deploy/`."""
    if not gates.should_run_scope(
        ctx.mode, roots=("deploy",), suffixes=(".tf",), fix_pattern=r"\.tf$|(^|/)\.tflint\.hcl$"
    ):
        return
    resolved = gates.resolve_local_or_docker(
        ctx,
        "tflint",
        docker_image="ghcr.io/terraform-linters/tflint:latest",
        mount="/data",
        missing_label="TFLint check skipped.",
        entrypoint="sh",
    )
    if not resolved:
        return
    print("--> Running Terraform Linter (TFLint)...")
    base, is_docker = resolved
    if is_docker:
        cmd = [*base, "-c", "tflint --init >/dev/null && tflint --recursive"]
    else:
        gates.exec_cmd([*base, "--init"], cwd=gates.REPO_ROOT, quiet_out=True, quiet_err=True)
        cmd = [*base, "--recursive"]
    if gates.exec_cmd(cmd, cwd=gates.REPO_ROOT).returncode != 0:
        ctx.fail("TFLint check failed.")


def _run_helm_lint(ctx: gates.RunContext) -> None:
    if helm_bin := shutil.which("helm"):
        print("--> Running Helm Chart Linter (helm lint)...")
        if gates.exec_cmd([helm_bin, "lint", "deploy/helm"], cwd=gates.REPO_ROOT).returncode != 0:
            ctx.fail("Helm lint failed.")
    else:
        ctx.warn_missing("helm not found. Helm chart linting skipped.")


def _run_helm_unit_tests(ctx: gates.RunContext) -> None:
    print("--> Running Helm Chart & Blueprint Unit Tests (test/helm)...")
    test_helm_dir = str(gates.REPO_ROOT / "test" / "helm")
    py_path = os.environ.get("PYTHONPATH")
    env = {
        **os.environ,
        "PYTHONPATH": f"{test_helm_dir}{os.pathsep}{py_path}" if py_path else test_helm_dir,
    }
    cmd = [
        sys.executable,
        "-m",
        "unittest",
        "discover",
        "--start-directory",
        test_helm_dir,
        "--pattern",
        "*_test.py",
    ]
    if gates.exec_cmd(cmd, cwd=gates.REPO_ROOT, env=env).returncode != 0:
        ctx.fail("Helm chart & blueprint unit tests failed.")


def check_helm(ctx: gates.RunContext) -> None:
    """Runs `helm lint` and `test/helm` unit tests."""
    if not gates.should_run_scope(
        ctx.mode,
        roots=("deploy/helm", "deploy/profiles/stable-2026b/components/hpo", "test/helm"),
        suffixes=(".yaml", ".yml", "_test.py"),
        fix_pattern=(
            r"^(deploy/helm/|deploy/profiles/stable-2026b/components/hpo/"
            r"|deploy/ctk_v2/xmanager-cloud\.yaml$|test/helm/)"
        ),
    ):
        return
    if ctx.mode == "check" or gates.staged_paths_match("AMD", r"^deploy/helm/"):
        _run_helm_lint(ctx)
    _run_helm_unit_tests(ctx)


def check_security(ctx: gates.RunContext) -> None:
    """Runs Trivy IaC config scanning across `deploy/`."""
    if not gates.should_run_scope(
        ctx.mode,
        roots=("deploy",),
        suffixes=(".tf", ".tfvars", ".yaml", ".yml"),
        fix_pattern=r"^deploy/",
    ):
        return
    resolved = gates.resolve_local_or_docker(
        ctx,
        "trivy",
        docker_image="aquasec/trivy:latest",
        mount="/src",
        missing_label="Security check skipped.",
    )
    if not resolved:
        return
    print("--> Running Infrastructure & Workload Security Scanner (Trivy)...")
    # TODO: Add "--exit-code", "1" once deploy/ manifests are verified clean under it.
    cmd = [*resolved[0], "config", "--skip-check-update", "--severity", "HIGH,CRITICAL", "deploy/"]
    if gates.exec_cmd(cmd, cwd=gates.REPO_ROOT).returncode != 0:
        ctx.fail("Trivy security check failed.")


def check_deploy(ctx: gates.RunContext) -> None:
    """Runs all infrastructure, Helm, and security pre-commit gates."""
    check_terraform(ctx)
    check_tflint(ctx)
    check_helm(ctx)
    check_security(ctx)


def main(argv: Sequence[str] | None = None) -> int:
    """CLI entrypoint for running infrastructure, Helm, and security gates."""
    parser = argparse.ArgumentParser(description="Run deploy/ infrastructure pre-commit checks.")
    parser.add_argument("--mode", choices=("fix", "check"), default="fix")
    args = parser.parse_args(list(argv) if argv is not None else None)
    ctx = gates.RunContext(mode=args.mode)
    check_deploy(ctx)
    return 1 if ctx.failed else 0


if __name__ == "__main__":
    raise SystemExit(main())
