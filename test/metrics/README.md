# XMC Metrics Telemetry Environment Variables

This document lists all environment variables used by the XMC Python SDK (`xmc`), Ingestion Gateway, and metrics test runners, along with their expected types, requirements, and default values.

---

## Environment Variables Reference

| Variable Name | Type | Required? | Default Value | Description |
| :--- | :--- | :--- | :--- | :--- |
| `XMC_XID` | Integer / String | **Yes** | *None* | XManager Experiment ID (e.g. `535543832`). Mandatory for metric aggregation; missing value silences metrics (fail-closed). |
| `XMC_WORK_UNIT_ID` | Integer / String | **Yes** | *None* | XManager Work Unit ID (e.g. `100`). Represents a specific work unit within an experiment; missing value silences metrics. |
| `XMC_EXECUTION_ID` | Integer / String | **Yes** | *None* | Execution attempt ID (e.g. `1`). Incremented on task restarts; missing value silences metrics. |
| `XMC_REPLICA_ID` | String | No | `"0"` | Distributed worker replica ID. If omitted, harvested automatically via framework rank heuristics (`RANK`, `JAX_PROCESS_INDEX`, `OMPI_COMM_WORLD_RANK`, or `JOB_COMPLETION_INDEX`). |
| `XMC_GATEWAY_URL` | String (URL) | No | `"http://localhost:disabled"` | Base URL of the target Ingestion Gateway endpoint (e.g. `http://ingestion-gateway.xmc.svc:8080`). If omitted, metrics collection is disabled. |
| `XMC_API_KEY` | String | No | *None* | Authentication bearer token passed in `Authorization: Bearer <API_KEY>` HTTP headers. |
| `XMC_METRICS_DISABLED` | Boolean String | No | `"0"` / `"false"` | Silences all metrics collection when set to a truthy value (`"1"`, `"true"`, `"yes"`). |
| `XMC_DISABLE_AUTOLOG` | Boolean String | No | `"0"` / `"false"` | Disables framework autologging integration (PyTorch / JAX) when set to `"1"` or `"true"`. |
