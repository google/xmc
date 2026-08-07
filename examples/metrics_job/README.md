# Metrics System XMC Example

This directory contains a complete validation example for logging training metrics via the **XMC Python SDK** (`xmc`) to the XMC Metrics Ingestion pipeline (Ingestion Gateway -> Redpanda -> ClickHouse), showcasing both manual SDK calls and automatic framework autologging.

## Contents

1. **[worker.py](./worker.py)**:
   - Python training worker using explicit `xmc.log_metric()` calls (`manual/loss`, `manual/accuracy`, `manual/learning_rate`).

2. **[worker_keras.py](./worker_keras.py)**:
   - Python training worker demonstrating **Keras autologging** (`xmc.autolog()`), which automatically intercepts `keras.Model.fit()` and streams batch/epoch metrics.

3. **[worker_lightning.py](./worker_lightning.py)**:
   - Python training worker demonstrating **PyTorch Lightning autologging** (`xmc.autolog()`), which automatically intercepts `pl.Trainer.fit()` and streams model metrics (`pl/loss`, `pl/accuracy`).

4. **[Dockerfile](./Dockerfile)**:
   - Packages `sdks/python/xmc`, PyTorch, PyTorch Lightning, Keras 3, and all three worker scripts into a Python 3.11 container image.

5. **[build_and_push.sh](./build_and_push.sh)**:
   - Builds the Docker image and pushes it to Artifact Registry (`us-docker.pkg.dev/<PROJECT_ID>/xmc/metrics-job:latest`).

6. **[launch.py](./launch.py)**:
   - XManager CLI launch script that creates an Experiment with a 3-job WorkUnit (`metrics-manual`, `metrics-keras`, `metrics-lightning`).

---

## Usage

### 1. Build and Push the Image

From this directory, run:
```bash
chmod +x build_and_push.sh
./build_and_push.sh
```

### 2. Launch the Multi-Job Metrics Experiment

Ensure you have your virtual environment active and `.env` configured:
```bash
source ../../deploy/cli/venv/bin/activate
python3 launch.py
```

### 3. Verification

Verify that metrics arrive in ClickHouse for all three frameworks:
```bash
kubectl exec -n xmc clickhouse-d99cbff55-lc6wc -- clickhouse-client --query "SELECT metric_name, count() FROM default.xmc_metrics WHERE xid = <xid> GROUP BY metric_name"
```
