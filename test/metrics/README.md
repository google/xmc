# Hermetic Test Setup Environment Variables

This document lists the environment variables used by the `test/metrics` package and its hermetic test runner harness (`hermetic_telemetry_test.py` and `hermetic_runner/main.go`).

---

## 1. Hermetic Workload Metadata Environment Variables

These environment variables are set in the worker environment by `run_simulated_training_workers` to simulate realistic multi-WorkUnit, multi-replica ML training workloads.

| Variable Name | Type | Required? | Default Value | Description in Hermetic Test Setup |
| :--- | :--- | :--- | :--- | :--- |
| `XMC_XID` | Integer / String | **Yes** | *None* | XManager Experiment ID (e.g. `"535543832"`). Identifies the experiment run; missing value silences metrics (fail-closed). |
| `XMC_WORK_UNIT_ID` | Integer / String | **Yes** | *None* | XManager Work Unit ID (e.g. `"100"`). Identifies individual work units within the experiment. |
| `XMC_EXECUTION_ID` | Integer / String | **Yes** | *None* | Execution attempt ID (e.g. `"1"`). Tracks job attempt retries. |
| `XMC_REPLICA_ID` | String | No | `"0"` | Distributed worker replica ID (e.g. `"0"`, `"1"`). Differentiates parallel worker threads. |
| `XMC_METRICS_DISABLED` | Boolean String | No | `"0"` / `"false"` | Silences metrics ingestion when set to `"1"` or `"true"`. |

---

## 2. Bazel Test Execution Environment Variables

These environment variables are automatically injected by Bazel when running `bazel test //test/metrics:hermetic_telemetry_test`.

| Variable Name | Type | Required? | Default Value | Description in Hermetic Test Setup |
| :--- | :--- | :--- | :--- | :--- |
| `TEST_SRCDIR` | String (Path) | No | *None* | Path to Bazel test runfiles root directory. Used to locate compiled binaries (`hermetic_runner`) and schema DDL (`schema.sql`). |
| `TEST_WORKSPACE` | String | No | `"xmanager_cloud"` | Name of the Bazel workspace module. |
