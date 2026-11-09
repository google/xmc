# Nightly Examples E2E Verification Suite

This directory contains the end-to-end (E2E) verification suite for runnable workloads under [`examples/`](../../examples/), Web UI Critical User Journeys (CUJs), and the weekly UI visual snapshot validator ([`weekly_visual_snapshot.py`](weekly_visual_snapshot.py), tested by [`weekly_visual_snapshot_test.py`](weekly_visual_snapshot_test.py), and report formatter [`format_visual_audit_summary.js`](format_visual_audit_summary.js)).

Every runnable example under [`examples/`](../../examples/) is executed nightly against a live XMC GKE environment by [`.github/workflows/nightly-examples-e2e.yml`](../../.github/workflows/nightly-examples-e2e.yml) and verified by a corresponding script in [`examples_verification/`](examples_verification/).

---

## 1. Architecture & Execution Lifecycle

`tools/nightly_tests/` is organized into two primary packages:

- **[`verification/`](verification/) ([`BUILD.bazel`](verification/BUILD.bazel))**: Shared verification primitives, session managers, backend entity pollers, Playwright Web UI helpers, and pure parsing/decision modules, where logic modules are each paired with a `*_test.py` unit test suite.
- **[`examples_verification/`](examples_verification/) ([`README.md`](examples_verification/README.md), [`BUILD.bazel`](examples_verification/BUILD.bazel))**: Concrete post-launch E2E verification test entrypoints (`*_test.py`, registered as `py_binary` targets) that run after launching the corresponding workload in [`examples/`](../../examples/). These modules stay thin: they compose [`verification/`](verification/) primitives with workload-specific DOM/RPC assertions and constants, and are exercised only by the nightly E2E run (they have no hermetic unit tests of their own). Any pure parsing, normalization, or decision logic that runs without a live cluster or browser (e.g., extracting numbers from UI text, mapping queue names, validating ID or position formats) MUST live in a [`verification/`](verification/) module with a paired `*_test.py` unit test, because the nightly run cannot detect a bug that makes a verifier silently pass.

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
- [`verification/resource_observability_parsing.py`](verification/resource_observability_parsing.py): Pure queue-name normalization, velocity rate extraction, and queue position predicates for Resource Observability Dashboard checks.

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

- [`examples_verification/simple_job_ui_test.py`](examples_verification/simple_job_ui_test.py): Experiment Overview page (`/experiments/<xid>`), header metadata, and Work Units table hydration.
- [`examples_verification/metrics_dashboard_ui_test.py`](examples_verification/metrics_dashboard_ui_test.py): Piggybacks on [`metrics_dashboard_job_test.py`](examples_verification/metrics_dashboard_job_test.py) (`--verify_ui=true`) to verify `/dashboards` search filtering, `/dashboards/exp_<xid>_overview` KPI boxes, WorkUnit deep-links, and ECharts canvas rendering.
- [`examples_verification/resource_observability_dashboard_ui_test.py`](examples_verification/resource_observability_dashboard_ui_test.py): Resource Observability Dashboard (`/resources`), covering Cluster Queue cards, queue selection, the queued jobs table (optionally workload-backed via `--xid`), and the Smart Resource Finder tab.
- [`examples_verification/admin_panel_ui_test.py`](examples_verification/admin_panel_ui_test.py): Standalone UI verifier for the Admin Panel (`/admin`), covering route access, System Info cards, Services Health statuses and refresh, and Configuration Entries filtering.

---

## 2. Step-by-Step Guide: Adding a Verification Suite

When adding a new example under [`examples/`](../../examples/) or a new UI E2E check, follow these four steps.

### Step 1: Implement the Verifier in [`examples_verification/`](examples_verification/)

> [!IMPORTANT]
> **Shared Dirty-Environment Setup (`xmc-nightly`)**: Nightly E2E and UI verification suites execute against a long-lived shared cluster (`xmc-nightly`, analogous to staging and multi-tenant dev clusters) populated with thousands of pre-existing historical entities from prior runs. Verifiers and test suites MUST NEVER assume the environment is clean, empty, or low-cardinality, or that newly created entities appear on an unfiltered first page. Always scope queries by target XID/dashboard ID, use server-side filtering (`AIP-160`), paginate across pages, or navigate directly to the resource URL (`/experiments/<xid>`, `/dashboards/<dashboard_id>`) rather than scanning truncated list pages.

Create `tools/nightly_tests/examples_verification/<example_name>_test.py` following one of the canonical patterns below (see [`examples_verification/README.md`](examples_verification/README.md) for prerequisite launch ordering). Keep it a thin entrypoint: put any pure parsing or decision logic in a unit-tested [`verification/`](verification/) module (see [Architecture](#1-architecture--execution-lifecycle)).

- **Pattern A — Standard CPU Workload**: For workloads that schedule onto `cpu-queue` and run to completion.
  - Minimal baseline: [`examples_verification/simple_job_test.py`](examples_verification/simple_job_test.py)
  - With WorkUnit artifact assertions: [`examples_verification/chart_artifact_job_test.py`](examples_verification/chart_artifact_job_test.py)
  - With ClickHouse telemetry assertions: [`examples_verification/clickhouse_metrics_job_test.py`](examples_verification/clickhouse_metrics_job_test.py)
  - With MultiKueue delegation assertions: [`examples_verification/multikueue_job_test.py`](examples_verification/multikueue_job_test.py)
- **Pattern B — Hardware-Gated Workload (GPU / TPU / Custom Node Pools)**: For workloads requesting accelerators or specialized node pools that remain `PENDING` on the CPU-only nightly cluster. Verifies control-plane Experiment, WorkUnit, Queue routing, and JobSet topology before automatically stopping the pending WorkUnits.
  - Per-job node selector and GPU pool verification: [`examples_verification/per_job_node_selector_test.py`](examples_verification/per_job_node_selector_test.py)
  - TPU / Pathways topology verification: [`examples_verification/pathways_job_test.py`](examples_verification/pathways_job_test.py)
- **Pattern C — Standalone Dashboard or Control-Plane Script**: For examples that provision control-plane resources (such as `xdash` dashboards) rather than launching an Experiment XID.
  - Standalone dashboard verification: [`examples_verification/xdash_dashboard_test.py`](examples_verification/xdash_dashboard_test.py)
- **Pattern D — Playwright Web UI E2E Verifier**:
  - Piggybacked on a backend workload via `--verify_ui=true`: see [`examples_verification/metrics_dashboard_job_test.py`](examples_verification/metrics_dashboard_job_test.py) and [`examples_verification/metrics_dashboard_ui_test.py`](examples_verification/metrics_dashboard_ui_test.py).
  - Optionally workload-backed global UI route verifier (accepts `--xid` to verify queued/running work units and stops them on exit): see [`examples_verification/resource_observability_dashboard_ui_test.py`](examples_verification/resource_observability_dashboard_ui_test.py).
  - Standalone global UI route verifier (no workload launch required): see [`examples_verification/admin_panel_ui_test.py`](examples_verification/admin_panel_ui_test.py).

### Step 2: Register Bazel Targets in [`examples_verification/BUILD.bazel`](examples_verification/BUILD.bazel)

Export `<example_name>_test.py` in `exports_files` and add the corresponding `py_binary` target in [`examples_verification/BUILD.bazel`](examples_verification/BUILD.bazel), following the existing targets in that file.

### Step 3: Wire the Scenario in [`.github/workflows/nightly-examples-e2e.yml`](../../.github/workflows/nightly-examples-e2e.yml)

1. **Container Image (if applicable)**: If the example has a custom `Dockerfile`, add a build-and-push entry in the `build-example-images` job of [`.github/workflows/nightly-examples-e2e.yml`](../../.github/workflows/nightly-examples-e2e.yml).
2. **Matrix Entry**: Add an entry under `jobs.run-and-verify-examples.strategy.matrix.example` in [`.github/workflows/nightly-examples-e2e.yml`](../../.github/workflows/nightly-examples-e2e.yml) (follow existing entries for workload-backed, ClickHouse-backed, or standalone `verify_ui: true` checks). Ensure workload entrypoints print `Experiment XID: <xid>` (or `experiments/<xid>` / `dashboards/<id>`) to stdout so the workflow launch step can extract the identifier.
3. **Launch Args & Verifier Expectations**: When `args` sets `--num_work_units=N` or `--duration=D`, also set `expected_work_units: N` or `expected_duration_sec: D` on the entry. The verify step forwards them as `--expected_work_units` and `--expected_duration_sec` (see [`verification/verifier_cli.py`](verification/verifier_cli.py)). [`check_matrix_contract.py`](check_matrix_contract.py) (tested by [`check_matrix_contract_test.py`](check_matrix_contract_test.py)) enforces this in [`.githooks/pre-commit`](../../.githooks/pre-commit) and [`.github/workflows/lint.yml`](../../.github/workflows/lint.yml).

### Step 4: Verify Locally and in CI

1. **Run Unit Tests & Coverage Gates Locally**:
   ```bash
   bazel test //tools/nightly_tests/...
   python3 tools/nightly_tests/check_examples_coverage.py
   python3 tools/nightly_tests/check_matrix_contract.py
   ./.githooks/pre-commit --check
   ```
2. **Run a Verifier Against a Deployed GKE Environment**:
   Launch the corresponding example from [`examples/`](../../examples/) first (see [`examples_verification/README.md`](examples_verification/README.md)), sign in with `xmanager login` (see [Authentication and Experiment Access](../../docs/authentication.md)), then invoke any module in [`examples_verification/`](examples_verification/) via `./tools/venv/bin/python3 -m tools.nightly_tests.examples_verification.<example_name>_test` with `--xid`, `--expected_queue`, `--endpoint`, `--ui_base_url`, and `--headless=false` (see flags in [`verification/verifier_cli.py`](verification/verifier_cli.py)). Verifiers read the experiment as you, so pass the XID of an experiment you launched. Experiments launched by the nightly workflow belong to its CI service account, and only that account can read them.
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

- On every PR and merge-queue run that touches `examples/`, `sdks/python/`, `deploy/cli/`, `api/protos/`, `tools/nightly_tests/{examples_verification,verification}/`, or the related workflows. Other PRs report success without running.
- On `workflow_dispatch`, optionally with an `xmanager_version` override.
- As the `examples-import-check` job of the nightly suite, in parallel with the image builds (it doesn't gate them). It receives the nightly `xmanager_version` and `examples_filter` inputs. A failed or cancelled import check alerts OWNERS, and the alert lists every failed job.

The workflow builds one combined virtualenv with [`install_xmanager.py`](../../deploy/cli/install_xmanager.py) and the `requirements.txt` of each example in the nightly matrix. That script also asserts local SDK provenance. This approximates nightly, which builds one venv per example: requirement files are installed one after another, so a conflict between two examples resolves last-wins instead of failing.

The tool then reads the nightly matrix and runs each check in its own subprocess, with a 30s timeout per check:
1. **Entrypoint import:** plainly imports the entrypoint module (e.g. `import launch`) with the nightly launch step's working directory, `PYTHONPATH` and `sys.argv[0]`. Each example needs its own subprocess because 12 of the 13 entrypoints share the module name `launch` and absl's flag registry is process-global (two examples define `--queue`).
2. **Matrix args:** if the entrypoint module defines absl flags, the matrix `args` are parsed against them the way `app.run` would, so a renamed or removed flag, a failed flag validator, or a leftover positional argument fails.
   - Args are tokenized like nightly's `eval echo`: quotes are removed and words are re-split on whitespace.
   - `${QUEUE_NAME}` expands as in nightly. Any other placeholder or shell expansion (`$VAR`, `${VAR:-x}`, `$(...)`) fails.
   - Entrypoints that parse arguments with argparse inside `main()` (`managed_xprof_artifact_job`, `xdash_dashboard`) can't be checked without running `main()`, so they're skipped. So are flags defined in a helper module rather than in the entrypoint itself.
   - Each distinct `(entrypoint, args)` row is checked.
3. **Main guard:** the entrypoint must have a top-level `if __name__ == "__main__":` block; this is verified statically, before the import.
4. **Verify modules:** each matrix `verify_module`, including those of UI-only entries, has its body run from the repo root via `runpy.run_module`, as nightly's `python -m <module>` would, but without its `__main__` block. Caveat: the combined venv could satisfy a verify module's dependency through another example's requirements, which nightly's per-example venv wouldn't.

`app.run(main)` never runs, so nothing is launched and no cluster or credentials are needed.

> [!NOTE]
> Scope: import time only. The check doesn't run `main()` or any verifier logic. Runtime behavior is still verified by the nightly workload suite.

### Contract for example entrypoints
The check assumes, and every nightly example currently satisfies, the following. New examples must too:
- The entrypoint is an importable `<identifier>.py` file at the example root.
- `main()` is only called under a top-level `if __name__ == "__main__":` (enforced by check 3).
- Flags passed in the matrix `args` are absl flags defined in the entrypoint module (checked by check 2), or are parsed by argparse inside `main()`.
- Nothing at import time needs the nightly launch environment (`XMANAGER_ENDPOINT`, `XMC_AUTH_TOKEN`, network, cluster). Module-level `dotenv.load_dotenv()` and flag definitions are fine.
- A `sys.exit()` at import time is reported as success, so don't exit at import time.

Run it locally:

```bash
python3 deploy/cli/install_xmanager.py --venv .venv \
  $(for p in $(python3 tools/nightly_tests/examples_import_smoke.py --list-example-paths); do printf -- '--requirements %s/requirements.txt ' "$p"; done)
.venv/bin/python3 tools/nightly_tests/examples_import_smoke.py --venv-python .venv/bin/python3
```

---

## 5. Nightly Failure Triage CLI (`triage_nightly_run.py`)

[`triage_nightly_run.py`](triage_nightly_run.py) (tested by [`triage_nightly_run_test.py`](triage_nightly_run_test.py)) consolidates Nightly Examples E2E discovery, log/artifact collection, and rollout-skew inspection into a single structured JSON summary:

- Fetches `origin/main`, queries recent [`nightly-examples-e2e.yml`](../../.github/workflows/nightly-examples-e2e.yml) and [`nightly-build.yml`](../../.github/workflows/nightly-build.yml) runs, and lists open `nightly-failure` GitHub Issues.
- Reports `status_summary` (`"failing"`, `"healthy"`, `"superseded"`, `"in_progress"`, or `"no_completed_runs"`), `target_run`, `superseded_by_green_run`, and `warnings`. When auto-discovering on `main` and the latest failing run has already been superseded by a newer scheduled green run (`event == "schedule"`), returns `"healthy"` immediately without downloading stale artifacts (newer `workflow_dispatch` green runs emit a warning instead of superseding because they may be filtered or skip verification; explicit `--run-id` runs that are `"healthy"` or `"in_progress"` likewise skip artifact/skew collection).
- Maps failed `'Run & Verify: <name>'` jobs to `matrix.example.id` (via [`workflow_matrix.py`](workflow_matrix.py)), downloads failed job logs (`job_<id>.log`) plus non-expired failure artifacts (`result-<failed_id>`, `ui-screenshots-*`, and `deploy-gke-diagnostics` when the active prior `nightly-build.yml` run failed) into `${ARTIFACT_DIR:-/tmp}/nightly_triage_<run_id>`, parses `result-<failed_id>/<failed_id>.json` (`id`, `status`, `launch_status`, `verify_status`, `xid`) into `failed_jobs[].result`, and extracts bounded timestamp/ANSI-stripped `##[error]` context lines (or trailing log lines when no `##[error]` marker is present) into `failed_jobs[].error_excerpt`.
- Collects SHA-pinned `recent_commits` (`<prior_scheduled_green_e2e_sha>..<failed_e2e_sha>`, up to 30 commits; returns `[]` with a Layer B drift/flake warning when both SHAs match, or falls back with a warning to the last 20 commits at `target_run.headSha` when no prior scheduled green run exists or range inspection fails) and computes the temporal rollout-skew commit delta (`<last_green_build_sha>..<failed_e2e_sha>`) plus touched cluster-deployed server, API, and `deploy/` manifest paths since the last green `nightly-build.yml` run, recording explicit `warnings` whenever no prior green build exists, commit SHAs are invalid, or `git` inspection fails.

```bash
# Auto-discover and triage the latest failing Nightly Examples E2E run on main
PYTHONPATH=. python3 tools/nightly_tests/triage_nightly_run.py

# Triage a specific workflow run ID into a custom scratch directory
PYTHONPATH=. python3 tools/nightly_tests/triage_nightly_run.py \
  --run-id 123456789 \
  --scratch-dir /tmp/nightly_triage_123456789
```
