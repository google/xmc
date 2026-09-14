# ManagedXprof WorkUnit Artifact Example

This directory contains a complete XManager Cloud launch example demonstrating
how to attach a `ManagedXprof` artifact to a `WorkUnit` using the
`CreateArtifact` RPC.

The Diagon reconciler reads this **work-unit-level** artifact directly from
storage to create the corresponding **MLRun** tracking record, so no
experiment-level artifact is required. The artifact is attached at the
`EXECUTION` lifecycle phase, because the ManagedXprof artifact is produced
during work unit execution and only `EXECUTION`/`OUTPUT` artifacts are enriched
with the resolved MLRun URL on the read path.

The worker (`worker.py`) is a small CPU JAX workload: it starts a JAX profiler
server and repeatedly runs matrix multiplications for `SLEEP_SECONDS` seconds so
the JobSet stays alive long enough for the reconciler to create the MLRun.

## Prerequisites

1. Set up your `.env` configuration file in the repository root as described in
   the CLI installation instructions (`PROJECT_ID`, `QUEUE_NAME`, `XMC_DOMAIN`).
2. Ensure you have the `xmc` CLI python virtual environment active:
   ```bash
   source ../../deploy/cli/venv/bin/activate
   ```
3. Build and push the docker image for the worker script:
   ```bash
   ./build_and_push.sh
   ```

## Running the Example

Run the launch script:

```bash
python launch.py
```

Optional flags:

- `--display_name`: UI display name for the ManagedXprof artifact (shown in the XProf UI). If empty, XMC defaults it to `<experiment_title>_<work_unit_id>`.
- `--run_group`: Run group (tag) for the ManagedXprof profiling session. If empty, XMC defaults it to `<experiment_title>`.
- `--num_work_units`: Number of work units to launch (default `2`), each with its
  own ManagedXprof artifact.
- `--sleep_seconds`: How long the workload runs so the JobSet stays alive (default `300`).

The script will:
1. Create an experiment.
2. For each work unit: create the WorkUnit (running `worker.py`), then attach an
   `EXECUTION`-phase `ManagedXprof` artifact to it.
3. Print the URL to your experiment dashboard.
