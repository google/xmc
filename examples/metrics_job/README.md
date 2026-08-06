# Metrics System XMC Example

This directory contains a complete validation example for logging training metrics via the **XMC Python SDK** (`xmc`) to the XMC Metrics Ingestion pipeline (Ingestion Gateway -> Redpanda -> ClickHouse).

## Contents

1. **[worker.py](./worker.py)**:
   - Python training script running inside the primary application container.
   - Imports `xmc` and uses `xmc.log_metric()` to record training loss, accuracy, and learning rate metrics during simulated training iterations.

2. **[Dockerfile](./Dockerfile)**:
   - Packages `sdks/python/xmc` and `worker.py` into a Python 3.11 container image.

3. **[build_and_push.sh](./build_and_push.sh)**:
   - Builds the Docker image and pushes it to Artifact Registry (`us-docker.pkg.dev/<PROJECT_ID>/xmc/metrics-job:latest`).

4. **[launch.py](./launch.py)**:
   - XManager CLI launch script that creates an Experiment and WorkUnit configured with `XMC_GATEWAY_URL` pointing to the in-cluster Ingestion Gateway.

---

## Usage

### 1. Build and Push the Image

From this directory, run:
```bash
chmod +x build_and_push.sh
./build_and_push.sh
```

### 2. Launch the Metrics Experiment

Ensure you have your virtual environment active and `.env` configured:
```bash
source ../../deploy/cli/venv/bin/activate
python3 launch.py
```

### 3. Verification

Verify that metrics arrive in ClickHouse:
```bash
kubectl exec -n xmc clickhouse-d99cbff55-lc6wc -- clickhouse-client --query "SELECT * FROM default.xmc_metrics WHERE xid = <xid>"
```
