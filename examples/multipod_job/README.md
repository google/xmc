# Multi-Pod & Sidecar XMC Example

This directory contains a complete validation example for running multi-pod and multi-container (sidecar) jobs on XManager Cloud (XMC).

## Contents

1. **[counter.py](./counter.py)**:
   - Python script that prints sequential integers to stdout for `N` minutes (`DURATION_MINUTES` env var, default: `2`).
   - Prefixes each line with a pod/container identifier (`POD_PREFIX` env var or hostname).

2. **[Dockerfile](./Dockerfile)**:
   - Packages `counter.py` into a minimal Python 3.11 container image.

3. **[build_and_push.sh](./build_and_push.sh)**:
   - Builds the Docker image for `linux/amd64` and pushes it to Artifact Registry (`us-docker.pkg.dev/<PROJECT_ID>/xmc/multipod-counter:latest`).

4. **[launch.py](./launch.py)**:
   - XManager CLI launch script that creates a multi-pod and sidecar WorkUnit on XMC with **deterministic DNS hostnames** (`enable_dns_hostnames=True`, `publish_not_ready_addresses=True`) and **two distinct Pod templates** in the JobSet:
     - **Pod Role 1 (`counter-workers`)**: 2 parallel Pod replicas (`replicas = 2`). Each Pod contains:
       - Primary Container: `primary-counter` running `multipod-counter:latest`.
       - Sidecar Container: `sidecar-counter` running `multipod-counter:latest`.
     - **Pod Role 2 (`secondary-counter`)**: 1 Pod replica (`replicas = 1`) running a second distinct role in the same WorkUnit (`secondary-counter`).
     - Replicas discover and resolve peer pod FQDNs immediately (`<jobset>-<replicated-job>-<job-idx>-<pod-idx>.<subdomain>`).

5. **[requirements.txt](./requirements.txt)**:
   - Python dependencies list.

---

## Prerequisites & Dependencies

1. **Client-Side Dependencies**: Install the host Python dependencies required to execute `launch.py`:
   ```bash
   pip install -r requirements.txt
   ```

2. **Environment Variables**: Configure `PROJECT_ID`, `XMC_DOMAIN`, and `QUEUE_NAME` in your shell or `.env` file.

---

## Usage

### 1. Build and Push the Image
```bash
./build_and_push.sh
```

### 2. Launch the Multi-Pod & Sidecar Experiment
Create your virtual environment, install dependencies, and launch:
```bash
python3 -m venv .venv
source .venv/bin/activate
pip install -r requirements.txt
python3 launch.py
```
