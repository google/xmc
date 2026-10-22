# Init Container XMC Example

This directory contains a complete validation example for running **Init Containers** alongside primary app containers in jobs on XManager Cloud (XMC).

## Contents

1. **[init_step.py](./init_step.py)**:
   - Python script that runs as the init container (`init-setup`).
   - Simulates pre-processing or environment initialization by logging messages and creating a completion marker file `/dev/disk-scratch/init_ready.flag`.

2. **[worker.py](./worker.py)**:
   - Python script that runs as the primary application container (`primary-worker`).
   - Verifies that `/dev/disk-scratch/init_ready.flag` was created by the init container before performing work.

3. **[Dockerfile](./Dockerfile)**:
   - Packages `init_step.py` and `worker.py` into a minimal Python 3.11 container image.

4. **[build_and_push.sh](./build_and_push.sh)**:
   - Builds the Docker image for `linux/amd64` and pushes it to Artifact Registry (`us-docker.pkg.dev/<PROJECT_ID>/xmc/init-container-job:latest`).

5. **[launch.py](./launch.py)**:
   - XManager CLI launch script that creates a WorkUnit specifying an init container (`init_containers`) that runs before the primary container (`spec`).

6. **[requirements.txt](./requirements.txt)**:
   - Python dependencies list.

---

## Prerequisites & Dependencies

1. **Client-Side Dependencies**: Install the host Python dependencies required to execute `launch.py`:
   ```bash
   pip install -r requirements.txt
   ```

2. **Environment Variables**: Configure `PROJECT_ID`, `XMC_DOMAIN`, and `QUEUE_NAME` in your shell or `.env` file.

3. **Authentication**: Run `xmanager login`. You own the experiments you launch. See [Authentication and Experiment Access](../../docs/authentication.md) for CI service accounts and sharing.

---

## Usage

### 1. Build and Push the Image
```bash
./build_and_push.sh
```

### 2. Launch the Init Container Experiment
Ensure you have installed the XManager CLI, activate your virtual environment, and install dependencies:
```bash
source ../../deploy/cli/venv/bin/activate
pip install -r requirements.txt
python3 launch.py
```

### 3. Verification
Verify logs in the XMC UI or via `kubectl`:
```bash
kubectl logs <pod-name> -c init-setup
kubectl logs <pod-name> -c primary-worker
```
The init container (`init-setup`) logs completion before the primary container (`primary-worker`) begins execution.
