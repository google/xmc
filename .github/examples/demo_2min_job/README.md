# Demo Workload Launch Script

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
python3 .github/examples/demo_2min_job/launch.py --queue=cpu-cluster-queue --num_work_units=3 --duration=60 --gpus=0
```

### Targeted at GPU Cluster Queue with High Priority
```bash
python3 .github/examples/demo_2min_job/launch.py --queue=gpu-cluster-queue --gpus=1 --priority=HIGH
```

### Custom WorkUnit Count & Duration
```bash
python3 .github/examples/demo_2min_job/launch.py --queue=cpu-cluster-queue --num_work_units=5 --duration=120
```

## Demo Flow
1. Open the ROD UI (`https://<your-xmc-domain>/resources?queue=cpu-cluster-queue`).
2. Run `python3 .github/examples/demo_2min_job/launch.py --queue=cpu-cluster-queue --num_work_units=3 --duration=60 --gpus=0`.
3. Switch to the ROD dashboard to observe:
   - The pending job count and requested CPU resources increase.
   - The new workloads appearing in the **Queue Inspection Side-Panel**.
   - Hovering over the workload's **Work / Aux Unit** link navigating directly to the experiment details (`/experiments/:xid`).

