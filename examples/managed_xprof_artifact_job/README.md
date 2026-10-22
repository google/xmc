# ManagedXprof WorkUnit Artifact Example

This directory contains an XManager Cloud launch example demonstrating **work-unit-level profiling** with `ManagedXprof` artifacts. It supports both **programmatic profiling** (capturing a JAX profile using native framework tooling directly to Cloud Storage) and **on-demand profiling** (via the live JAX profiler server).

The worker (`worker.py`) reads the per-work-unit GCS destination from the `XMC_MANAGED_XPROF_GCS_OBJECT_FOLDER` environment variable — which the XMC Actuator injects into every container — and calls `jax.profiler.start_trace()` / `jax.profiler.stop_trace()` to write the programmatic profile directly to that GCS folder. It also runs a live JAX profiler server on port `9999` for on-demand profiling.

`launch.py` attaches a work-unit-level `ManagedXprof` artifact (at the `EXECUTION` lifecycle phase) to each work unit immediately after creation. The Diagon reconciler reads this artifact from storage, derives the GCS object folder, and creates the corresponding **MLRun** tracking record. Diagon then discovers and loads profiles from the GCS folder using the ManagedXprof labels.

## Prerequisites

1. Set up your `.env` configuration file in the repository root as described in the CLI installation instructions (`PROJECT_ID`, `QUEUE_NAME`, `XMC_DOMAIN`).
2. Managed xprof must be configured for the cluster so the Actuator injects the `XMC_MANAGED_XPROF_GCS_OBJECT_FOLDER` env var, and the workload's service account must have write access to the managed-xprof GCS bucket.
3. Create a Python virtual environment and install dependencies:
   ```bash
   python3 -m venv .venv
   source .venv/bin/activate
   pip install -r requirements.txt
   ```
4. Build and push the docker image for the worker script (optional if using pre-built image):
   ```bash
   ./build_and_push.sh
   ```

## Running the Example

Run the launch script:

```bash
python3 launch.py
```

Optional CLI flags:

- `--experiment_name`: Title of the experiment in XMC UI (default `ManagedXprof WorkUnit Artifact Example`).
- `--display_name`: UI display name for the ManagedXprof artifact (shown in the XProf UI). If empty, XMC defaults it to `<experiment_title>_<work_unit_id>`.
- `--run_group`: Run group (tag) for the ManagedXprof profiling session. If empty, XMC defaults it to `<experiment_title>`.
- `--num_work_units`: Number of work units to launch (default `1`).
- `--image_path`: Custom container image path to run.
- `--queue_name`: Scheduling queue (default `cpu-queue`).
- `--programmatic_steps`: Number of steps to profile programmatically (default `10`).
- `--matrix_size`: Matrix dimension for matmul workload (default `2000`).

The script will:
1. Create an experiment.
2. For each work unit: create the WorkUnit (running `worker.py`), then immediately attach an `EXECUTION`-phase `ManagedXprof` artifact to it.
3. Print the URL to your experiment dashboard.

## How the Worker Profiles

`worker.py`:
1. Resolves the destination from `XMC_MANAGED_XPROF_GCS_OBJECT_FOLDER`, raising an error if unset (the workload must run in a managed-xprof-configured cluster where the XMC Actuator injects it).
2. Warms up a JAX matmul so the trace captures steady-state execution.
3. Starts a JAX profiler server on port `9999`.
4. Captures the first `--programmatic_steps` steps inside `jax.profiler.start_trace(profile_dir)` / `jax.profiler.stop_trace()`, writing the profile directly to the GCS folder.
5. Continues running the remaining steps up to `--total_steps` with a 1-second delay per step. Due to an XProf limitation, triggering an on-demand profile capture needs to wait for programmatic tracing to finish here (though the 9999 xprof server can be started at the same time), so on-demand profiling can be performed during these remaining steps.
