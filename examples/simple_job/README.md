# Simple Job Workload Launch Script

This directory contains a standalone launch script (`launch.py`) for creating a multi-WorkUnit experiment in XMC. It is designed to demonstrate multi-WorkUnit workload submission, queue scheduling, and execution lifecycle.

## Prerequisites

1. Activate the XMC virtual environment and install dependencies:
   ```bash
   source deploy/cli/venv/bin/activate
   pip install -r requirements.txt
   ```

2. **Environment Variables**:
   Verify environment variables in your active shell or `.env`:
   ```bash
   export PROJECT_ID="<your-gcp-project-id>"
   export XMC_DOMAIN="<your-xmc-domain>"
   export XMANAGER_ENDPOINT="dns:///grpc.<your-xmc-domain>:443"
   export IAP_CLIENT_ID="<your-oauth-iap-client-id>.apps.googleusercontent.com"
   export XMC_CLIENT_SA="<client-sa>@<your-gcp-project-id>.iam.gserviceaccount.com"
   ```
   Without `XMANAGER_ENDPOINT` the client dials its built-in placeholder endpoint; without
   `IAP_CLIENT_ID` / `XMC_CLIENT_SA` it prompts for them interactively.

> [!IMPORTANT]
> `--queue` takes the Kueue **LocalQueue** name (`cpu-queue`, `gpu-queue`, `tpu-queue`), not the
> ClusterQueue name (`cpu-cluster-queue`, ...). With a ClusterQueue name, Kueue reports
> `LocalQueue ... doesn't exist` and the WorkUnit never gets admitted. List the available queues with
> `kubectl get localqueues -A`.

## Invocation Examples

### Default (3 WorkUnits for 60 Seconds on the CPU Queue)
```bash
python3 examples/simple_job/launch.py --queue=cpu-queue --num_work_units=3 --duration=60 --gpus=0
```

### Targeted at the GPU Queue with High Priority
```bash
python3 examples/simple_job/launch.py --queue=gpu-queue --gpus=1 --priority=HIGH
```

### Custom WorkUnit Count & Duration
```bash
python3 examples/simple_job/launch.py --queue=cpu-queue --num_work_units=5 --duration=120
```

## Workflow
1. Run `python3 examples/simple_job/launch.py --queue=cpu-queue --num_work_units=3 --duration=60 --gpus=0`.
2. Inspect the created experiment and WorkUnits in the XMC Web UI (`https://<your-xmc-domain>/experiments/:xid`).
