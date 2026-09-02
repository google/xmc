---
name: XMC Upgrade Environment
description: Upgrades an existing XManager on Cloud (XMC) environment on GKE to the latest current branch state using the deployment configuration YAML.
---

# Upgrade XMC Environment Workflow

This skill guides the agent through upgrading an existing deployed XManager on Cloud (XMC) environment on Google Kubernetes Engine (GKE) so that it runs the latest code from the current local repository branch (`HEAD`), leveraging the deployment configuration YAML file.

> [!IMPORTANT]
> All execution steps in this skill MUST strictly adhere to the policy rules defined in [Skills & Automation Guidelines](../../rules/skills_and_automation.md) and [Core Engineering Standards](../../rules/core_engineering.md).

> [!CAUTION]
> **Exclusive Execution Tool Policy**:
> Environment upgrades MUST be performed EXCLUSIVELY using the unified development environment CLI `python3 tools/dev_env/dev_env.py gke-upgrade` (or legacy `python3 ./tools/dev_env/upgrade_dev_env.py`).
> Agents MUST NOT run manual `helm upgrade`, `kubectl apply`, `kubectl set image`, or `gcluster deploy` commands for upgrades. If the user does not specify a specific upgrade step, run the entire upgrade script (or run step-by-step using `--only <step>` or `--start-from <step>` depending on the prompt). If a step fails or is interrupted, troubleshoot script inputs or resume using `python3 tools/dev_env/dev_env.py gke-upgrade --start-from <step>` or `--only <step>`.

> [!CAUTION]
> **Authentication Integrity Policy**: Under no circumstances should `gke-upgrade` be invoked with `auth.mockAuth="true"` or `auth.disableAuthentication="true"` unless the user explicitly requests and confirms mock authentication. Always maintain the production OAuth/IAP authentication pipeline configured in `deploy/ctk_v2/xmanager-cloud-deployment.yaml`.

```mermaid
graph TD
    Start[Start Upgrade] --> CheckEnv["Check configuration YAML"]
    CheckEnv --> CheckRun["Run python3 tools/dev_env/dev_env.py gke-upgrade --only check"]
    CheckRun --> MigrateCheck["Run python3 tools/dev_env/dev_env.py gke-upgrade --only migrate"]
    MigrateCheck -->|No pending migrations| AutoRun["Run python3 tools/dev_env/dev_env.py gke-upgrade --start-from images"]
    MigrateCheck -->|"Pending migrations (Exit 10)"| PromptUser["Prompt User to run migrations"]
    PromptUser -->|User declines| Abort["Abort Upgrade"]
    PromptUser -->|User approves| AutoRunMigrate["Run python3 tools/dev_env/dev_env.py gke-upgrade --start-from migrate --run-migrations"]
    AutoRunMigrate --> Verify["Verification & Validation"]
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

### Configuration File Verification (YAML)
Verify that the deployment configuration YAML file exists. By default, the script looks for `deploy/ctk_v2/xmanager-cloud-deployment.yaml`.

If you need to use a custom configuration file, you must pass it using the `--config` (or `-c`) flag.

You can optionally define environment variable overrides (e.g., `PROJECT_ID`, `K8S_NAMESPACE`, `XMANAGER_ENV`) in your active shell before running the script to bypass values defined in the YAML configuration. You can copy `.env.example` to `.env` and export them: `set -o allexport; source .env; set +o allexport`.

If no configuration file is found at the default path:
1. **Search for alternatives**: Check if there are any other `*-deployment.yaml` files in `deploy/ctk_v2/`.
2. **Handle missing config**:
   - If other configuration files are found, ask the user which one they want to use and run the script with `--config <path>`.
   - If no configuration files are found, **stop execution** and instruct the user to create a deployment configuration (referring to `INSTALL.md` or the `xmc_deploy` skill) before upgrading.

---

## 2. Automated Dev Upgrade (`tools/dev_env/dev_env.py gke-upgrade`)

Use the unified environment tool (`python3 tools/dev_env/dev_env.py gke-upgrade` or legacy `upgrade_dev_env.py` / `upgrade` command) to execute the environment upgrade.

### Step 1: Run Configuration & Authentication Checks
Validate configuration in YAML, authenticate with Google Cloud and GKE, and verify that the target Kubernetes namespace exists:
```bash
python3 tools/dev_env/dev_env.py gke-upgrade --only check
```

### Step 2: Check for Pending Database Migrations
Verify if there are any pending Spanner database migrations:
```bash
python3 tools/dev_env/dev_env.py gke-upgrade --only migrate
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

> [!NOTE]
> **Feature Flag Awareness**: During the `images` step (building/pushing microservice OCI images) and `restart` step (rolling restarts of Kubernetes deployments), `gke-upgrade` reads feature flags from the deployment configuration YAML (e.g. `rod_deployment_enabled: false`). If a feature is disabled (e.g., `rod_deployment_enabled: false`), `rod_data_provider` image pushing and `rod-data-provider` deployment restarts are automatically skipped.

*   **Case A: No pending migrations**:
    Run the upgrade starting from the `images` step (skipping migration step):
    ```bash
    python3 tools/dev_env/dev_env.py gke-upgrade --start-from images
    ```
*   **Case B: User approved migrations**:
    Run the upgrade starting from `migrate` step and authorize migration execution:
    ```bash
    python3 tools/dev_env/dev_env.py gke-upgrade --start-from migrate --run-migrations
    ```

> [!NOTE]
> During the `app` step, `gke-upgrade` automatically re-compiles the Cluster Toolkit blueprint (`gcluster create <blueprint> -d ... --force`) and exports outputs across prerequisite groups (`primary`, `compute`) so that all updated feature flags, environment variables, and Helm values are propagated to `ctk-out/` before applying Kubernetes workload updates.
>
> The blueprint re-compiled is the one recorded in `ctk-out/<deployment_name>/.xmc-blueprint` when the deployment was compiled, defaulting to `deploy/ctk_v2/xmanager-cloud.yaml` when there is no record. Do not pass `--blueprint` unless the user asks for a specific blueprint or the record is missing on a deployment that was not built from the default — re-compiling a deployment from the wrong blueprint plans the creation of infrastructure it is already using.

### Resuming or Running Specific Steps (Advanced / Troubleshooting)
If the upgrade fails at a specific phase, or if you need to rerun a specific part of the workflow:

*   **Resume from a specific step**: To resume from a failed phase (`check`, `migrate`, `images`, `app`, `restart`, `verify`):
    ```bash
    python3 tools/dev_env/dev_env.py gke-upgrade --start-from <failed_step>
    ```
*   **Execute a single step**: To execute **only** one specific step:
    ```bash
    python3 tools/dev_env/dev_env.py gke-upgrade --only <step>
    ```
    *Valid steps: `check`, `migrate`, `images`, `app`, `restart`, `verify`*



## 4. Verification and Validation

Ensure the Python virtual environment is activated, then run the verification script from the repository root to verify that all upgraded pods are running and endpoints respond correctly:

```bash
ENVIRONMENT="${XMANAGER_ENV:-dev}" REGION="${GCP_REGION:-us-central1}" PROJECT_ID="$PROJECT_ID" python3 ./deploy/ctk_v2/verify_xmc.py
```

> [!TIP]
> Once the verification script reports that all pods are healthy and services are ready, confirm to the user that the XMC environment upgrade is complete and running the current branch state.

