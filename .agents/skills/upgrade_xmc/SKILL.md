---
name: Upgrade XMC Environment
description: Upgrades an existing XManager on Cloud (XMC) environment on GKE to the latest current branch state using local .env configuration.
---

# Upgrade XMC Environment Workflow

This skill guides the agent through upgrading an existing deployed XManager on Cloud (XMC) environment on Google Kubernetes Engine (GKE) so that it runs the latest code from the current local repository branch (`HEAD`), leveraging the local `.env` configuration file in the repository root.

```mermaid
graph TD
    Start[Start Upgrade] --> CheckEnv["Check .env configuration"]
    CheckEnv --> CheckRun["Run ./tools/upgrade_dev_env.sh --only check"]
    CheckRun --> MigrateCheck["Run ./tools/upgrade_dev_env.sh --only migrate"]
    MigrateCheck -->|No pending migrations| AutoRun["Run ./tools/upgrade_dev_env.sh --start-from images"]
    MigrateCheck -->|Pending migrations (Exit 10)| PromptUser["Prompt User to run migrations"]
    PromptUser -->|User declines| Abort["Abort Upgrade"]
    PromptUser -->|User approves| AutoRunMigrate["Run ./tools/upgrade_dev_env.sh --start-from migrate --run-migrations"]
    AutoRunMigrate --> Verify[Verification & Validation]
    AutoRun --> Verify
    Verify -->|All checks pass| L["Report completed upgrade & healthy rollout"]
```

---

## 1. Pre-flight CLI & Config Checks

### CLI Tool Checks
Before running any upgrade step, verify that the required CLI tools are installed and operational:

| CLI Tool | Verification Command | Expected Successful Outcome |
| :--- | :--- | :--- |
| **`gcloud`** | `gcloud --version` | Prints Google Cloud SDK version info |
| **`kubectl`** | `kubectl version --client` | Prints Kubernetes client version |
| **`helm`** | `helm version` | Prints Helm client version |
| **`bazel`** | `bazel --version` | Prints Bazel version |

> [!CAUTION]
> **Check Action**: If any CLI check fails (command not found or returns error):
> 1. **Stop execution immediately**.
> 2. Alert the user about the missing/broken CLI dependency.
> 3. Provide installation instructions and do not proceed.

### Configuration File Verification (.env)
Check if the `.env` file exists in the repository root.

If the `.env` file **does not exist**:
1. **Gather Context**:
   - Run `gcloud config list` to find the active GCP project ID.
   - Run `gcloud container clusters list` to find available GKE clusters.
   - Run `kubectl config current-context` to find the current active GKE cluster context.
   - Check for deployment configuration YAML files in `deploy/ctk_v2/` (e.g., `deploy/ctk_v2/xmanager-cloud-deployment.yaml` or other custom deployment configs) and read `deployment_name` and `environment` from them.
   - Run `gcloud iam service-accounts list` to find the service accounts in the project.
2. **Propose Proposed Values**:
   - Draft a proposed `.env` configuration file using the discovered project ID, GKE cluster name, region, namespace, environment name, deployment name (`XMC_DEPLOYMENT_NAME`), and OAuth client SA details.
3. **Prompt User**:
   - Stop execution, explicitly inform the user that the `.env` configuration file is missing, and present the proposed configuration content.
   - Ask the user if they would like you to automatically create the `.env` file with these proposed values. Do not proceed until you have received confirmation.

---

## 2. Automated Dev Upgrade (`upgrade_dev_env.sh`)

Use the automated upgrade tool (`./tools/upgrade_dev_env.sh`) to execute the environment upgrade.

### Step 1: Run Configuration & Authentication Checks
Validate configuration in `.env`, authenticate with Google Cloud and GKE, and verify that the target Kubernetes namespace exists:
```bash
./tools/upgrade_dev_env.sh --env .env --only check
```

### Step 2: Check for Pending Database Migrations
Verify if there are any pending Spanner database migrations:
```bash
./tools/upgrade_dev_env.sh --env .env --only migrate
```

> [!IMPORTANT]
> **Migration Decision**:
> *   **If the command succeeds (Exit 0)**: No pending migrations are found. Proceed to Step 3.
> *   **If the command exits with code 10**: Pending migrations were detected.
>     1.  **Stop execution** and communicate the pending migrations status to the user.
>     2.  **Ask the user** if they want to run the pending migrations.
>     3.  **If user says YES**: Proceed to Step 3 and append `--run-migrations` flag to the command.
>     4.  **If user says NO**: Abort the upgrade workflow and report to the user that upgrade was cancelled due to declined migrations.

### Step 3: Execute Upgrade
If no migrations were pending, or if the user approved running migrations:

*   **Case A: No pending migrations**:
    Run the upgrade starting from the `images` step (skipping migration step):
    ```bash
    ./tools/upgrade_dev_env.sh --env .env --start-from images
    ```
*   **Case B: User approved migrations**:
    Run the upgrade starting from `migrate` step and authorize migration execution:
    ```bash
    ./tools/upgrade_dev_env.sh --env .env --start-from migrate --run-migrations
    ```

### Resuming or Running Specific Steps (Advanced / Troubleshooting)
If the upgrade fails at a specific phase, or if you need to rerun a specific part of the workflow:

*   **Resume from a specific step**: To resume from a failed phase (`check`, `migrate`, `images`, `app`, `verify`):
    ```bash
    ./tools/upgrade_dev_env.sh --env .env --start-from <failed_step>
    ```
*   **Execute a single step**: To execute **only** one specific step:
    ```bash
    ./tools/upgrade_dev_env.sh --env .env --only <step>
    ```
    *Valid steps: `check`, `migrate`, `images`, `app`, `verify`*



## 4. Verification and Validation

Run the verification script from the repository root to verify that all upgraded pods are running and endpoints respond correctly:

```bash
ENVIRONMENT="${XMANAGER_ENV:-dev}" REGION="${GCP_REGION:-us-central1}" PROJECT_ID="$PROJECT_ID" ./deploy/ctk_v2/verify_xmc.sh
```

> [!TIP]
> Once the verification script reports that all pods are healthy and services are ready, confirm to the user that the XMC environment upgrade is complete and running the current branch state.

