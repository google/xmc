# Nightly Examples E2E Verification Suite

This directory contains the end-to-end (E2E) verification suite for runnable workloads under [`examples/`](../../examples/) as well as weekly UI visual snapshot validators.

Every runnable example under `examples/<example_name>/` is executed nightly against a live XMC GKE environment by [`.github/workflows/nightly-examples-e2e.yml`](../../.github/workflows/nightly-examples-e2e.yml) and verified by a corresponding `tools/nightly_tests/examples_verification/<example_name>_verify.py` script.

---

## 1. Architecture & Execution Lifecycle

To maintain high cohesion and clear separation of concerns, `tools/nightly_tests/` is organized into two primary subdirectories:

1. **`verification/` (`//tools/nightly_tests/verification:*`)**:
   Shared, reusable verification primitives, session/API stubs, and UI validators (e.g. `verifier_cli.py`, `workload_verification.py`, `xmc_entities_validator.py`, `ui_entities_validator.py`). Every shared helper is paired with a 1-to-1 `*_test.py` unit test suite.
2. **`examples_verification/` (`//tools/nightly_tests/examples_verification:*`)**:
   Concrete end-to-end verification scripts for individual runnable workloads under `examples/` and CUJs (e.g. `simple_job_verify.py`, `clickhouse_metrics_job_verify.py`, `metrics_dashboard_ui_verify.py`, `resource_observability_dashboard_ui_verify.py`).

The Nightly Examples E2E workflow ([`.github/workflows/nightly-examples-e2e.yml`](../../.github/workflows/nightly-examples-e2e.yml)) runs in three stages:

1. **Build Example Container Images (`build-example-images`)**:
   Builds and pushes custom container images for examples that include a `Dockerfile` (`examples/<example_name>/Dockerfile`) to Artifact Registry (`us-docker.pkg.dev/${PROJECT_ID}/xmc/<image_name>:latest`).
2. **Launch & Verify Matrix (`run-and-verify-examples`)**:
   For each matrix entry:
   - Installs the XMC CLI, Python SDKs (`sdks/python/xmc`, `sdks/python/xdash`), and optional example `requirements.txt`.
   - Mints an OIDC identity token (`XMC_AUTH_TOKEN`) and starts health-checked `kubectl port-forward` tunnels to internal services (`dashboard-service:50051` and `clickhouse:8123`) when `requires_clickhouse: true` is set.
   - Executes the example entrypoint script (`launch.py` or `create_dashboard.py`) and extracts the created Experiment XID (or Dashboard ID) from stdout.
   - Runs the example's verification module via `python3 -m tools.nightly_tests.examples_verification.<example_name>_verify --xid="${XID}" --endpoint="${XMANAGER_ENDPOINT}" --expected_queue="${EXPECTED_QUEUE}"`.
3. **Aggregate Results (`aggregate-results`)**:
   Combines per-example JSON status artifacts into a unified workflow summary and alerts repository owners if any scheduled run on `main` fails.

### Shared Verification Modules (`tools/nightly_tests/verification/`)

- [`verification/`](verification/) (`//tools/nightly_tests/verification:*`): Domain-focused verification modules (each paired with a 1-to-1 `*_test.py` unit test suite):
  - [`verification/verifier_cli.py`](verification/verifier_cli.py) (`//tools/nightly_tests/verification:verifier_cli`, tested by [`verification/verifier_cli_test.py`](verification/verifier_cli_test.py)): Defines standard CLI flags (`--xid`, `--endpoint`, `--expected_queue`, `--timeout_sec`, `--dashboard_endpoint`, `--expected_work_units`, `--clickhouse_endpoint`, `--require_clickhouse_metrics`, `--dashboard_id`, `--expected_title_pattern`), flag retrieval (`get_flag`), argument validation (`validate_argv`), XID normalization (`parse_xid`), work unit count resolution (`resolve_expected_work_units`), and the CLI entrypoint wrapper (`run_main`).
  - [`verification/endpoint_resolution.py`](verification/endpoint_resolution.py) (`//tools/nightly_tests/verification:endpoint_resolution`, tested by [`verification/endpoint_resolution_test.py`](verification/endpoint_resolution_test.py)): Resolves ESS gRPC (`resolve_endpoint`), Dashboard Service REST (`resolve_dashboard_endpoint`), ClickHouse HTTP (`resolve_clickhouse_endpoint`), and Web UI (`resolve_ui_base_url`) endpoints from flags, environment variables, or local defaults.
  - [`verification/ess_session.py`](verification/ess_session.py) (`//tools/nightly_tests/verification:ess_session`, tested by [`verification/ess_session_test.py`](verification/ess_session_test.py)): Provides context managers for authenticated ESS gRPC channel lifecycle (`ess_channel_context`) and standardized verification failure exit handling (`handle_verification_errors`).
  - [`verification/verification_report.py`](verification/verification_report.py) (`//tools/nightly_tests/verification:verification_report`, tested by [`verification/verification_report_test.py`](verification/verification_report_test.py)): Formats console verification header banners (`print_banner`) and completion summaries (`print_success_summary`).
  - [`verification/workload_verification.py`](verification/workload_verification.py) (`//tools/nightly_tests/verification:workload_verification`, tested by [`verification/workload_verification_test.py`](verification/workload_verification_test.py)): Implements high-level workload and dashboard verification orchestrators (`verify_standard_workload`, `verify_dashboard_workload`) and step assertion helpers (`assert_experiment`, `assert_work_units`, `assert_work_unit_jobsets`, `assert_work_unit_artifacts`, `assert_clickhouse_metrics`, `assert_dashboard_chart_data`, `assert_dashboard`).
  - [`verification/xmc_entities_validator.py`](verification/xmc_entities_validator.py) (`//tools/nightly_tests/verification:xmc_entities_validator_lib`, tested by [`verification/xmc_entities_validator_test.py`](verification/xmc_entities_validator_test.py)): Implements underlying ESS gRPC, ClickHouse HTTP SQL, and Dashboard Service REST/gRPC polling and validation assertions (`validate_experiment`, `validate_work_units`, `validate_work_unit_jobsets`, `validate_node_selector_mappings`, `validate_artifacts`, `validate_clickhouse_metrics`, `validate_dashboard`, `validate_dashboard_chart_data`, `validate_multikueue_execution_metadata`, `validate_multikueue_k8s_delegation`, `stop_work_units`).
  - [`verification/ui_entities_validator.py`](verification/ui_entities_validator.py) & [`verification/ui_css_selectors.py`](verification/ui_css_selectors.py) (`//tools/nightly_tests/verification:ui_entities_validator_lib`, tested by [`verification/ui_entities_validator_test.py`](verification/ui_entities_validator_test.py)): Provide authenticated Playwright browser sessions (`XMCUIBrowserSession`), route navigation, CSS selectors, and UI runtime error gates (`navigate_to_route`, `assert_no_runtime_errors`).

---

## 2. Step-by-Step Guide: Adding a Verification Suite for a New Example

When adding a new example under `examples/<example_name>/`, follow these four steps to wire its nightly E2E verification suite.

### Step 1: Create `tools/nightly_tests/examples_verification/<example_name>_verify.py`

Choose the verification pattern that matches your example's runtime behavior:

#### Pattern A: Standard CPU Workload (Completes on CPU Queue)

Use `workload_verification.verify_standard_workload` for workloads that schedule onto the default `cpu-queue` and run to completion. Provide an optional `extra_step` callback if the workload logs metrics to ClickHouse, provisions dashboards, or requires custom JobSet spec assertions:

```python
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

"""Validation script for <example_name> workload in XMC."""

from __future__ import annotations

from collections.abc import Sequence
from typing import Any

from tools.nightly_tests.verification import verifier_cli, workload_verification

EXPECTED_TITLE_PATTERN: str = "My Example Experiment"
EXPECTED_WORKER_NAMES: tuple[str, ...] = ("worker",)
EXPECTED_JOBSET_SPECS: list[dict[str, Any]] = [
    {
        "name": "worker",
        "replicas": 1,
        "primary_container": "worker",
    },
]


def main(argv: Sequence[str]) -> None:
    """Main verification routine for <example_name> workload."""
    workload_verification.verify_standard_workload(
        argv,
        banner_title="XMC My Example Verification & Assertions",
        summary_title="ALL MY EXAMPLE ASSERTIONS PASSED SUCCESSFULLY!",
        expected_title_pattern=EXPECTED_TITLE_PATTERN,
        expected_jobs=EXPECTED_JOBSET_SPECS,
        expected_worker_names=EXPECTED_WORKER_NAMES,
    )


if __name__ == "__main__":
    verifier_cli.run_main(main)
```

If your example also writes artifacts, ClickHouse metrics, or dashboards, pass `expected_artifact_types`, `expected_metric_names`, and an `extra_step` callback (see [`chart_artifact_job_verify.py`](examples_verification/chart_artifact_job_verify.py) or [`metrics_dashboard_job_verify.py`](examples_verification/metrics_dashboard_job_verify.py)).

#### Pattern B: Hardware-Gated Workload (GPU / TPU / Specialized Node Pools)

The nightly E2E cluster runs a CPU-only node pool (`cpu-queue`). If an example requests GPU/TPU accelerators, specialized node selectors (`gpu-pool`), or TPU queues (`tpu-queue`), pods will remain `PENDING` (unschedulable) on the CPU cluster.

Pass `wait_for_completion=False` to `workload_verification.verify_standard_workload`. This verifies that the Experiment, WorkUnit, Queue routing, JobSet topology, and custom node selectors/artifacts are persisted accurately by the control plane while `PENDING` or `RUNNING`, and automatically stops the WorkUnits in a `finally` block via `BatchStopWorkUnits` so pending jobs do not accumulate in Kueue:

```python
def _verify_node_selectors(stub: Any, xid: str) -> None:
    """Validates per-job nodeSelector constraints on the WorkUnit JobSet spec."""
    xmc_entities_validator.validate_node_selector_mappings(
        stub,
        xid,
        {"head": {"cloud.google.com/gke-nodepool": "cpups"}},
    )


def main(argv: Sequence[str]) -> None:
    """Main verification routine for hardware-gated workload."""
    workload_verification.verify_standard_workload(
        argv,
        banner_title="XMC Hardware-Gated Example Verification",
        summary_title="ALL HARDWARE-GATED EXAMPLE ASSERTIONS PASSED!",
        expected_title_pattern=EXPECTED_TITLE_PATTERN,
        expected_jobs=EXPECTED_JOBSET_SPECS,
        expected_worker_names=EXPECTED_WORKER_NAMES,
        extra_step=_verify_node_selectors,
        wait_for_completion=False,
    )
```

#### Pattern C: Standalone Dashboard or Control-Plane API Script

For examples that provision standalone resources (such as `xdash` dashboards) rather than launching an Experiment XID, use `workload_verification.verify_dashboard_workload` (which accepts `--dashboard_id` or `--xid` from the workflow runner):

```python
def main(argv: Sequence[str]) -> None:
    """Main verification routine for standalone dashboard example."""
    workload_verification.verify_dashboard_workload(
        argv,
        banner_title="XMC Dashboard Example Verification",
        summary_title="ALL DASHBOARD EXAMPLE ASSERTIONS PASSED!",
        default_dashboard_id="my_dashboard_id",
        expected_title_pattern="My Dashboard Title",
        expected_min_charts=2,
    )
```

### Step 2: Register the Target in `tools/nightly_tests/examples_verification/BUILD.bazel`

1. Add `"<example_name>_verify.py"` to `exports_files([...])` in [`tools/nightly_tests/examples_verification/BUILD.bazel`](examples_verification/BUILD.bazel).
2. Add a `py_binary` target with `name = "<example_name>_verify"` (include `"//tools/nightly_tests/verification:xmc_entities_validator_lib"` in `deps` if your script invokes `xmc_entities_validator` directly):

```starlark
py_binary(
    name = "<example_name>_verify",
    srcs = ["<example_name>_verify.py"],
    imports = ["../../.."],
    deps = [
        "//tools/nightly_tests/verification:verifier_cli",
        "//tools/nightly_tests/verification:workload_verification",
    ],
)
```

### Step 3: Wire the Example in `.github/workflows/nightly-examples-e2e.yml`

1. **Container Image (if applicable)**: If your example includes `examples/<example_name>/Dockerfile`, add a `build_and_push` call in the `build-example-images` job of [`.github/workflows/nightly-examples-e2e.yml`](../../.github/workflows/nightly-examples-e2e.yml).
2. **Matrix Entry**: Add an entry under `jobs.run-and-verify-examples.strategy.matrix.example`:

```yaml
          - id: '<example_name>'
            name: 'Human-Readable Example Name'
            example_path: 'examples/<example_name>'
            entrypoint: 'launch.py'
            args: ''
            verify_module: 'tools.nightly_tests.examples_verification.<example_name>_verify'
            expected_queue: "${{ inputs.queue_name || 'cpu-queue' }}"
            requires_clickhouse: false
```

Ensure your example's `entrypoint` script prints `Experiment XID: <xid>` (or `experiments/<xid>` / `dashboards/<id>`) to stdout so the workflow launch step can extract the identifier automatically.

### Step 4: Verify Locally and in CI

Run the Bazel build, unit tests, and example coverage gate locally:

```bash
EXAMPLE_TARGET="simple_job_verify"
bazel build "//tools/nightly_tests/examples_verification:${EXAMPLE_TARGET}"
bazel test //tools/nightly_tests/...
python3 tools/nightly_tests/check_examples_coverage.py
./.githooks/pre-commit --check
```

To trigger a targeted E2E run on your feature branch in GitHub Actions without running the entire matrix:

```bash
BRANCH_NAME="feat/my-new-example"
EXAMPLE_ID="my_new_example"
gh workflow run nightly-examples-e2e.yml --ref "${BRANCH_NAME}" -f examples_filter="${EXAMPLE_ID}"
```

To build and launch the example workloads without running verification assertions (`skip_verification=true`):

```bash
gh workflow run nightly-examples-e2e.yml --ref "${BRANCH_NAME}" -f examples_filter="${EXAMPLE_ID}" -f skip_verification=true
```

---

## 3. Automated Example Coverage Gate & Explicit Opt-Out Policy

To ensure new examples do not silently lack E2E verification, [`check_examples_coverage.py`](check_examples_coverage.py) enforces a repository gate in [`.githooks/pre-commit`](../../.githooks/pre-commit) and in [`.github/workflows/lint.yml`](../../.github/workflows/lint.yml) (on PRs touching `examples/**`, `tools/nightly_tests/**`, or `.github/workflows/nightly-examples-e2e.yml`, as well as via `bazel test //tools/nightly_tests:check_examples_coverage_test`).

### What the Gate Checks

For every subdirectory `examples/<example_name>/`, the gate verifies that all three of the following exist:

1. **Verifier script**: `tools/nightly_tests/examples_verification/<example_name>_verify.py`
2. **Bazel target**: `py_binary(name = "<example_name>_verify", ...)` in `tools/nightly_tests/examples_verification/BUILD.bazel`
3. **Workflow matrix entry**: `examples/<example_name>` and `tools.nightly_tests.examples_verification.<example_name>_verify` in `.github/workflows/nightly-examples-e2e.yml`

If an example is partially wired (for example, `<example_name>_verify.py` exists but is missing from `BUILD.bazel` or `nightly-examples-e2e.yml`), the gate fails immediately and reports which artifact is missing.

### How to Explicitly Opt Out (When Required)

Adding a nightly verification suite is the default expectation for all examples. Remember that hardware-gated examples (GPU/TPU) **can and should** still be verified using `wait_for_completion=False` (Pattern B above) to validate control-plane Experiment/WorkUnit/JobSet creation.

If an `examples/<example_name>/` directory genuinely cannot or should not be executed by the nightly GKE workflow (for example, static YAML/profile catalogs like `examples/components` or local-only `xm_local` scripts like `examples/xmc_lite_job`), add `<example_name>` to `EXEMPT_EXAMPLES` in [`tools/nightly_tests/check_examples_coverage.py`](check_examples_coverage.py) along with an inline `#` comment explaining why nightly E2E verification does not apply.

The gate cross-checks `EXEMPT_EXAMPLES` against existing `examples/` subdirectories to prevent stale entries and fails if an example listed in `EXEMPT_EXAMPLES` already has nightly verification artifacts.
