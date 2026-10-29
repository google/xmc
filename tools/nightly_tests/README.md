# Nightly Examples E2E Verification Suite

This directory contains the end-to-end (E2E) verification suite for runnable workloads under [`examples/`](../../examples/), Web UI Critical User Journeys (CUJs), and the weekly UI visual snapshot validator ([`weekly_visual_snapshot.py`](weekly_visual_snapshot.py), tested by [`weekly_visual_snapshot_test.py`](weekly_visual_snapshot_test.py), and report formatter [`format_visual_audit_summary.js`](format_visual_audit_summary.js)).

Every runnable example under [`examples/`](../../examples/) is executed nightly against a live XMC GKE environment by [`.github/workflows/nightly-examples-e2e.yml`](../../.github/workflows/nightly-examples-e2e.yml) and verified by a corresponding script in [`examples_verification/`](examples_verification/).

---

## 1. Architecture & Execution Lifecycle

`tools/nightly_tests/` is organized into two primary packages:

- **[`verification/`](verification/) ([`BUILD.bazel`](verification/BUILD.bazel))**: Shared verification primitives, session managers, backend entity pollers, and Playwright Web UI helpers, where logic modules are each paired with a `*_test.py` unit test suite.
- **[`examples_verification/`](examples_verification/) ([`BUILD.bazel`](examples_verification/BUILD.bazel))**: Concrete E2E verification entrypoints and unit tests for individual runnable workloads and Web UI routes.

The Nightly Examples E2E workflow ([`.github/workflows/nightly-examples-e2e.yml`](../../.github/workflows/nightly-examples-e2e.yml)) runs in three stages:

1. **Build Example Container Images (`build-example-images`)**: Builds and pushes container images for examples that include a `Dockerfile`.
2. **Launch & Verify Matrix (`run-and-verify-examples`)**: Installs SDKs/Playwright, establishes authenticated tunnels to cluster services when needed, runs the example entrypoint to create an Experiment XID (or Dashboard ID), and executes the matching verifier module in [`examples_verification/`](examples_verification/).
3. **Aggregate Results (`aggregate-results`)**: Combines per-example JSON status artifacts into a unified workflow summary.

### Shared Verification Modules ([`verification/`](verification/))

- [`verification/verifier_cli.py`](verification/verifier_cli.py): Standard CLI flags, argument validation, identifier normalization, and script entrypoint runner.
- [`verification/endpoint_resolution.py`](verification/endpoint_resolution.py): Resolves ESS gRPC, Dashboard Service, ClickHouse HTTP, and Web UI base URLs from flags or environment variables.
- [`verification/ess_session.py`](verification/ess_session.py): Authenticated ESS gRPC channel lifecycle and standardized verification error handling.
- [`verification/verification_report.py`](verification/verification_report.py): Console formatting for verification step banners and final summary tables.
- [`verification/workload_verification.py`](verification/workload_verification.py): End-to-end verification orchestrators for batch workloads and standalone dashboards.
- [`verification/xmc_entities_validator.py`](verification/xmc_entities_validator.py): Polling and assertions for ESS gRPC entities (Experiments, WorkUnits, JobSets, Artifacts, MultiKueue), ClickHouse metrics, and Dashboard Service charts.
- [`verification/ui_entities_validator.py`](verification/ui_entities_validator.py) & [`verification/ui_css_selectors.py`](verification/ui_css_selectors.py): Authenticated Playwright Chromium browser sessions, route navigation, shared Angular CSS selectors, screenshot capture, and runtime/network error assertions.

### Out-of-the-Box Verification Capabilities

The [`verification/`](verification/) package provides reusable verification across every layer of the XMC stack:

| Verification Layer | Module Pointer(s) | Capabilities Provided |
| :--- | :--- | :--- |
| **Control Plane (ESS gRPC)** | [`xmc_entities_validator.py`](verification/xmc_entities_validator.py), [`workload_verification.py`](verification/workload_verification.py) | Experiment metadata and queue routing checks, WorkUnit lifecycle polling, and automatic WorkUnit teardown for unschedulable hardware-gated jobs. |
| **Kubernetes JobSets & MultiKueue** | [`xmc_entities_validator.py`](verification/xmc_entities_validator.py), [`workload_verification.py`](verification/workload_verification.py) | ReplicatedJob topology, replica counts, primary container specs, per-job node selector constraints, and MultiKueue worker cluster delegation checks. |
| **WorkUnit Artifacts** | [`xmc_entities_validator.py`](verification/xmc_entities_validator.py), [`workload_verification.py`](verification/workload_verification.py) | Artifact registration, expected artifact type coverage, and metadata integrity checks. |
| **ClickHouse Telemetry** | [`xmc_entities_validator.py`](verification/xmc_entities_validator.py), [`workload_verification.py`](verification/workload_verification.py) | ClickHouse HTTP polling until OTLP/metric rows for the target Experiment XID are ingested with expected metric names and minimum row counts. |
| **xDash / Dashboard Service** | [`xmc_entities_validator.py`](verification/xmc_entities_validator.py), [`workload_verification.py`](verification/workload_verification.py) | Dashboard configuration persistence, chart count validation, and live time-series chart data hydration backed by ClickHouse metrics. |
| **Playwright Web UI E2E** | [`ui_entities_validator.py`](verification/ui_entities_validator.py), [`ui_css_selectors.py`](verification/ui_css_selectors.py) | Headless Chromium session with Cloud IAP OIDC / session JWT auth, cold-start reload retry, login redirect guards, milestone/failure screenshots, and JS/HTTP error gates. |

#### Existing UI E2E Verifiers

Refer to these implementations in [`examples_verification/`](examples_verification/) when adding UI coverage for Angular views or full-stack features:

- [`examples_verification/simple_job_ui_verify.py`](examples_verification/simple_job_ui_verify.py): Experiment Overview page (`/experiments/<xid>`), header metadata, and Work Units table hydration.
- [`examples_verification/metrics_dashboard_ui_verify.py`](examples_verification/metrics_dashboard_ui_verify.py): Piggybacks on [`metrics_dashboard_job_verify.py`](examples_verification/metrics_dashboard_job_verify.py) (`--verify_ui=true`) to verify `/dashboards` search filtering, `/dashboards/exp_<xid>_overview` KPI boxes, WorkUnit deep-links, and ECharts canvas rendering.
- [`examples_verification/resource_observability_dashboard_ui_verify.py`](examples_verification/resource_observability_dashboard_ui_verify.py) (tested by [`examples_verification/resource_observability_dashboard_ui_verify_test.py`](examples_verification/resource_observability_dashboard_ui_verify_test.py)): Resource Observability Dashboard (`/resources`), covering Cluster Queue cards, queue selection, the queued jobs table (optionally workload-backed via `--xid`), and the Smart Resource Finder tab.
- [`examples_verification/admin_panel_ui_verify.py`](examples_verification/admin_panel_ui_verify.py): Standalone UI verifier for the Admin Panel (`/admin`), covering route access, System Info cards, Services Health statuses and refresh, and Configuration Entries filtering.

---

## 2. Step-by-Step Guide: Adding a Verification Suite

When adding a new example under [`examples/`](../../examples/) or a new UI E2E check, follow these four steps.

### Step 1: Implement the Verifier in [`examples_verification/`](examples_verification/)

> [!IMPORTANT]
> **Shared Dirty-Environment Setup (`xmc-nightly`)**: Nightly E2E and UI verification suites execute against a long-lived shared cluster (`xmc-nightly`, analogous to staging and multi-tenant dev clusters) populated with thousands of pre-existing historical entities from prior runs. Verifiers and test suites MUST NEVER assume the environment is clean, empty, or low-cardinality, or that newly created entities appear on an unfiltered first page. Always scope queries by target XID/dashboard ID, use server-side filtering (`AIP-160`), paginate across pages, or navigate directly to the resource URL (`/experiments/<xid>`, `/dashboards/<dashboard_id>`) rather than scanning truncated list pages.

Create `tools/nightly_tests/examples_verification/<example_name>_verify.py` (and a matching `<example_name>_verify_test.py` unit test) following one of the canonical patterns below:

- **Pattern A — Standard CPU Workload**: For workloads that schedule onto `cpu-queue` and run to completion.
  - Minimal baseline: [`examples_verification/simple_job_verify.py`](examples_verification/simple_job_verify.py)
  - With WorkUnit artifact assertions: [`examples_verification/chart_artifact_job_verify.py`](examples_verification/chart_artifact_job_verify.py)
  - With ClickHouse telemetry assertions: [`examples_verification/clickhouse_metrics_job_verify.py`](examples_verification/clickhouse_metrics_job_verify.py)
  - With MultiKueue delegation assertions: [`examples_verification/multikueue_job_verify.py`](examples_verification/multikueue_job_verify.py)
- **Pattern B — Hardware-Gated Workload (GPU / TPU / Custom Node Pools)**: For workloads requesting accelerators or specialized node pools that remain `PENDING` on the CPU-only nightly cluster. Verifies control-plane Experiment, WorkUnit, Queue routing, and JobSet topology before automatically stopping the pending WorkUnits.
  - Per-job node selector and GPU pool verification: [`examples_verification/per_job_node_selector_verify.py`](examples_verification/per_job_node_selector_verify.py)
  - TPU / Pathways topology verification: [`examples_verification/pathways_job_verify.py`](examples_verification/pathways_job_verify.py)
- **Pattern C — Standalone Dashboard or Control-Plane Script**: For examples that provision control-plane resources (such as `xdash` dashboards) rather than launching an Experiment XID.
  - Standalone dashboard verification: [`examples_verification/xdash_dashboard_verify.py`](examples_verification/xdash_dashboard_verify.py)
- **Pattern D — Playwright Web UI E2E Verifier**:
  - Piggybacked on a backend workload via `--verify_ui=true`: see [`examples_verification/metrics_dashboard_job_verify.py`](examples_verification/metrics_dashboard_job_verify.py) and [`examples_verification/metrics_dashboard_ui_verify.py`](examples_verification/metrics_dashboard_ui_verify.py).
  - Optionally workload-backed global UI route verifier (accepts `--xid` to verify queued/running work units and stops them on exit): see [`examples_verification/resource_observability_dashboard_ui_verify.py`](examples_verification/resource_observability_dashboard_ui_verify.py) (tested by [`examples_verification/resource_observability_dashboard_ui_verify_test.py`](examples_verification/resource_observability_dashboard_ui_verify_test.py)).
  - Standalone global UI route verifier (no workload launch required): see [`examples_verification/admin_panel_ui_verify.py`](examples_verification/admin_panel_ui_verify.py).

### Step 2: Register Bazel Targets in [`examples_verification/BUILD.bazel`](examples_verification/BUILD.bazel)

Export `<example_name>_verify.py` in `exports_files` and add the corresponding `py_binary` (and `py_test`) targets in [`examples_verification/BUILD.bazel`](examples_verification/BUILD.bazel), following the existing targets in that file.

### Step 3: Wire the Scenario in [`.github/workflows/nightly-examples-e2e.yml`](../../.github/workflows/nightly-examples-e2e.yml)

1. **Container Image (if applicable)**: If the example has a custom `Dockerfile`, add a build-and-push entry in the `build-example-images` job of [`.github/workflows/nightly-examples-e2e.yml`](../../.github/workflows/nightly-examples-e2e.yml).
2. **Matrix Entry**: Add an entry under `jobs.run-and-verify-examples.strategy.matrix.example` in [`.github/workflows/nightly-examples-e2e.yml`](../../.github/workflows/nightly-examples-e2e.yml) (follow existing entries for workload-backed, ClickHouse-backed, or standalone `verify_ui: true` checks). Ensure workload entrypoints print `Experiment XID: <xid>` (or `experiments/<xid>` / `dashboards/<id>`) to stdout so the workflow launch step can extract the identifier.

### Step 4: Verify Locally and in CI

1. **Run Unit Tests & Coverage Gates Locally**:
   ```bash
   bazel test //tools/nightly_tests/...
   python3 tools/nightly_tests/check_examples_coverage.py
   ./.githooks/pre-commit --check
   ```
2. **Run a Verifier Against a Deployed GKE Environment**:
   Sign in with `xmanager login` (see [Authentication and Experiment Access](../../docs/authentication.md)), then invoke any module in [`examples_verification/`](examples_verification/) via `./tools/venv/bin/python3 -m tools.nightly_tests.examples_verification.<verifier_name>` with `--xid`, `--expected_queue`, `--endpoint`, `--ui_base_url`, and `--headless=false` (see flags in [`verification/verifier_cli.py`](verification/verifier_cli.py)). Verifiers read the experiment as you, so pass the XID of an experiment you launched. Experiments launched by the nightly workflow belong to its CI service account, and only that account can read them.
3. **Trigger a Single Scenario in GitHub Actions on Your Branch**:
   ```bash
   gh workflow run nightly-examples-e2e.yml --ref "<branch_name>" -f examples_filter="<example_id>"
   ```
   Pass `-f skip_verification=true` to test building and launching without running verification assertions.

---

## 3. Automated Example Coverage Gate & Explicit Opt-Out Policy

[`check_examples_coverage.py`](check_examples_coverage.py) (tested by [`check_examples_coverage_test.py`](check_examples_coverage_test.py)) enforces in [`.githooks/pre-commit`](../../.githooks/pre-commit) and [`.github/workflows/lint.yml`](../../.github/workflows/lint.yml) that every subdirectory under [`examples/`](../../examples/) is wired across all three locations:

1. Verifier script in [`examples_verification/`](examples_verification/)
2. `py_binary` target in [`examples_verification/BUILD.bazel`](examples_verification/BUILD.bazel)
3. Matrix entry in [`.github/workflows/nightly-examples-e2e.yml`](../../.github/workflows/nightly-examples-e2e.yml)

### How to Explicitly Opt Out (When Required)

Adding a nightly verification suite is the default expectation for all examples (including GPU/TPU examples via Pattern B above).

If an [`examples/`](../../examples/) subdirectory cannot be executed by the nightly GKE workflow (such as static YAML catalogs or local-only `xm_local` scripts), add its directory name with an explanatory comment to the exemption set in [`check_examples_coverage.py`](check_examples_coverage.py).

---

## 4. Per-PR Examples Import Check

[`examples_import_smoke.py`](examples_import_smoke.py) (tested by [`examples_import_smoke_test.py`](examples_import_smoke_test.py)) catches example import regressions, such as a missing SDK symbol or a stale package shadowing a local SDK, before they reach the nightly suite. [`.github/workflows/examples-smoke.yml`](../../.github/workflows/examples-smoke.yml) runs it:

- On every PR and merge-queue run that touches `examples/`, `sdks/python/`, `deploy/cli/`, `api/protos/`, or the related workflows. Other PRs report success without running.
- As the `examples-import-check` job of the nightly suite, in parallel with the image builds.

The workflow builds a virtualenv with [`install_xmanager.py`](../../deploy/cli/install_xmanager.py) and every `examples/*/requirements.txt`. That script also asserts local SDK provenance. The tool then reads each `(example_path, entrypoint)` pair from the nightly matrix and executes the entrypoint's module body (imports and flag definitions) in its own subprocess. `app.run(main)` never runs, so nothing is launched and no cluster or credentials are needed.

> [!NOTE]
> The check covers imports and flag definitions only. Runtime behavior is still verified by the nightly workload suite.

Run it locally:

```bash
python3 deploy/cli/install_xmanager.py --venv .venv $(printf -- '--requirements %s ' examples/*/requirements.txt)
.venv/bin/python3 tools/nightly_tests/examples_import_smoke.py --venv-python .venv/bin/python3
```
