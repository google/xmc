---
name: Cleanup XMC Environment
description: Guides the agent through tearing down an XMC environment deployed via Cluster Toolkit (gcluster) safely.
---

# Cleanup XMC Environment Workflow

This skill guides you through destroying an XManager on Cloud (XMC) environment that was deployed using the Cluster Toolkit v2, verifying that all cloud resources are cleaned up to avoid leakages/costs.

> [!WARNING]
> **DESTRUCTIVE WORKFLOW**: This cleanup workflow is intended for developer environments. It will **permanently delete** all deployed cloud resources, including GKE clusters, VPC networks, and **all Cloud Spanner databases and backups**. Data recovery is not possible.

## 1. Prerequisites & Context Check

Before starting the teardown, verify you are operating on the correct environment:

1.  **Context Check & Spanner Warning**: 
    *   **Always explicitly confirm** with the user before starting. Use the `ask_question` tool to ask the user to confirm: *"Are you sure you want to clean up the environment '<environment_name>'? **This will permanently delete the GKE cluster and the Cloud Spanner instance, including all databases and all backups.**"*
2.  **Formulate & Present a Cleanup Plan**:
    *   Check for the deployment directory in the current working directory under `./ctk-out/` (e.g., `./ctk-out/my-dev`).
        > [!IMPORTANT]
        > **Strict Path Lock**: You must **only** look for the `ctk-out/` folder in the immediate directory you are operating in. Do **not** perform a global search (e.g., using `find_by_name` across the entire home directory) to locate `ctk-out/` folders in other project directories or worktrees.
    *   If no `./ctk-out/` directory exists in your immediate directory, note that no active cloud environment was deployed from this folder and stop execution.
    *   Present a clear plan to the user listing exactly what will be deleted:
        *   **Cloud Spanner**: The instance `xmc-<environment>`, all databases, and **all active backups** (backups must be deleted first to allow instance teardown).
        *   **GKE Cluster**: The GKE cluster and all running applications.
        *   **VPC Network**: VPC network, subnetworks, routers, and static IPs.
        *   **Local Config / Deployment Directory** (e.g., `ctk-out/<deployment_name>` and Kubeconfig contexts)
    *   Ask for explicit user approval on this plan using the `ask_question` tool before executing any destructive commands.
3.  **Verify Cluster Connection** (if cloud cleanup is needed): Run `kubectl get nodes` to ensure you are targeting the correct active cluster.
4.  **Identify GCP Project** (if cloud cleanup is needed): Open the compiled configuration file at `ctk-out/<deployment_name>/primary/terraform.tfvars` to identify the target `project_id`.
5.  **Check Deletion Protection** (if cloud cleanup is needed):
    *   Check if `deletion_protection = true` is configured in `ctk-out/<deployment_name>/primary/terraform.tfvars` (or any other `*.tfvars` file in the deployment directory).
    *   If `deletion_protection` is set to `true`:
        *   **Stop execution and warn the user**: *"Deletion protection is enabled in this deployment's Terraform state. The standard destroy command will fail. You must either set `deletion_protection: false` in your configuration, run gcluster compile/deploy to apply the change, or manually clean up the protected resources after standard destroy fails."*
        *   Ask the user how they wish to proceed. Do not run the destroy command while `deletion_protection` is `true` without explicit user confirmation.

---

## 2. Teardown Steps

Follow these steps in sequence to perform a clean teardown.

### Step 1: Delete Spanner Backups (Mandatory)

Cloud Spanner instances cannot be deleted if they contain active backups. You must delete them before running the destroy command to prevent Terraform failures.

1.  Identify the Spanner instance name (usually `xmc-<environment>`).
2.  List backups:
    ```bash
    gcloud spanner backups list --instance=xmc-<environment> --project=<project_id>
    ```
3.  Delete each backup found:
    ```bash
    gcloud spanner backups delete <backup_id> --instance=xmc-<environment> --project=<project_id> --quiet
    ```

### Step 2: Run Standard Destroy

Attempt to run the standard Toolkit destroy command:

```bash
./cluster-toolkit/gcluster destroy ctk-out/<deployment_name> --auto-approve
```

Wait until the command completes successfully. If it succeeds, proceed to **Step 3 (Delete Cloud DNS Records)**. If it fails, follow the mitigations in **Section 3 (Mitigations for Teardown Failures)**.

### Step 3: Delete Cloud DNS Records (Mandatory for Automated Setup)

The automated deployment script (`deploy_dev_env.py`) creates Cloud DNS A records using `gcloud dns` CLI transactions. These are not managed by Terraform. To prevent resource leakage, you must delete them manually after destroying the cluster.

1.  **Identify DNS Configuration**:
    *   Identify the GCP project ID hosting the DNS zone and the Cloud DNS managed zone name.
    *   If they are not found in the deployment context or config, **you MUST stop and ask the user** to provide:
        *   **DNS Project ID** (the GCP project hosting the DNS zone).
        *   **DNS Managed Zone Name** (the name of the Cloud DNS managed zone).
2.  **Inspect active records** to find the exact ones to delete:
    ```bash
    gcloud dns record-sets list --zone=<dns_zone_name> --project=<dns_project_id>
    ```
    *(Note: If no DNS records matching the environment are found in the list, skip the delete commands below and inform the user that the DNS records were not found.)*
3.  **Delete the records** found that are related to the environment we are cleaning up:
    ```bash
    gcloud dns record-sets delete <environment>.<domain_name>. --type=A --zone=<dns_zone_name> --project=<dns_project_id> --quiet
    gcloud dns record-sets delete api.<environment>.<domain_name>. --type=A --zone=<dns_zone_name> --project=<dns_project_id> --quiet
    gcloud dns record-sets delete grpc.<environment>.<domain_name>. --type=A --zone=<dns_zone_name> --project=<dns_project_id> --quiet
    ```

### Step 4: Local Configurations & Files Cleanup

1.  **Kubeconfig Contexts**: The local kubeconfig will still have the context entry for the deleted cluster. Clean it up:
    ```bash
    # Find the context name (which typically matches the cluster name)
    kubectl config get-contexts -o name | grep "xmc-.*<environment>"
    
    # Delete the context and cluster entry
    kubectl config delete-context <context_name>
    kubectl config delete-cluster <context_name>
    ```
2.  **Delete Deployment Folder**: Delete the local generated folder:
    ```bash
    rm -rf ctk-out/<deployment_name>
    ```

---

## 3. Mitigations for Teardown Failures

If `gcluster destroy` gets stuck or fails, use the following workarounds depending on the failure reason:

### Scenario A: Helm Timeout / Connection issues in `xmc_app`
If the destroy gets stuck on the `xmc_app` group (often due to Helm release deletion timeouts because GKE nodes are scaling down or unreachable), retry skipping `xmc_app`:
```bash
./cluster-toolkit/gcluster destroy ctk-out/<deployment_name> --auto-approve --skip xmc_app
```

### Scenario B: GKE Node Pool stuck in `STOPPING` state
If the destroy fails because GKE node pools are stuck deleting (which prevents subnetwork deletion):
1.  **Force delete the GKE cluster** manually:
    ```bash
    gcloud container clusters delete xmc-<deployment_name>-<environment> --location=<location> --project=<project_id> --quiet
    ```
2.  **Retry destroy** skipping both `xmc_app` and `kubernetes_prereqs` (since the cluster is already gone):
    ```bash
    ./cluster-toolkit/gcluster destroy ctk-out/<deployment_name> --auto-approve --skip xmc_app,kubernetes_prereqs
    ```
