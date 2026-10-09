# MultiKueue Delegated Workload Launch Script

This directory contains a standalone launch script (`launch.py`) for submitting an XMC experiment to a MultiKueue-backed queue (`multikueue-cpu-queue`). Workloads submitted to `multikueue-cpu-queue` are admitted on the management cluster and delegated by Kueue's MultiKueue controller (`kueue.x-k8s.io/multikueue`) to the target worker cluster (`xmc-nightly-worker-1`).

## Prerequisites

1. Activate the XMC virtual environment and install dependencies:
   ```bash
   source deploy/cli/venv/bin/activate
   pip install -r examples/multikueue_job/requirements.txt
   ```

2. **Environment Variables**:
   ```bash
   export PROJECT_ID="<your-gcp-project-id>"
   export XMC_DOMAIN="<your-xmc-domain>"
   export XMANAGER_ENDPOINT="dns:///grpc.<your-xmc-domain>:443"
   export IAP_CLIENT_ID="<your-oauth-iap-client-id>.apps.googleusercontent.com"
   export XMC_CLIENT_SA="<client-sa>@<your-gcp-project-id>.iam.gserviceaccount.com"
   ```

## Invocation

```bash
python3 examples/multikueue_job/launch.py \
  --queue=multikueue-cpu-queue \
  --num_work_units=2 \
  --duration=20
```
