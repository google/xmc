# Local Standalone ClickHouse Setup for Development & Schema Testing

This directory contains configuration files for running a **standalone ClickHouse container locally** out-of-band of Cluster Toolkit (CTK) or GKE.

---

## Purpose & User Flow

### 1. Local Standalone Flow (This Directory)
- **Target User**: Developers iterating on ClickHouse SQL schemas, metrics table definitions, or writing local integration tests without needing access to a GKE cluster or running CTK blueprints.
- **Components**:
  - `deploy/local/clickhouse-standalone.yaml`: Docker Compose file exposing ClickHouse HTTP (`8123`) and TCP (`9000`) ports locally.
  - `scripts/test-clickhouse-schema.sh` / `scripts/test_clickhouse_schema.py`: Test runner script that spins up the container, waits for readiness (`/ping`), runs schema DDL/DML verification queries, and cleans up.
- **Quickstart**:
  ```bash
  # Run schema verification and cleanup container after test
  ./scripts/test-clickhouse-schema.sh --cleanup
  ```

### 2. GKE Cluster Addon Flow (Addons Directory)
- **Target User**: Developers deploying ClickHouse into a live Google Kubernetes Engine (GKE) cluster for full metrics system testing, Grafana visualization, and end-to-end integration.
- **Reference**: See [`deploy/addons/metrics/clickhouse/README.md`](../addons/metrics/clickhouse/README.md) for GKE Helm deployment instructions.

---

## Usage Instructions

### Starting the Local Container
```bash
docker compose -f deploy/local/clickhouse-standalone.yaml up -d
```

### Checking Container Health
```bash
curl http://localhost:8123/ping
# Response: Ok.
```

### Running Schema Verification Tests
```bash
python3 scripts/test_clickhouse_schema.py
```

### Stopping the Local Container
```bash
docker compose -f deploy/local/clickhouse-standalone.yaml down
```
