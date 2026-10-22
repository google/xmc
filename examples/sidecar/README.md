# Sidecar & Init Container XMC Example

This directory contains a complete validation example demonstrating how to run **both an Init Container and a Kubernetes Native Sidecar Container** alongside a primary application container in an XManager Cloud (XMC) WorkUnit.

## Contents

1. **[init_step.py](./init_step.py)**:
   - Python script running in the sequential init container (`init-setup`).
   - Performs initialization tasks and writes a completion marker `/dev/disk-scratch/init_ready.flag`.

2. **[sidecar.py](./sidecar.py)**:
   - Python script running in the Kubernetes Native Sidecar Container (`sidecar-service`, emitted as an init container with `restartPolicy: Always`).
   - Runs a continuous background monitoring loop checking `/dev/disk-scratch/init_ready.flag` and logging heartbeat metrics.
   - Automatically terminated by Kubernetes when the primary container (`primary-worker`) completes.

3. **[worker.py](./worker.py)**:
   - Python script running in the primary application container (`primary-worker`).
   - Verifies that `/dev/disk-scratch/init_ready.flag` was created by `init-setup`, simulates a workload for `DURATION_MINUTES` (default: 1), and exits cleanly.

4. **[Dockerfile](./Dockerfile)**:
   - Packages `init_step.py`, `sidecar.py`, and `worker.py` into a minimal Python 3.11 container image.

5. **[build_and_push.sh](./build_and_push.sh)**:
   - Builds the Docker image for `linux/amd64` and pushes it to Artifact Registry (`us-docker.pkg.dev/<PROJECT_ID>/xmc/sidecar-job:latest`).

6. **[launch.py](./launch.py)**:
   - XManager CLI launch script that creates an XMC WorkUnit defining:
     - `init_containers`: `init-setup` running `init_step.py`.
     - `sidecars`: `sidecar-service` running `sidecar.py`.
     - `spec`: `primary-worker` running `worker.py`.

7. **[requirements.txt](./requirements.txt)**:
   - Python dependencies list.

---

## Prerequisites & Dependencies

1. **Environment Variables**: Configure `PROJECT_ID`, `XMC_DOMAIN`, and `QUEUE_NAME` in your shell or `.env` file.

2. **Authentication**: After creating and activating your `.venv` (see below), run `xmanager login`. You own the experiments you launch. See [Authentication and Experiment Access](../../docs/authentication.md) for CI service accounts and sharing.

---

## Usage

### 1. Build and Push the Image
```bash
./build_and_push.sh
```

### 2. Launch the Experiment
Create your virtual environment, install dependencies, compile local XMC protos, authenticate, and launch:
```bash
python3 ../../deploy/cli/install_xmanager.py --venv .venv --requirements requirements.txt
source .venv/bin/activate
xmanager login
python3 launch.py
```

### 3. Verification
Inspect logs in the XMC UI or via `kubectl`:
```bash
# View sequential init container logs (runs to completion first)
kubectl logs <pod-name> -c init-setup

# View sidecar logs (starts after init-setup, runs continuously)
kubectl logs <pod-name> -c sidecar-service

# View primary application logs
kubectl logs <pod-name> -c primary-worker
```
When `primary-worker` completes successfully, Kubernetes automatically shuts down `sidecar-service` and marks the Pod as `Completed`.
