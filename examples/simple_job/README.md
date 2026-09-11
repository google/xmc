# Simple Job Workload Launch Script

This directory contains a standalone launch script (`launch.py`) for creating a multi-WorkUnit experiment in XMC. It is designed to demonstrate multi-WorkUnit workload submission, queue scheduling, and execution lifecycle.

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
python3 examples/simple_job/launch.py --queue=cpu-cluster-queue --num_work_units=3 --duration=60 --gpus=0
```

### Targeted at GPU Cluster Queue with High Priority
```bash
python3 examples/simple_job/launch.py --queue=gpu-cluster-queue --gpus=1 --priority=HIGH
```

### Custom WorkUnit Count & Duration
```bash
python3 examples/simple_job/launch.py --queue=cpu-cluster-queue --num_work_units=5 --duration=120
```

## Workflow
1. Run `python3 examples/simple_job/launch.py --queue=cpu-cluster-queue --num_work_units=3 --duration=60 --gpus=0`.
2. Inspect the created experiment and WorkUnits in the XMC Web UI (`https://<your-xmc-domain>/experiments/:xid`).
