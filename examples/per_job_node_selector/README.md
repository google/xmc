# Per-Job `nodeSelector` Example

This example demonstrates how to configure per-job `node_selector` maps on distinct `KubernetesJob` specifications inside a `JobSetExecutable` using XManager Cloud (XMC).

## Overview

In multi-job distributed machine learning workloads (`JobSet`s), it is standard practice to schedule distinct pod roles on different Kubernetes node pools:
- **Head Coordinator Pod (`head`)**: Pinned to a CPU-only node pool (`cloud.google.com/gke-nodepool: cpups`), requesting standard CPU and memory resources.
- **Worker Accelerator Pods (`worker`)**: Pinned to an accelerator node pool (`cloud.google.com/gke-nodepool: gpu-pool`), requesting GPU/TPU resources via `ResourceSet`.

When an XMC Work Unit is created with these requirements:
1. The `node_selector` field from each `KubernetesJob` (`head` and `worker`) is copied directly into the respective `PodSpec.NodeSelector` when translating to a `ReplicatedJob`.
2. The GKE execution environment automatically merges specific accelerator keys (e.g., `cloud.google.com/gke-accelerator: nvidia-tesla-t4`) into the `worker` job's `NodeSelector` map without overwriting or conflicting with the pre-populated node pool selector (`cloud.google.com/gke-nodepool: gpu-pool`).

## Directory Contents

- **`launch.py`**: The XManager launch script that creates the distributed `WorkUnit` specifying custom `node_selector` labels per job.
- **`coordinator.py`**: The workload script executed by the `head` coordinator pod on the CPU-only node pool.
- **`worker.py`**: The workload script executed by the `worker` pods on the GPU node pool.
- **`Dockerfile`**: Minimal multi-role container image definition containing both `coordinator.py` and `worker.py`.
- **`build_and_push.sh`**: Utility script to build and publish the container image to Google Cloud Artifact Registry using Cloud Build.
- **`requirements.txt`**: Python dependencies list.

## Prerequisites

1. **Client-Side Dependencies**: Install the host Python dependencies required to execute `launch.py`:
   ```bash
   pip install -r requirements.txt
   ```

2. **GKE Node Pools**: Your GKE cluster must have the required node pools (`cpups` and `gpu-pool`, or modify `launch.py` to match your cluster's existing node pool names).
3. **Environment Variables**: Ensure `PROJECT_ID`, `XMC_DOMAIN`, and optionally `QUEUE_NAME` are exported in your environment or defined in a `.env` file.

## Step 1: Build and Push Container Image

```bash
export PROJECT_ID="my-gcp-project"
./build_and_push.sh
```

## Step 2: Launch the Experiment

```bash
python3 -m venv .venv
source .venv/bin/activate
pip install -r requirements.txt
python3 launch.py
```

Upon creation, the script prints the Work Unit resource name and the XMC dashboard URL where you can monitor job scheduling across node pools.
