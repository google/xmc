# XMC Metrics Telemetry Environment Variables

This document lists all environment variables used by the XMC Python SDK (`xmc`), Ingestion Gateway, and test runners for metrics harvesting, routing, and cluster replica detection.

---

## 1. Primary Metadata Environment Variables

| Variable Name | Type | Required? | Description & Expected Values |
| :--- | :--- | :--- | :--- |
| `XMC_XID` | Integer / String | **Yes** | XManager Experiment ID (e.g. `535543832`). Mandatory for metric aggregation. |
| `XMC_WORK_UNIT_ID` | Integer / String | **Yes** | XManager Work Unit ID (e.g. `100`). Represents a specific job/work unit within an experiment. |
| `XMC_EXECUTION_ID` | Integer / String | **Yes** | Execution attempt/retry ID (e.g. `1`). Incremented on task restarts. |
| `XMC_REPLICA_ID` | String | No | Distributed training worker replica ID (e.g. `"0"`, `"1"`). If omitted, harvested automatically via framework rank heuristics. |

---

## 2. Ingestion & Connectivity Environment Variables

| Variable Name | Type | Required? | Description & Expected Values |
| :--- | :--- | :--- | :--- |
| `XMC_GATEWAY_URL` | String (URL) | No | Target Ingestion Gateway base URL (e.g. `http://ingestion-gateway.xmc.svc:8080` or `https://xmc.googleapis.com`). |
| `XMC_API_KEY` | String | No | Authentication token / API key passed in `Authorization: Bearer <API_KEY>` HTTP headers. |
| `XMC_METRICS_DISABLED` | Boolean String | No | Silences metrics collection when set to a truthy value (`"1"`, `"true"`, `"yes"`). |
| `XMC_DISABLE_AUTOLOG` | Boolean String | No | Disables framework autologging (e.g. PyTorch/JAX autolog integration) when set to `"1"` or `"true"`. |

---

## 3. Distributed ML Rank Harvesting Fallbacks

When `XMC_REPLICA_ID` is not explicitly set, the XMC Python SDK evaluates process environment variables in 3 stages to determine worker rank and rank-zero logging status:

### Stage 1: Framework Rank Detection Variables
- `RANK`: PyTorch Distributed / TorchRun worker rank (e.g. `0`, `1`).
- `JAX_PROCESS_INDEX`: JAX Multi-Host process index.
- `OMPI_COMM_WORLD_RANK`: OpenMPI / Slurm worker rank.

### Stage 2: Generic Distributed Indicators (Fail-Closed Safety)
- `WORLD_SIZE`: PyTorch / MPI world size (silences if `> 1` without explicit rank).
- `RAY_ADDRESS`: Ray Cluster address indicator.
- `MASTER_ADDR`: PyTorch DDP master node address.

### Stage 3: Orchestrator Index Fallbacks
- `XMC_JOB_INDEX`: XMC Orchestrator pod index.
- `JOB_COMPLETION_INDEX`: Kubernetes Indexed Job completion index (`0`..`N-1`).
