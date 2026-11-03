# Example Post-Launch E2E Verification Tests

This directory contains post-launch end-to-end (E2E) verification tests (`*_test.py`) for the runnable workloads and dashboards under [`examples/`](../../../examples/) as well as Web UI Critical User Journeys (CUJs).

> [!IMPORTANT]
> **Run the Corresponding Example First**: Despite the `*_test.py` suffix, these scripts are **not** standalone hermetic unit tests. Workload and dashboard tests in this directory **must only be run after launching the corresponding example** from [`examples/`](../../../examples/) against a deployed XMC GKE environment and passing the resulting `--xid` (or `--dashboard_id`).
>
> Because they require a live XMC control plane and a pre-launched workload identifier, [`BUILD.bazel`](BUILD.bazel) registers them as `py_binary` targets rather than `py_test` targets so offline `bazel test //...` runs remain hermetic.

---

## 1. Two-Step Execution Flow

In CI, [`.github/workflows/nightly-examples-e2e.yml`](../../../.github/workflows/nightly-examples-e2e.yml) (`strategy.matrix.example`) is the single source of truth for launch entrypoints, arguments, port-forwards, and `verify_module` pairings. Follow the same two-step order when running locally from the repository root after authenticating (see [Authentication and Experiment Access](../../../docs/authentication.md)):

1. **Launch the prerequisite example from [`examples/`](../../../examples/)** and record the printed `Experiment XID` (or `Dashboard ID`):
   ```bash
   (cd examples/simple_job && python3 launch.py --queue=cpu-queue --num_work_units=3 --duration=60 --gpus=0)
   # Output prints: Experiment XID: <xid>
   ```
2. **Run the corresponding `*_test.py` module from the repository root**, passing an `--xid` launched under the same caller identity (see CLI flags in [`../verification/verifier_cli.py`](../verification/verifier_cli.py)):
   ```bash
   python3 -m tools.nightly_tests.examples_verification.simple_job_test \
     --xid="<xid>" \
     --expected_queue="cpu-queue" \
     --endpoint="grpc.example.com:443"
   ```

---

## 2. Naming Convention & Standalone Exceptions

- **1-to-1 Example Tests (`<example_name>_test.py`)**: Run after launching [`examples/<example_name>/`](../../../examples/) (`--xid=<xid>`, or `--dashboard_id=<id>` for [`xdash_dashboard_test.py`](xdash_dashboard_test.py)).
- **Shared [`examples/simple_job/`](../../../examples/simple_job/) Suites**: [`simple_job_ui_test.py`](simple_job_ui_test.py), [`multikueue_job_test.py`](multikueue_job_test.py) (`--queue=multikueue-cpu-queue`), and [`resource_observability_dashboard_ui_test.py`](resource_observability_dashboard_ui_test.py) (`--duration=180`, or standalone without `--xid`) verify different stack layers against a `simple_job` launch.
- **Piggybacked Dashboard UI Test**: [`metrics_dashboard_ui_test.py`](metrics_dashboard_ui_test.py) runs via [`metrics_dashboard_job_test.py`](metrics_dashboard_job_test.py) (`--verify_ui=true`) or directly with `--xid` after [`examples/metrics_dashboard_job/`](../../../examples/metrics_dashboard_job/).
- **Standalone Global UI Test**: [`admin_panel_ui_test.py`](admin_panel_ui_test.py) verifies `/admin` directly against a live cluster (`--ui_base_url=<url>`) without a prior example launch.

See [`../README.md`](../README.md) for shared primitives under [`../verification/`](../verification/), authoring patterns, and the coverage check in [`../check_examples_coverage.py`](../check_examples_coverage.py).
