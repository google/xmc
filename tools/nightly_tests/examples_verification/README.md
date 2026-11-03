# Example Post-Launch E2E Verification Tests

This directory contains end-to-end (E2E) verification tests (`*_test.py`) for the runnable workloads and dashboards under [`examples/`](../../../examples/) as well as Web UI Critical User Journeys (CUJs).

> [!IMPORTANT]
> **Run the Corresponding Example First**: Despite the `*_test.py` suffix, these scripts are **not** standalone hermetic unit tests. Workload and dashboard tests in this directory **must only be run after launching the corresponding example** from [`examples/`](../../../examples/) against a deployed XMC GKE environment and passing the resulting `--xid` (or `--dashboard_id`).
>
> Because they require a live XMC control plane and a pre-launched workload identifier, [`BUILD.bazel`](BUILD.bazel) registers them as `py_binary` targets rather than `py_test` targets so offline `bazel test //...` runs remain hermetic.

---

## 1. Two-Step Execution Flow

In CI, [`.github/workflows/nightly-examples-e2e.yml`](../../../.github/workflows/nightly-examples-e2e.yml) executes each scenario in two sequential steps. Follow the same order when running locally from the repository root after authenticating (see [Authentication and Experiment Access](../../../docs/authentication.md)):

1. **Launch the prerequisite example from [`examples/`](../../../examples/)** and record the printed `Experiment XID` (or `Dashboard ID`):
   ```bash
   (cd examples/simple_job && python3 launch.py --queue=cpu-queue --num_work_units=3 --duration=60 --gpus=0)
   # Output prints: Experiment XID: <xid>
   ```
2. **Run the corresponding `*_test.py` module from the repository root**, passing that identifier and target endpoints (see CLI flags in [`../verification/verifier_cli.py`](../verification/verifier_cli.py)):
   ```bash
   python3 -m tools.nightly_tests.examples_verification.simple_job_test \
     --xid="<xid>" \
     --expected_queue="cpu-queue" \
     --endpoint="grpc.example.com:443"
   ```

> [!NOTE]
> Verification tests read the experiment using your caller identity, so always pass the `--xid` of an experiment launched under the same user or service account.

---

## 2. Example-to-Test Mapping

| Test Script | Prerequisite Example Directory | Required Identifier / Invocation Notes |
| :--- | :--- | :--- |
| [`simple_job_test.py`](simple_job_test.py) | [`examples/simple_job/`](../../../examples/simple_job/) | `--xid=<xid>` |
| [`simple_job_ui_test.py`](simple_job_ui_test.py) | [`examples/simple_job/`](../../../examples/simple_job/) | `--xid=<xid> --ui_base_url=<url>` |
| [`multikueue_job_test.py`](multikueue_job_test.py) | [`examples/simple_job/`](../../../examples/simple_job/) | `--xid=<xid>` (launched with `--queue=multikueue-cpu-queue`) |
| [`resource_observability_dashboard_ui_test.py`](resource_observability_dashboard_ui_test.py) | [`examples/simple_job/`](../../../examples/simple_job/) | `--xid=<xid> --ui_base_url=<url>` (launched with `--duration=180` for active queue assertions; also supports standalone UI mode without `--xid`) |
| [`init_container_job_test.py`](init_container_job_test.py) | [`examples/init_container_job/`](../../../examples/init_container_job/) | `--xid=<xid>` |
| [`sidecar_test.py`](sidecar_test.py) | [`examples/sidecar/`](../../../examples/sidecar/) | `--xid=<xid>` |
| [`multipod_job_test.py`](multipod_job_test.py) | [`examples/multipod_job/`](../../../examples/multipod_job/) | `--xid=<xid>` |
| [`chart_artifact_job_test.py`](chart_artifact_job_test.py) | [`examples/chart_artifact_job/`](../../../examples/chart_artifact_job/) | `--xid=<xid>` (requires ClickHouse & Dashboard Service port-forwards) |
| [`clickhouse_metrics_job_test.py`](clickhouse_metrics_job_test.py) | [`examples/clickhouse_metrics_job/`](../../../examples/clickhouse_metrics_job/) | `--xid=<xid>` (requires ClickHouse & Dashboard Service port-forwards) |
| [`metrics_job_test.py`](metrics_job_test.py) | [`examples/metrics_job/`](../../../examples/metrics_job/) | `--xid=<xid>` (requires ClickHouse port-forward) |
| [`metrics_dashboard_job_test.py`](metrics_dashboard_job_test.py) | [`examples/metrics_dashboard_job/`](../../../examples/metrics_dashboard_job/) | `--xid=<xid>` (pass `--verify_ui=true --ui_base_url=<url>` to also run [`metrics_dashboard_ui_test.py`](metrics_dashboard_ui_test.py)) |
| [`metrics_dashboard_ui_test.py`](metrics_dashboard_ui_test.py) | [`examples/metrics_dashboard_job/`](../../../examples/metrics_dashboard_job/) | `--xid=<xid> --ui_base_url=<url>` (invoked by [`metrics_dashboard_job_test.py`](metrics_dashboard_job_test.py) or directly after workload completion) |
| [`managed_xprof_artifact_job_test.py`](managed_xprof_artifact_job_test.py) | [`examples/managed_xprof_artifact_job/`](../../../examples/managed_xprof_artifact_job/) | `--xid=<xid>` |
| [`workunit_artifact_job_test.py`](workunit_artifact_job_test.py) | [`examples/workunit_artifact_job/`](../../../examples/workunit_artifact_job/) | `--xid=<xid>` |
| [`per_job_node_selector_test.py`](per_job_node_selector_test.py) | [`examples/per_job_node_selector/`](../../../examples/per_job_node_selector/) | `--xid=<xid>` (verifies pending GPU/node-selector specs, then stops WorkUnits) |
| [`pathways_job_test.py`](pathways_job_test.py) | [`examples/pathways_job/`](../../../examples/pathways_job/) | `--xid=<xid>` (verifies pending TPU Pathways JobSet topology, then stops WorkUnits) |
| [`xdash_dashboard_test.py`](xdash_dashboard_test.py) | [`examples/xdash_dashboard/`](../../../examples/xdash_dashboard/) | `--dashboard_id=<id>` (after running `create_dashboard.py`) |
| [`admin_panel_ui_test.py`](admin_panel_ui_test.py) | *None (standalone global UI route)* | `--ui_base_url=<url>` (verifies `/admin` directly against a live XMC cluster) |

---

## 3. Adding or Updating Tests

See [`../README.md`](../README.md) for shared verification primitives under [`../verification/`](../verification/), canonical test patterns, and the automated example coverage check enforced by [`../check_examples_coverage.py`](../check_examples_coverage.py).
