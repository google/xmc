# Nightly Examples E2E Verification Suite

This directory contains the end-to-end (E2E) verification suite for runnable workloads under [`examples/`](../../examples/) as well as weekly UI visual snapshot validators.

Every runnable example under `examples/<example_name>/` is executed nightly against a live XMC GKE environment by [`.github/workflows/nightly-examples-e2e.yml`](../../.github/workflows/nightly-examples-e2e.yml) and verified by a corresponding `tools/nightly_tests/<example_name>_verify.py` script.

---

## 1. Architecture & Execution Lifecycle

The Nightly Examples E2E workflow ([`.github/workflows/nightly-examples-e2e.yml`](../../.github/workflows/nightly-examples-e2e.yml)) runs in three stages:

1. **Build Example Container Images (`build-example-images`)**:
   Builds and pushes custom container images for examples that include a `Dockerfile` (`examples/<example_name>/Dockerfile`) to Artifact Registry (`us-docker.pkg.dev/${PROJECT_ID}/xmc/<image_name>:latest`).
2. **Launch & Verify Matrix (`run-and-verify-examples`)**:
   For each matrix entry:
   - Installs the XMC CLI, Python SDKs (`sdks/python/xmc`, `sdks/python/xdash`), and optional example `requirements.txt`.
   - Mints an OIDC identity token (`XMC_AUTH_TOKEN`) and starts health-checked `kubectl port-forward` tunnels to internal services (`dashboard-service:50051` and `clickhouse:8123`) when `requires_clickhouse: true` is set.
   - Executes the example entrypoint script (`launch.py` or `create_dashboard.py`) and extracts the created Experiment XID (or Dashboard ID) from stdout.
   - Runs the example's verification module via `python3 -m tools.nightly_tests.<example_name>_verify --xid="${XID}" --endpoint="${XMANAGER_ENDPOINT}" --expected_queue="${EXPECTED_QUEUE}"`.
3. **Aggregate Results (`aggregate-results`)**:
   Combines per-example JSON status artifacts into a unified workflow summary and alerts repository owners if any scheduled run on `main` fails.

### Shared Verification Modules

- [`common.py`](common.py): Provides standard CLI flags (`--xid`, `--endpoint`, `--expected_queue`, `--timeout_sec`, `--dashboard_endpoint`, `--clickhouse_endpoint`, `--require_clickhouse_metrics`, `--dashboard_id`), authenticated ESS gRPC channel management (`ess_channel_context`), formatted banners, and high-level workload verification runners (`verify_standard_workload`, `verify_dashboard_workload`, `assert_*`).
- [`xmc_entities_validator.py`](xmc_entities_validator.py): Implements the underlying ESS gRPC, ClickHouse HTTP SQL, and Dashboard Service REST/gRPC polling and validation assertions:
  - `validate_experiment`: Polls `GetExperiment` and asserts title substring match.
  - `validate_work_units`: Polls `ListWorkUnits` until all expected WorkUnits reach `COMPLETED` (or `RUNNING` / `PENDING` when `wait_for_completion=False`), failing fast on `FAILED` or `CANCELLED`.
  - `validate_work_unit_jobsets`: Asserts `JobSet` replicated job names, replica counts, primary container names, init containers, sidecars, and volumes.
  - `validate_node_selector_mappings`: Asserts per-job `node_selector` key-value constraints on the WorkUnit `JobSet` spec.
  - `validate_artifacts`: Polls `ListArtifacts` and verifies artifact types (`url`, `chart`, `directory`, `markdown`, etc.) and metric series names.
  - `validate_clickhouse_metrics`: Queries ClickHouse HTTP SQL (`SELECT metric_name, count(), ... FROM metrics`) to assert metric ingestion and finite float values.
  - `validate_dashboard` / `validate_dashboard_chart_data`: Queries Dashboard Service REST/gRPC endpoints to assert dashboard creation, constituent chart definitions, and `GetChartData` time-series hydration.
  - `stop_work_units`: Issues `BatchStopWorkUnits` cleanup for non-completing (`wait_for_completion=False`) workloads.

---

## 2. Step-by-Step Guide: Adding a Verification Suite for a New Example

When adding a new example under `examples/<example_name>/`, follow these four steps to wire its nightly E2E verification suite.

### Step 1: Create `tools/nightly_tests/<example_name>_verify.py`

Choose the verification pattern that matches your example's runtime behavior:

#### Pattern A: Standard CPU Workload (Completes on CPU Queue)

Use `common.verify_standard_workload` for workloads that schedule onto the default `cpu-queue` and run to completion. Provide an optional `extra_step` callback if the workload logs metrics to ClickHouse, provisions dashboards, or requires custom JobSet spec assertions:

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

from tools.nightly_tests import common

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
    common.verify_standard_workload(
        argv,
        banner_title="XMC My Example Verification & Assertions",
        summary_title="ALL MY EXAMPLE ASSERTIONS PASSED SUCCESSFULLY!",
        expected_title_pattern=EXPECTED_TITLE_PATTERN,
        expected_jobs=EXPECTED_JOBSET_SPECS,
        expected_worker_names=EXPECTED_WORKER_NAMES,
    )


if __name__ == "__main__":
    common.run_main(main)
```

If your example also writes artifacts, ClickHouse metrics, or dashboards, pass `expected_artifact_types`, `expected_metric_names`, and an `extra_step` callback (see [`chart_artifact_job_verify.py`](chart_artifact_job_verify.py) or [`metrics_dashboard_job_verify.py`](metrics_dashboard_job_verify.py)).

#### Pattern B: Hardware-Gated Workload (GPU / TPU / Specialized Node Pools)

The nightly E2E cluster runs a CPU-only node pool (`cpu-queue`). If an example requests GPU/TPU accelerators, specialized node selectors (`gpu-pool`), or TPU queues (`tpu-queue`), pods will remain `PENDING` (unschedulable) on the CPU cluster.

Pass `wait_for_completion=False` to `common.verify_standard_workload`. This verifies that the Experiment, WorkUnit, Queue routing, JobSet topology, and custom node selectors/artifacts are persisted accurately by the control plane while `PENDING` or `RUNNING`, and automatically stops the WorkUnits in a `finally` block via `BatchStopWorkUnits` so pending jobs do not accumulate in Kueue:

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
    common.verify_standard_workload(
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

For examples that provision standalone resources (such as `xdash` dashboards) rather than launching an Experiment XID, use `common.verify_dashboard_workload` (which accepts `--dashboard_id` or `--xid` from the workflow runner):

```python
def main(argv: Sequence[str]) -> None:
    """Main verification routine for standalone dashboard example."""
    common.verify_dashboard_workload(
        argv,
        banner_title="XMC Dashboard Example Verification",
        summary_title="ALL DASHBOARD EXAMPLE ASSERTIONS PASSED!",
        default_dashboard_id="my_dashboard_id",
        expected_title_pattern="My Dashboard Title",
        expected_min_charts=2,
    )
```

### Step 2: Register the Target in `tools/nightly_tests/BUILD.bazel`

1. Add `"<example_name>_verify.py"` to `exports_files([...])` in [`tools/nightly_tests/BUILD.bazel`](BUILD.bazel).
2. Add a `py_binary` target with `name = "<example_name>_verify"`:

```starlark
py_binary(
    name = "<example_name>_verify",
    srcs = ["<example_name>_verify.py"],
    imports = ["../.."],
    deps = [
        ":common",
        ":xmc_entities_validator_lib",
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
            verify_module: 'tools.nightly_tests.<example_name>_verify'
            expected_queue: "${{ inputs.queue_name || 'cpu-queue' }}"
            requires_clickhouse: false
            timeout_minutes: 15
```

Ensure your example's `entrypoint` script prints `Experiment XID: <xid>` (or `experiments/<xid>` / `dashboards/<id>`) to stdout so the workflow launch step can extract the identifier automatically.

### Step 4: Verify Locally and in CI

Run the Bazel build, unit tests, and example coverage gate locally:

```bash
EXAMPLE_TARGET="simple_job_verify"
bazel build "//tools/nightly_tests:${EXAMPLE_TARGET}"
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

---

## 3. Automated Example Coverage Gate & Explicit Opt-Out Policy

To ensure new examples do not silently lack E2E verification, [`check_examples_coverage.py`](check_examples_coverage.py) enforces a repository gate in [`.githooks/pre-commit`](../../.githooks/pre-commit) and in [`.github/workflows/lint.yml`](../../.github/workflows/lint.yml) (on PRs touching `examples/**`, `tools/nightly_tests/**`, or `.github/workflows/nightly-examples-e2e.yml`, as well as via `bazel test //tools/nightly_tests:check_examples_coverage_test`).

### What the Gate Checks

For every subdirectory `examples/<example_name>/`, the gate verifies that all three of the following exist:

1. **Verifier script**: `tools/nightly_tests/<example_name>_verify.py`
2. **Bazel target**: `py_binary(name = "<example_name>_verify", ...)` in `tools/nightly_tests/BUILD.bazel`
3. **Workflow matrix entry**: `examples/<example_name>` and `tools.nightly_tests.<example_name>_verify` in `.github/workflows/nightly-examples-e2e.yml`

If an example is partially wired (for example, `<example_name>_verify.py` exists but is missing from `BUILD.bazel` or `nightly-examples-e2e.yml`), the gate fails immediately and reports which artifact is missing.

### How to Explicitly Opt Out (When Required)

Adding a nightly verification suite is the default expectation for all examples. Remember that hardware-gated examples (GPU/TPU) **can and should** still be verified using `wait_for_completion=False` (Pattern B above) to validate control-plane Experiment/WorkUnit/JobSet creation.

If an `examples/<example_name>/` directory genuinely cannot or should not be executed by the nightly GKE workflow (for example, static YAML/profile catalogs like `examples/components` or local-only `xm_local` scripts like `examples/xmc_lite_job`), you must **explicitly opt out** with a written justification using one of two mechanisms:

1. **Marker File (`examples/<example_name>/.no_nightly_e2e`)**:
   Create a `.no_nightly_e2e` file inside the example directory containing a clear, non-empty explanation (>= 10 characters, excluding `#` comments) of why nightly E2E verification is intentionally omitted. Empty marker files are rejected by the gate.
2. **Central Registry (`EXEMPT_EXAMPLES` in `tools/nightly_tests/check_examples_coverage.py`)**:
   Register `<example_name>` in `EXEMPT_EXAMPLES` with a non-empty justification string. Note that if an entry in `EXEMPT_EXAMPLES` later receives a verification suite, the gate automatically validates that all three verification artifacts are complete.
