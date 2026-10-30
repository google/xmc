# ManagedXprof WorkUnit Artifact Example

This directory contains an XManager Cloud launch example demonstrating **work-unit-level profiling** with `ManagedXprof` artifacts. It supports both **programmatic profiling** (capturing a JAX profile using native framework tooling directly to Cloud Storage) and **on-demand profiling** (via the live JAX profiler server).

The worker (`worker.py`) reads the per-work-unit GCS destination from the `XMC_MANAGED_XPROF_GCS_OBJECT_FOLDER` environment variable — which the XMC Actuator injects into every container — and calls `jax.profiler.start_trace()` / `jax.profiler.stop_trace()` to write the programmatic profile directly to that GCS folder. It also runs a live JAX profiler server on port `9999` for on-demand profiling.

`launch.py` creates an `EXECUTION`-phase work-unit-level `ManagedXprof` artifact (`experiments/{xid}/workUnits/{wid}`) immediately before submitting each WorkUnit with an explicit `work_unit_id`. Pre-registering the artifact ensures both the Actuator and the Diagon Reconciler find `display_name` and `run_group` on the very first schedule and reconcile pass, with zero startup race. Diagon then discovers and loads profiles from the GCS folder using the ManagedXprof labels.

## Prerequisites

1. Set up your `.env` configuration file in the repository root as described in the CLI installation instructions (`PROJECT_ID`, `QUEUE_NAME`, `XMC_DOMAIN`).
2. Managed xprof must be configured for the cluster so the Actuator injects the `XMC_MANAGED_XPROF_GCS_OBJECT_FOLDER` env var, and the workload's service account must have write access to the managed-xprof GCS bucket.
3. Create a Python virtual environment, install dependencies, and compile local XMC protos:
   ```bash
   python3 ../../deploy/cli/install_xmanager.py --venv .venv --requirements requirements.txt
   source .venv/bin/activate
   ```
4. Build and push the docker image for the worker script (optional if using pre-built image):
   ```bash
   ./build_and_push.sh
   ```
5. **Authentication**: With `.venv` activated, run `xmanager login`. You own the experiments you launch. See [Authentication and Experiment Access](../../docs/authentication.md) for CI service accounts and sharing.

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
1. Create an experiment via `ExperimentStateService.CreateExperiment`.
2. For each work unit: pre-create an `EXECUTION`-phase `ManagedXprof` artifact under `experiments/{xid}/workUnits/{wid}`, then submit `CreateWorkUnit` with explicit `work_unit_id={wid}` running `worker.py`.
3. Update the experiment launch state to `COMPLETED` and print the URL to your experiment dashboard.

## How the Worker Profiles

`worker.py`:
1. Resolves the trace destination from `XMC_MANAGED_XPROF_GCS_OBJECT_FOLDER` (automatically injected by the XMC Actuator when Managed XProf is configured), falling back to `/tmp/xprof` for local standalone testing.
2. Warms up a JAX matmul so the trace captures steady-state execution.
3. Captures the first `--programmatic_steps` steps inside `jax.profiler.start_trace(profile_dir)` / `jax.profiler.stop_trace()`, writing the programmatic profile directly to the GCS folder.
4. Starts a JAX profiler server on port `9999` after the programmatic trace finishes so on-demand profile captures cannot collide with an active programmatic trace.
5. Continues running the remaining steps up to `--total_steps` with a 1-second delay per step so you can trigger on-demand profiles during steady-state execution.
