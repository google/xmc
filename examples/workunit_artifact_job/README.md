# WorkUnit Artifact Example

This example demonstrates how to interact with the XManager on Cloud (XMC) **Experiment State Service (ESS)** to manage WorkUnit artifacts across all lifecycle phases end-to-end:

1. Connect to the Experiment State Service via the XMC CLI (`xm_cloud.create_experiment`).
2. Create an Experiment (`experiments/{xid}`).
3. Create a WorkUnit (`experiments/{xid}/workUnits/{wid}`).
4. Construct and attach artifacts across all three lifecycle phases (`INPUT`, `EXECUTION`, `OUTPUT`) in deterministic display priority order (`priority=1..3`):
   - **`build_input_artifact`** (`CodeSourceArtifact` in the `INPUT` lifecycle phase, `priority=1`)
   - **`build_executable_artifact`** (`TextArtifact` in the `EXECUTION` lifecycle phase, `priority=2`)
   - **`build_output_artifact`** (`UrlArtifact` in the `OUTPUT` lifecycle phase, `priority=3`)
5. Read back and verify every attached artifact via `get_artifact` and `list_artifacts`, asserting retrieval by parent WorkUnit resource name (`artifact.parent == work_unit.name`) and chronological timestamp ordering (`WorkUnit.create_time <= Artifact.create_time`).

## Prerequisites

1. Create a Python virtual environment, install dependencies, and compile local XMC protos:

   ```bash
   python3 -m venv .venv
   source .venv/bin/activate
   pip install -r requirements.txt
   python3 ../../deploy/cli/install_xmanager.py --venv .venv
   ```

2. **Environment Variables**:
   Verify environment variables in your active shell or `.env`:

   ```bash
   export PROJECT_ID="<your-gcp-project-id>"
   export XMC_DOMAIN="<your-xmc-domain>"
   ```

3. **Authentication**: Run `xmanager login`. You own the experiments you launch. See [Authentication and Experiment Access](../../docs/authentication.md) for CI service accounts and sharing.

## Running the Example

Run [launch.py](launch.py):

```bash
python3 launch.py
```

Optional CLI flags:

- `--experiment_title`: Title of the experiment in XMC UI (default: `WorkUnit Artifact Lifecycle Example`).
- `--queue`: Target scheduling queue name (default: `QUEUE_NAME` env var or `cpu-queue`).

## Expected Output

The script creates the experiment and work unit, attaches the `INPUT`, `EXECUTION`, and `OUTPUT` artifacts via `build_input_artifact`, `build_executable_artifact`, and `build_output_artifact`, and verifies all three via both `get_artifact` and `list_artifacts` (including parent WorkUnit linkage and `WorkUnit.create_time <= Artifact.create_time`):

```text
🚀 Connecting to Experiment State Service (UI: https://<your-xmc-domain>)
✅ Created Experiment: experiments/<xid> (XID: <xid>)
✅ Created WorkUnit: experiments/<xid>/workUnits/<wid>
📎 Attached artifact: artifacts/<aid1> (phase=INPUT, priority=1, payload=code_source) to experiments/<xid>/workUnits/<wid>
📎 Attached artifact: artifacts/<aid2> (phase=EXECUTION, priority=2, payload=text) to experiments/<xid>/workUnits/<wid>
📎 Attached artifact: artifacts/<aid3> (phase=OUTPUT, priority=3, payload=url) to experiments/<xid>/workUnits/<wid>

🔍 Reading back and verifying artifacts...
  [✓] Verified via get_artifact: name=artifacts/<aid1>, parent=experiments/<xid>/workUnits/<wid>, wu_create_time<=art_create_time, phase=INPUT, priority=1, payload=code_source, notes='Input code source snapshot consumed by the WorkUnit'
  [✓] Verified via get_artifact: name=artifacts/<aid2>, parent=experiments/<xid>/workUnits/<wid>, wu_create_time<=art_create_time, phase=EXECUTION, priority=2, payload=text, notes='Execution logs captured from primary worker container'
  [✓] Verified via get_artifact: name=artifacts/<aid3>, parent=experiments/<xid>/workUnits/<wid>, wu_create_time<=art_create_time, phase=OUTPUT, priority=3, payload=url, notes='External training metrics dashboard URL'
  [✓] Verified via list_artifacts: found all 3 expected artifacts under parent experiments/<xid>/workUnits/<wid> with valid timestamp ordering

==================================================
  🎉 Experiment and WorkUnit Launch Completed!    
  Work Unit : https://<your-xmc-domain>/experiments/<xid>/workUnits/<wid>
  Dashboard : https://<your-xmc-domain>/experiments/<xid>
==================================================
```
