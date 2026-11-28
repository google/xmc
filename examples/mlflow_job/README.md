<!--
Copyright 2026 Google LLC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
-->

# MLflow Sweep Example

This directory contains a 2x2 hyperparameter sweep (two learning rates x two seeds, four `STANDARD` WorkUnits) demonstrating XMC's MLflow tracking integration. The launcher contains **no MLflow code**, and the training worker uses only standard `mlflow` APIs (`mlflow.start_run()`, `mlflow.log_params()`, `mlflow.log_metric()`).

## Contents

1. **[launch.py](./launch.py)**:
   - Submits an XMC experiment with 4 `STANDARD` WorkUnits (`lr` in `0.01,0.001` x `seed` in `1,2`).
2. **[worker.py](./worker.py)**:
   - Standard MLflow training loop logging `loss` and `accuracy` curves into the XMC-injected `MLFLOW_RUN_ID`.
3. **[Dockerfile](./Dockerfile)**:
   - Non-root `python:3.11.18-slim` container with `mlflow==3.1.1` and `worker.py`.
4. **[build_and_push.sh](./build_and_push.sh)**:
   - Builds and pushes `us-docker.pkg.dev/<PROJECT_ID>/xmc/mlflow-job:latest`.
5. **[requirements.txt](./requirements.txt)**:
   - Launcher Python dependencies (plus pinned `mlflow==3.1.1`).

---

## Prerequisites

1. **MLflow Component Enabled**: An admin must enable the `mlflow` component in `deploy/profiles/stable-2026b` (for local KinD testing, see [the MLflow User Guide](../../docs/mlflow.md) and the [dev/test MLflow fixture](../../test/e2e/testdata/mlflow/README.md)).
2. **Environment Variables**: Configure `PROJECT_ID`, `XMC_DOMAIN`, and `QUEUE_NAME` in your shell or `.env` file. For local KinD clusters, set `IMAGE_PATH=xmc-mlflow-job:latest` after loading the image into KinD.
3. **Authentication**: Run `xmanager login` after setting up your virtual environment (see [Authentication and Experiment Access](../../docs/authentication.md)).

---

## Usage

### 1. Build and Push (or Load into KinD)

For GKE / Artifact Registry:

```bash
chmod +x build_and_push.sh
./build_and_push.sh
```

For local KinD:

```bash
docker build -t xmc-mlflow-job:latest examples/mlflow_job
kind load docker-image xmc-mlflow-job:latest --name "<kind_cluster_name>"
export IMAGE_PATH="xmc-mlflow-job:latest"
```

### 2. Launch the Sweep

```bash
python3 ../../deploy/cli/install_xmanager.py --venv .venv --requirements requirements.txt
source .venv/bin/activate
xmanager login
python3 launch.py --queue="${QUEUE_NAME:-cpu-queue}"
```

### 3. Verify End-to-End

Run the E2E verifier against the launched `<xid>` and your MLflow tracking URI (for KinD, port-forward `svc/mlflow-service 5000:5000 -n xmc` first):

```bash
python3 -m tools.nightly_tests.examples_verification.mlflow_job_test \
  --xid="<xid>" \
  --expected_queue="${QUEUE_NAME:-cpu-queue}" \
  --expected_work_units=4 \
  --mlflow_uri="http://localhost:5000"
```
