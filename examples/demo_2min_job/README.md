# 2-Minute Demo Workload Launch Script

This directory contains a standalone launch script (`launch.py`) for creating a 2-minute demonstration experiment in XMC. It is designed to demonstrate real-time queue ingestion, priority sorting, resource capacity changes, and time-window velocity metrics in the Resource Observability Dashboard (ROD).

## Prerequisites

1. Activate the XMC virtual environment (or set up `PYTHONPATH`):
   ```bash
   source deploy/cli/venv/bin/activate
   ```
2. Verify environment variables in your active shell or `.env`:
   ```bash
   export PROJECT_ID="xmc-test-project-2"
   export XMC_DOMAIN="ui.janzak.xmanageriso.dev"
   ```

## Invocation Examples

### Default (2 Minutes on GPU Cluster Queue, High Priority)
```bash
python3 examples/demo_2min_job/launch.py --queue=gpu-cluster-queue
```

### Targeted at CPU Queue with Normal Priority
```bash
python3 examples/demo_2min_job/launch.py --queue=cpu-cluster-queue --gpus=0 --priority=NORMAL
```

### Custom Duration (e.g. 3 Minutes)
```bash
python3 examples/demo_2min_job/launch.py --queue=gpu-cluster-queue --duration=180
```

## Demo Flow
1. Open the ROD UI (`https://ui.janzak.xmanageriso.dev/resources?queue=gpu-cluster-queue`).
2. Run `python3 examples/demo_2min_job/launch.py --queue=gpu-cluster-queue --priority=HIGH`.
3. Switch to the ROD dashboard to observe:
   - The pending job count and requested GPU resources increase.
   - The new workload appearing in the **Queue Inspection Side-Panel**.
   - Hovering over the workload's **Work / Aux Unit** link navigating directly to the experiment details (`/experiments/:xid`).
