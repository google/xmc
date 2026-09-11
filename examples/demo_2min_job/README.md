# Demo Workload Launch Script

> [!WARNING]
> ### ⚠️ TEMPORARY CI/CD WORKAROUNDS ACTIVE
> This example script currently contains interim compatibility patches (`_patch_grpc_auth_if_needed`) to support automated gRPC authentication under GitHub Actions Workload Identity Federation (WIF).
>
> **These workarounds are temporary and will be removed** once upstream [google-deepmind/xmanager#79](https://github.com/google-deepmind/xmanager/pull/79) is merged and the official XManager client SDK natively accepts `XMC_AUTH_TOKEN` and `XMC_USER_EMAIL`.

This directory contains a standalone launch script (`launch.py`) for creating a multi-WorkUnit demonstration experiment in XMC. It is designed to demonstrate real-time queue ingestion, priority sorting, resource capacity changes, and time-window velocity metrics in the Resource Observability Dashboard (ROD).

## Prerequisites

1. Activate the XMC virtual environment (or set up `PYTHONPATH`):
   ```bash
   source deploy/cli/venv/bin/activate
   ```
2. Verify environment variables in your active shell or `.env`:
   ```bash
   export PROJECT_ID="<your-gcp-project-id>"
   export XMC_DOMAIN="<your-xmc-domain>"
   ```

## Invocation Examples

### Default (3 WorkUnits for 60 Seconds on CPU Cluster Queue)
```bash
python3 examples/demo_2min_job/launch.py --queue=cpu-cluster-queue --num_work_units=3 --duration=60 --gpus=0
```

### Targeted at GPU Cluster Queue with High Priority
```bash
python3 examples/demo_2min_job/launch.py --queue=gpu-cluster-queue --gpus=1 --priority=HIGH
```

### Custom WorkUnit Count & Duration
```bash
python3 examples/demo_2min_job/launch.py --queue=cpu-cluster-queue --num_work_units=5 --duration=120
```

## Demo Flow
1. Open the ROD UI (`https://<your-xmc-domain>/resources?queue=cpu-cluster-queue`).
2. Run `python3 examples/demo_2min_job/launch.py --queue=cpu-cluster-queue --num_work_units=3 --duration=60 --gpus=0`.
3. Switch to the ROD dashboard to observe:
   - The pending job count and requested CPU resources increase.
   - The new workloads appearing in the **Queue Inspection Side-Panel**.
   - Hovering over the workload's **Work / Aux Unit** link navigating directly to the experiment details (`/experiments/:xid`).
