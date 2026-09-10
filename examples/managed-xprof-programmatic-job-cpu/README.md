# Managed XProf Programmatic Profiling on CPU

This example demonstrates how to capture JAX profile traces **programmatically** on CPU workloads using Google Cloud Managed ML Diagnostics (Diagon) and XManager Cloud (XMC).

## Architecture

1. **Launcher (`launch.py`)**:
   - Creates an experiment and submits a WorkUnit with a CPU workload.
   - Attaches a `ManagedXprof` artifact at the `EXECUTION` lifecycle phase.
2. **Actuator**:
   - Injects the `XMC_MANAGED_XPROF_GCS_OBJECT_FOLDER` environment variable (e.g. `gs://<bucket>/xmc-<xid>-<wid>-<timestamp>`) into the workload pod.
3. **Worker (`worker.py`)**:
   - Reads the GCS destination from `XMC_MANAGED_XPROF_GCS_OBJECT_FOLDER`.
   - Calls `jax.profiler.start_trace(profile_dir)` and `jax.profiler.stop_trace()` to write the profile directly to GCS.
4. **Reconciler & Diagon**:
   - The XMC Reconciler reads the attached `ManagedXprof` artifact, creates an MLRun tracking record in Diagon, and points it to the GCS folder.
   - Diagon indexes the programmatic trace files (`*.xplane.pb`) and displays the profiling session in the Cloud Console.

## Prerequisites

1. Set up your `.env` configuration file in the repository root (or export environment variables):
   ```bash
   export PROJECT_ID="<your-gcp-project-id>"
   export XMC_DOMAIN="<your-xmc-domain>"
   export QUEUE_NAME="cpu-queue"
   ```
2. Build and push the container image:
   ```bash
   ./build_and_push.sh
   ```

## Running the Example

Run the launcher:

```bash
python3 launch.py
```

### Optional Flags

- `--display_name`: Name for the ManagedXprof artifact shown in the Diagon UI (default: `managed-xprof-cpu-demo`).
- `--run_group`: Grouping tag for profiling sessions (default: `managed-xprof-cpu`).
- `--num_work_units`: Number of work units to launch (default: `1`).
- `--image_path`: Custom container image URL (defaults to `us-docker.pkg.dev/${PROJECT_ID}/xmc/managed-xprof-programmatic-job-cpu:latest`).
- `--queue_name`: Scheduling queue name (default: `cpu-queue`).

## Verification

1. Open the **Experiment Dashboard URL** printed by `launch.py`:
   `https://<XMC_DOMAIN>/experiments/<XID>`
2. Click into **Work Unit #1** → **Artifacts**.
3. Click the linked **Diagon MLRun URL** to view the profiling session in Google Cloud Console:
   `https://console.cloud.google.com/kubernetes/aiml/run/us-central1/<MLRUN_ID>?project=<PROJECT_ID>&tab=profiles`
4. Inspect the generated trace files in Cloud Storage:
   ```bash
   gcloud storage ls "gs://<managed-xprof-bucket>/xmc-<XID>-*/**"
   ```
