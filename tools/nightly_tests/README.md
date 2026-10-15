# Nightly Examples E2E Verification Suite

This directory contains the end-to-end (E2E) verification suite for runnable workloads under [`examples/`](../../examples/) as well as weekly UI visual snapshot validators.

Every runnable example under `examples/<example_name>/` is executed nightly against a live XMC GKE environment by [`.github/workflows/nightly-examples-e2e.yml`](../../.github/workflows/nightly-examples-e2e.yml) and verified by a corresponding `tools/nightly_tests/examples_verification/<example_name>_verify.py` script.

---

## 1. Architecture & Execution Lifecycle

To maintain high cohesion and clear separation of concerns, `tools/nightly_tests/` is organized into two primary subdirectories:

1. **`verification/` (`//tools/nightly_tests/verification:*`)**:
   Shared, reusable verification primitives, session/API stubs, and UI validators (e.g. `verifier_cli.py`, `workload_verification.py`, `xmc_entities_validator.py`, `ui_entities_validator.py`). Every shared helper is paired with a 1-to-1 `*_test.py` unit test suite.
2. **`examples_verification/` (`//tools/nightly_tests/examples_verification:*`)**:
   Concrete end-to-end verification scripts for individual runnable workloads under `examples/` and CUJs (e.g. `simple_job_verify.py`, `clickhouse_metrics_job_verify.py`, `metrics_dashboard_ui_verify.py`, `resource_observability_dashboard_ui_verify.py`, `admin_panel_ui_verify.py`).

The Nightly Examples E2E workflow ([`.github/workflows/nightly-examples-e2e.yml`](../../.github/workflows/nightly-examples-e2e.yml)) runs in three stages:

1. **Build Example Container Images (`build-example-images`)**:
   Builds and pushes custom container images for examples that include a `Dockerfile` (`examples/<example_name>/Dockerfile`) to Artifact Registry (`us-docker.pkg.dev/${PROJECT_ID}/xmc/<image_name>:latest`).
2. **Launch & Verify Matrix (`run-and-verify-examples`)**:
   For each matrix entry:
   - Installs the XMC CLI, Python SDKs (`sdks/python/xmc`, `sdks/python/xdash`), optional example `requirements.txt`, and Playwright Chromium when `verify_ui: true` is set.
   - Mints an OIDC identity token (`XMC_AUTH_TOKEN`) and starts health-checked `kubectl port-forward` tunnels to internal services (`dashboard-service:50051` and `clickhouse:8123`) when `requires_clickhouse: true` is set.
   - Executes the example entrypoint script (`launch.py` or `create_dashboard.py`) and extracts the created Experiment XID (or Dashboard ID) from stdout.
   - Runs the example's verification module via `python3 -m tools.nightly_tests.examples_verification.<example_name>_verify --xid="${XID}" --endpoint="${XMANAGER_ENDPOINT}" --expected_queue="${EXPECTED_QUEUE}"` (adding `--verify_ui=true --ui_base_url="${XMANAGER_UI_URL}"` when `verify_ui: true` is set).
3. **Aggregate Results (`aggregate-results`)**:
   Combines per-example JSON status artifacts into a unified workflow summary and alerts repository owners if any scheduled run on `main` fails.

### Shared Verification Modules (`tools/nightly_tests/verification/`)

Each module under [`verification/`](verification/) (`//tools/nightly_tests/verification:*`) is paired with a 1-to-1 `*_test.py` unit test suite:

- [`verification/verifier_cli.py`](verification/verifier_cli.py): Standard CLI flag definitions, argument validation, XID normalization, and the entrypoint runner for all verification scripts.
- [`verification/endpoint_resolution.py`](verification/endpoint_resolution.py): Resolves ESS gRPC, Dashboard Service REST, ClickHouse HTTP, and Web UI base URLs from CLI flags, environment variables, or local defaults.
- [`verification/ess_session.py`](verification/ess_session.py): Context managers for authenticated ESS gRPC channel lifecycle and standardized verification error handling.
- [`verification/verification_report.py`](verification/verification_report.py): Console formatting for verification step banners and final pass/fail summary tables.
- [`verification/workload_verification.py`](verification/workload_verification.py): High-level orchestrators and step assertions for verifying standard batch workloads and standalone dashboards end-to-end.
- [`verification/xmc_entities_validator.py`](verification/xmc_entities_validator.py): Core polling and validation logic for ESS gRPC entities (Experiments, WorkUnits, JobSets, Artifacts, MultiKueue metadata), ClickHouse SQL metrics, and Dashboard Service chart data.
- [`verification/ui_entities_validator.py`](verification/ui_entities_validator.py) & [`verification/ui_css_selectors.py`](verification/ui_css_selectors.py): Authenticated Playwright Chromium browser session management, route navigation, shared Angular CSS selectors, screenshot capture, and runtime/network error assertions.

### Out-of-the-Box Testing & Verification Capabilities

Developers do not need to write boilerplate gRPC clients, Kubernetes API pollers, SQL query runners, or Playwright authentication plumbing from scratch. The [`verification/`](verification/) package provides turnkey verification across every layer of the XMC stack:

| Verification Layer | Primary Module(s) | What It Provides Out of the Box |
| :--- | :--- | :--- |
| **Control Plane (ESS gRPC)** | [`xmc_entities_validator.py`](verification/xmc_entities_validator.py), [`workload_verification.py`](verification/workload_verification.py) | Experiment metadata and queue routing checks (`cpu-queue`, `tpu-queue`, `multikueue-cpu-queue`), WorkUnit lifecycle state polling (`PENDING` -> `RUNNING` -> `COMPLETED`), and automatic WorkUnit teardown for hardware-gated jobs. |
| **Kubernetes JobSets & MultiKueue** | [`xmc_entities_validator.py`](verification/xmc_entities_validator.py), [`workload_verification.py`](verification/workload_verification.py) | ReplicatedJob topology, replica counts, primary container specs, per-job `nodeSelector` constraints, and MultiKueue manager-to-worker cluster `Workload` delegation checks. |
| **WorkUnit Artifacts** | [`xmc_entities_validator.py`](verification/xmc_entities_validator.py), [`workload_verification.py`](verification/workload_verification.py) | WorkUnit artifact registration, expected artifact type coverage (chart configs, checkpoints, storage URIs), and metadata integrity checks. |
| **ClickHouse Telemetry** | [`xmc_entities_validator.py`](verification/xmc_entities_validator.py), [`workload_verification.py`](verification/workload_verification.py) | ClickHouse HTTP SQL polling (`localhost:8123`) until OTLP/metric rows for the target Experiment XID are ingested with expected metric names and minimum row counts. |
| **xDash / Dashboard Service** | [`xmc_entities_validator.py`](verification/xmc_entities_validator.py), [`workload_verification.py`](verification/workload_verification.py) | Dashboard configuration persistence via REST/gRPC, minimum chart count checks, and live time-series chart data hydration backed by ClickHouse metrics. |
| **Playwright Web UI E2E** | [`ui_entities_validator.py`](verification/ui_entities_validator.py), [`ui_css_selectors.py`](verification/ui_css_selectors.py) | Headless Chromium session with Cloud IAP OIDC bearer headers and session JWT cookies, cold-start HTTP 500 reload retry, login/redirect guards, diagnostic screenshots, and JS/HTTP runtime error gates. |

#### Playwright Web UI E2E Testing Framework

[`verification/ui_entities_validator.py`](verification/ui_entities_validator.py) and [`verification/ui_css_selectors.py`](verification/ui_css_selectors.py) provide an end-to-end browser verification harness out of the box:

- **Programmatic Authentication**: Automatically injects `Authorization: Bearer <XMC_AUTH_TOKEN>` headers on same-origin requests, sets `GCP_IAAP_AUTH_TOKEN` and `session_token` (`/api/session` JWT exchange) domain cookies, and configures a 1920x1080 HiDPI viewport.
- **Cold-Start Resilience**: Waits for initial DOM selectors and automatically performs a single `networkidle` reload after clearing transient error buffers if the first page load hits a cold-start timeout or HTTP 500.
- **Route Navigation & Auth/Redirect Guards**: Navigates to any Angular route (`/experiments/<xid>`, `/dashboards`, `/resources`, `/admin`), waits for web fonts to settle, captures a landing screenshot, and fails immediately if the session lands on a login prompt or is unexpectedly redirected by an Angular route guard.
- **Diagnostic Screenshots**: Captures full-page PNG snapshots at each CUJ milestone into `--screenshots_dir` (default `/tmp/xmc_screenshots`). On failure in GitHub Actions, the workflow automatically uploads captured PNGs as the `ui-screenshots-<id>` artifact.
- **Strict Runtime Error Gates**: Fails the test if any uncaught JavaScript `pageerror`, UI error banner (`xmc-error-banner`), or same-origin HTTP `>= 400` response occurs during the user journey.
- **Centralized CSS Selectors**: Keeps shared Angular component selectors in [`verification/ui_css_selectors.py`](verification/ui_css_selectors.py) so UI verifiers stay concise and easy to maintain.

#### Existing UI E2E Verifiers (Reference Implementations)

Use these existing verifiers under [`examples_verification/`](examples_verification/) as reference templates when adding UI tests for new Angular views or full-stack features:

- [`examples_verification/simple_job_ui_verify.py`](examples_verification/simple_job_ui_verify.py): Verifies the Experiment Overview page (`/experiments/<xid>`), header metadata, and Work Units tab table hydration.
- [`examples_verification/metrics_dashboard_ui_verify.py`](examples_verification/metrics_dashboard_ui_verify.py): Piggybacks on `metrics_dashboard_job` via `--verify_ui=true` to test `/dashboards` search filtering, `/dashboards/exp_<xid>_overview` KPI summary boxes, WorkUnit deep-links, and ECharts `<canvas>` rendering.
- [`examples_verification/resource_observability_dashboard_ui_verify.py`](examples_verification/resource_observability_dashboard_ui_verify.py): Verifies the Resource Observability Dashboard (`/resources`), covering Cluster Queue cards, interactive queue selection, the "Jobs in the Queue" table and pagination, and the Smart Resource Finder tab with velocity toggles.
- [`examples_verification/admin_panel_ui_verify.py`](examples_verification/admin_panel_ui_verify.py): Standalone UI verifier for the Admin Panel (`/admin`), asserting privileged route guard access, System Info version/schema cards, Services Health component statuses and live Refresh, and Configuration Entries group collapse/expand and search filtering.

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

#### Pattern D: Writing a Playwright Web UI E2E Verifier

Playwright UI verifiers can be wired in two ways depending on whether the UI view requires a freshly launched workload:

1. **Piggybacked on an Existing Workload (`--verify_ui=true`)**:
   Invoke your UI verifier inside the `extra_step` callback of a backend verifier when `verifier_cli.get_flag(verifier_cli.FLAGS, "verify_ui", False)` is true (see [`metrics_dashboard_job_verify.py`](examples_verification/metrics_dashboard_job_verify.py) and [`metrics_dashboard_ui_verify.py`](examples_verification/metrics_dashboard_ui_verify.py)):

   ```python
   def _verify_backend_and_ui(_stub: Any, xid: str) -> None:
       """Runs backend assertions followed by Playwright Web UI CUJ assertions."""
       if verifier_cli.get_flag(verifier_cli.FLAGS, "verify_ui", False):
           with my_feature_ui_verify.Session.from_flags() as session:
               my_feature_ui_verify.verify_my_feature_ui(session, xid)
   ```

2. **Standalone UI Route Verifier (`example_path: ''` and `verify_ui: true`)**:
   For global UI views that do not require launching an experiment workload first (such as `/admin` in [`admin_panel_ui_verify.py`](examples_verification/admin_panel_ui_verify.py)), create a standalone verifier module and set `example_path: ''`, `entrypoint: ''`, and `verify_ui: true` in [`.github/workflows/nightly-examples-e2e.yml`](../../.github/workflows/nightly-examples-e2e.yml):

   ```python
   from collections.abc import Sequence
   from typing import TypeAlias

   from tools.nightly_tests.verification import (
       ess_session as ess,
       ui_entities_validator,
       verification_report as rep,
       verifier_cli as cli,
   )

   Session: TypeAlias = ui_entities_validator.XMCUIBrowserSession


   def verify_my_feature_ui(session: Session) -> None:
       """Executes Playwright UI E2E assertions for /my-route."""
       session.navigate_to_route("/my-route", "xmc-my-feature-root", "ui_step1_landing.png")
       session.wait_for_selector_with_retry(".feature-card")
       session.take_screenshot("ui_step2_interacted.png")
       session.assert_no_runtime_errors("my_feature")
       rep.print_success_summary("MY FEATURE UI E2E PASSED!", [("UI Base URL", session.ui_base_url)])


   def main(argv: Sequence[str]) -> None:
       """CLI entrypoint for standalone UI E2E verification."""
       cli.validate_argv(argv)
       with ess.handle_verification_errors(), Session.from_flags() as session:
           verify_my_feature_ui(session)


   if __name__ == "__main__":
       cli.run_main(main)
   ```

   Corresponding matrix entry in [`.github/workflows/nightly-examples-e2e.yml`](../../.github/workflows/nightly-examples-e2e.yml):

   ```yaml
          - id: 'my_feature_ui'
            name: 'My Feature (Playwright UI)'
            example_path: ''
            entrypoint: ''
            args: ''
            verify_module: 'tools.nightly_tests.examples_verification.my_feature_ui_verify'
            expected_queue: ''
            verify_ui: true
   ```

   Remember to pair every new `*_verify.py` module with a 1-to-1 `*_verify_test.py` unit test suite in [`tools/nightly_tests/examples_verification/`](examples_verification/).

### Step 2: Register the Target in `tools/nightly_tests/examples_verification/BUILD.bazel`

1. Add `"<example_name>_verify.py"` to `exports_files([...])` in [`tools/nightly_tests/examples_verification/BUILD.bazel`](examples_verification/BUILD.bazel).
2. Add a `py_binary` target with `name = "<example_name>_verify"` (include `"//tools/nightly_tests/verification:xmc_entities_validator_lib"` or `"//tools/nightly_tests/verification:ui_entities_validator_lib"` in `deps` as needed, plus a `py_test` target when adding unit tests):

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
            verify_ui: false
```

Ensure your example's `entrypoint` script prints `Experiment XID: <xid>` (or `experiments/<xid>` / `dashboards/<id>`) to stdout so the workflow launch step can extract the identifier automatically.

### Step 4: Verify Locally and in CI (Developer Workflow)

#### 1. Run Unit Tests & Coverage Gates Locally with Bazel

All verification libraries and UI verifiers have hermetic unit tests that mock out gRPC channels and Playwright `Page` locators, allowing you to validate logic in seconds without a live cluster:

```bash
EXAMPLE_TARGET="simple_job_verify"
bazel build "//tools/nightly_tests/examples_verification:${EXAMPLE_TARGET}"
bazel test //tools/nightly_tests/...
python3 tools/nightly_tests/check_examples_coverage.py
./.githooks/pre-commit --check
```

#### 2. Run a Backend or Playwright UI Verifier Locally Against a Deployed GKE Environment

To run or debug a verifier directly from your workstation against a deployed GKE environment (such as `xmc-nightly` or your personal dev cluster):

```bash
# Authenticate with Cloud IAP (or set XMC_SESSION_TOKEN from /api/session)
export XMC_AUTH_TOKEN="$(gcloud auth print-identity-token --audiences="${IAP_CLIENT_ID}" --include-email)"

# Run a backend + piggybacked Playwright UI verifier with a visible browser window (--headless=false)
./tools/venv/bin/python3 -m tools.nightly_tests.examples_verification.metrics_dashboard_job_verify \
  --xid=42 \
  --endpoint="grpc.xmc-nightly.example.com:443" \
  --expected_queue="cpu-queue" \
  --verify_ui=true \
  --ui_base_url="https://xmc-nightly.example.com" \
  --headless=false \
  --screenshots_dir=/tmp/xmc_screenshots

# Or run a standalone Playwright UI verifier directly against any UI route
./tools/venv/bin/python3 -m tools.nightly_tests.examples_verification.admin_panel_ui_verify \
  --ui_base_url="https://xmc-nightly.example.com" \
  --headless=false \
  --screenshots_dir=/tmp/xmc_screenshots
```

#### 3. Trigger a Single Scenario in GitHub Actions on Your Feature Branch

To execute an end-to-end verification scenario against the live nightly GKE cluster in GitHub Actions without running the entire matrix, pass `-f examples_filter=<id>` (comma-separated IDs are also supported):

```bash
BRANCH_NAME="feat/my-new-feature"
EXAMPLE_ID="admin_panel_ui"
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
