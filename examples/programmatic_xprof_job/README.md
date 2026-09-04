# Programmatic Xprof Example

This directory contains a complete XManager Cloud launch example demonstrating
**programmatic profiling**. The workload captures a JAX profile using native
framework tooling only.

The worker (`worker.py`) reads the per-work-unit GCS destination from the
`XMC_MANAGED_XPROF_GCS_OBJECT_FOLDER` environment variable — which the XMC
Actuator injects into every container — and calls `jax.profiler.start_trace()` /
`jax.profiler.stop_trace()` to write the profile directly to that GCS folder.

As with the [`managed_xprof_artifact_job`](../managed_xprof_artifact_job) example,
`launch.py` attaches a work-unit-level `ManagedXprof` artifact (at the `EXECUTION`
lifecycle phase) to each work unit. The Diagon reconciler reads that artifact
from storage, derives the GCS object folder, and creates the corresponding
**MLRun** tracking record. Diagon then discovers and loads the programmatic
profile from the GCS folder using the ManagedXprof labels.

The difference from `managed_xprof_artifact_job` is the worker: that example
runs a `jax.profiler.start_server` (on-demand) profiler; this example captures
the profile **programmatically** and writes it to
`XMC_MANAGED_XPROF_GCS_OBJECT_FOLDER`.

## Prerequisites

1. Set up your `.env` configuration file in the repository root as described in
   the CLI installation instructions (`PROJECT_ID`, `QUEUE_NAME`, `XMC_DOMAIN`).
2. Managed xprof must be configured for the cluster so the Actuator injects the
   `XMC_MANAGED_XPROF_GCS_OBJECT_FOLDER` env var, and the workload's service
   account must have write access to the managed-xprof GCS bucket.
3. Ensure you have the `xmc` CLI python virtual environment active:
   ```bash
   source ../../deploy/cli/venv/bin/activate
   ```
4. Build and push the docker image for the worker script:
   ```bash
   ./build_and_push.sh
   ```

## Running the Example

Run the launch script:

```bash
python launch.py
```

Optional flags:

- `--num_work_units`: Number of work units to launch (default `2`), each with its
  own ManagedXprof artifact.
- `--display_name`: Display name for the ManagedXprof artifact (XProf UI title).
- `--run_group`: Run group (tag) for the profiling session.

The script will:
1. Create an experiment.
2. For each work unit: create the WorkUnit (running `worker.py`), then attach an
   `EXECUTION`-phase `ManagedXprof` artifact to it.
3. Print the URL to your experiment dashboard.

## How the worker profiles

`worker.py`:
1. Resolves the destination from `XMC_MANAGED_XPROF_GCS_OBJECT_FOLDER`, raising
   an error if it is unset (the workload must run in a managed-xprof-configured
   cluster where the XMC Actuator injects it).
2. Warms up a JAX matmul so the trace captures steady-state execution.
3. Captures the first `--programmatic_steps` steps (default 50) inside
   `jax.profiler.start_trace(profile_dir)` / `jax.profiler.stop_trace()`,
   writing the profile directly to the GCS folder.
4. Then starts a JAX profiler server (port `9999`) and keeps running matmuls for
   `IDLE_SECONDS` (default 30 minutes), so the following steps are available for
   on-demand profile captures and the JobSet stays alive long enough for the
   reconciler to create the MLRun before the container exits.
