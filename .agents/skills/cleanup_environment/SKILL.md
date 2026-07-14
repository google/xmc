---
name: Cleanup XMC Environment
description: Guides the agent through tearing down an XMC environment deployed via Cluster Toolkit (gcluster) safely using the cleanup tool.
---

# Cleanup XMC Environment Workflow

This skill guides you through destroying an XManager on Cloud (XMC) environment that was deployed using the Cluster Toolkit v2, using the automated cleanup tool.

> [!WARNING]
> **DESTRUCTIVE WORKFLOW**: This cleanup workflow is intended for developer environments. It will **permanently delete** all deployed cloud resources, including GKE clusters, VPC networks, and **all Cloud Spanner databases and backups**. Data recovery is not possible.

## 1. Prerequisites & Context Check

Before starting the teardown, you must verify the environment and get user confirmation.

1.  **Identify Environment**: Identify the deployment name (e.g., `my-dev`) which corresponds to the folder under `./ctk-out/`.
    > [!IMPORTANT]
    > **Strict Path Lock**: You must **only** look for the `ctk-out/` folder in the immediate directory you are operating in. Do **not** perform a global search to locate `ctk-out/` folders in other directories.
2.  **Identify DNS Zone (Optional)**: If the environment used Cloud DNS (e.g., for automated dev setups), identify the DNS zone name. If unknown, you can proceed without it, but DNS records might leak.

## 2. Dry Run & Planning

Always perform a dry run first to generate a cleanup plan for the user.

1.  Run the cleanup tool in dry-run mode:
    ```bash
    python3 tools/cleanup_env.py --env <deployment_name> [--dns-zone <dns_zone>] --dry-run
    ```
2.  **Present Plan & Confirm**:
    *   Show the output of the dry run to the user.
    *   Use the `ask_question` tool to ask for confirmation:
        *"Are you sure you want to clean up the environment '<deployment_name>'? This will permanently delete the resources listed in the dry-run output above."*
    *   Do **not** proceed to execution without explicit user approval.

## 3. Execution

Once confirmed, execute the cleanup:

1.  Run the cleanup tool:
    ```bash
    python3 tools/cleanup_env.py --env <deployment_name> [--dns-zone <dns_zone>]
    ```
2.  **Process Output**: The tool outputs a JSON structure.
    *   **Success**: If the exit code is 0 and output contains `"status": "success"`, the cleanup is complete. Report success to the user.
    *   **Handled Error**: If the exit code is 1 and output contains `"status": "error"`, `"error_type": "handled"`, **you must follow the instructions** provided in the `"instruction"` field of the JSON.
        *   *Example*: If it asks you to retry with `--skip-xmc-app`, run the command again with that flag.
        *   *Example*: If it asks you to manually run a GKE force-delete command, run it and then retry the cleanup script with the suggested skip flags.
    *   **Unhandled Error**: If the exit code is 2 or other, report the error and traceback to the user.

## 4. Common Mitigations (Referenced by Tool Instructions)

If the tool fails and instructs you to perform manual steps or retry with flags, here are the details:

### Scenario A: Helm Timeout on `xmc_app`
If the tool instructs you to retry with `--skip-xmc-app`, run:
```bash
python3 tools/cleanup_env.py --env <deployment_name> [--dns-zone <dns_zone>] --skip-xmc-app
```

### Scenario B: GKE Cluster Stuck Deleting
If the tool instructs you to force delete GKE and retry:
1.  Run the `gcloud container clusters delete` command provided in the tool's instruction.
2.  Rerun the cleanup tool skipping GKE and app layers:
    ```bash
    python3 tools/cleanup_env.py --env <deployment_name> [--dns-zone <dns_zone>] --skip xmc_app,kubernetes_prereqs
    ```
