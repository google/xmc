# Chart Artifact Example Job

> [!NOTE]
> This example is currently a starting point for dashboard UI development. 
> It attaches chart artifacts and status outputs strictly for the purpose of testing the XManager UI. 
> It will continuously evolve over time to become a fully fleshed-out usage example.

This directory contains a complete XManager launch example demonstrating how to attach a `Chart` artifact to a `WorkUnit` using the `CreateArtifact` RPC. 
The dummy worker script simply prints text to standard output to simulate container activity, while `launch.py` directly handles creating the mock chart payload.

## Prerequisites

1. Set up your `.env` configuration file in the repository root as described in the CLI installation instructions.
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

The script will:
1. Create a WorkUnit running the `worker.py` script.
2. Create an Artifact containing a payload with mock Chart configurations.
3. Print the URL to your experiment dashboard where you can see the artifact.
