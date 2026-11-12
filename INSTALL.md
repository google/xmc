# Installation & Deployment Guide

This guide details how to install a fresh deployment of XManager on Cloud (XMC) as well as how to upgrade existing installations to newer versions:

1. **Automated Agent Deployment**: Using the pre-configured [xmc_deploy skill](.agents/skills/xmc_deploy/SKILL.md) for fresh installations.
2. **Cluster Toolkit Blueprint v2**: The recommended manual pathway for fresh installations using automated orchestration blueprints.
3. **Terraform and Helm**: The direct, hands-on infrastructure deployment pathway.
4. **Upgrading an Existing Installation**: Step-by-step instructions for upgrading existing deployments, running incremental Cloud Spanner schema migrations, building updated container images, and rolling out application upgrades.
5. **Local KinD Development & Fast Reload**: Rapid inner-loop local development using `python3 tools/dev_env/dev_env.py up` and `dev_env.py reload [-w] [--helm]` (see [`deploy/local/README.md`](deploy/local/README.md)).

> [!IMPORTANT]
> **Working Directory**: All commands in this guide must be executed from the root of the `xmc` repository directory.

---

## Prerequisites

Before launching any deployment pathway, ensure you have the following CLI tools installed:

| CLI Tool | Verification Command | Expected Outcome |
| :--- | :--- | :--- |
| **`gcloud`** | `gcloud --version` | Prints Google Cloud SDK version info |
| **`kubectl`** | `kubectl version --client` | Prints Kubernetes client version |
| **`helm`** | `helm version` | Prints Helm client version (minimum 3.14.0) |
| **`terraform`** | `terraform --version` | Prints Terraform version (minimum 1.12.2) |
| **`gcluster`** | `./cluster-toolkit/gcluster version` | Prints gcluster tool version |

### Installing HPC Cluster Toolkit (gcluster)

If you do not have `gcluster` installed locally, you must clone and build the Google Cloud HPC Toolkit:
1. Clone the repository into a directory named `cluster-toolkit` at the root of this workspace:
   ```bash
   git clone https://github.com/GoogleCloudPlatform/hpc-toolkit.git cluster-toolkit
   ```
2. Navigate into the folder and build the tool:
   ```bash
   cd cluster-toolkit && make && cd ..
   ```
This compiles the toolkit and produces the `./cluster-toolkit/gcluster` CLI binary.

---

## Pre-Deployment Setup

Before executing any deployment pathway, follow these configuration steps to prepare your environment. The steps described in this section assume highly privileged access to a GCP project and limited constraints stemming from organization policies. Please check your organization policies regarding the resources you are allowed to create - most companies will have very specific requirements around domain registration and certificates management. 

### 1. Copy Configuration Template

Copy the blueprint configuration override template to create your deployment overrides configuration file:
```bash
cp deploy/ctk_v2/xmc-deployment-template.yaml deploy/ctk_v2/xmanager-cloud-deployment.yaml
```
You will configure your deployment variables by editing `deploy/ctk_v2/xmanager-cloud-deployment.yaml`.

### 2. Build and Push Container Images

The XMC services require OCI container images built from the codebase. You must build and push these images to Google Artifact Registry (or another registry of your choice) before deploying.

1. **Create an Artifact Registry repository** in your GCP project:
   ```bash
   gcloud artifacts repositories create xmc \
       --repository-format=docker \
       --location=<LOCATION> \
       --description="XMC Container Images"
   ```

2. **Configure Docker authentication** to access the repository:
   ```bash
   gcloud auth configure-docker <LOCATION>-docker.pkg.dev
   ```

3. **Set environment variables** specifying your target registry path, environment name suffix, and tag:
   ```bash
   export XMC_ACR_TARGET="<LOCATION>-docker.pkg.dev/<PROJECT_ID>/xmc"
   export XMC_ENV="dev"  # Match your deployment environment name
   export BUILD_TAG="<IMAGE_TAG>"
   ```

4. **Build and push the images** using the generated Bazel targets:
   ```bash
   bazel run //cmd/xid_service:xid_service_image_push
   bazel run //cmd/actuator:actuator_image_push
   bazel run //cmd/experiment_state_server:experiment_state_server_image_push
   bazel run //cmd/ui:ui_image_push
   bazel run //cmd/reconciler:reconciler_image_push
   bazel run //cmd/log_monitor:log_monitor_image_push
   bazel run //cmd/health_service:health_service_image_push
   bazel run //cmd/dashboard_service:dashboard_service_image_push
   bazel run //cmd/rod_data_provider:rod_data_provider_image_push
   bazel run //cmd/ingestion_gateway:ingestion_gateway_image_push
   bazel run //cmd/xmc_bridge:xmc_bridge_image_push
   ```

5. **Update image variables** in your deployment configuration file (e.g. `xmanager-cloud-deployment.yaml`, copy of [xmc-deployment-template.yaml](deploy/ctk_v2/xmc-deployment-template.yaml)):
   ```yaml
   image_registry: "<LOCATION>-docker.pkg.dev/<PROJECT_ID>/xmc"
   image_tag: "<IMAGE_TAG>"
   ```

### 3. DNS and Domain Name Configuration

XMC relies on GKE Gateway API to expose HTTP, HTTPS, and gRPC endpoints externally on a single global external static IP address.

#### Why a domain is required
- **Gateway Routing**: The Gateway routes incoming traffic to the appropriate service (e.g. UI, ESS backend, or gRPC) using HTTP host headers.
- **TLS/SSL Provisioning**: Google Trust Services (GTS) or Let's Encrypt requires a valid, publicly resolvable DNS domain to verify domain ownership (via ACME HTTP/DNS challenges) before issuing certificates.

#### Using GKE Gateway Technical Domains in Non-Production Environments
If you are deploying to a development, staging, or testing environment and do not have a dedicated corporate/personal domain name, you have two options to provision DNS records:

##### Option A: Using a Shared Custom Domain (e.g. `example.com`)
If your organization maintains a delegated DNS zone (e.g. in [Cloud DNS](https://console.cloud.google.com/net-services/dns/zones)) for development clusters:
1. Set the `domain_name` variable in your configuration to the shared technical domain (e.g. `example.com`).
2. Segment your setup using a unique `environment` suffix (e.g. `my-dev`). The blueprints automatically route the endpoints to:
   - UI: `my-dev.example.com`
   - API: `api.my-dev.example.com`
   - gRPC: `grpc.my-dev.example.com`
3. Map the Gateway's static IP (retrieved during deployment) to these domain names by adding **A records** in the DNS managed zone of your shared domain.

##### Option B: Using Google Cloud-provided Endpoints Domains (`cloud.goog`)
If you do not have any custom domain or DNS zone, you can use Google Cloud Endpoints to register free public DNS records under Google's managed `cloud.goog` domain.

The domain names will have the following structure:
- UI: `<environment>.endpoints.<project_id>.cloud.goog`
- API: `api.<environment>.endpoints.<project_id>.cloud.goog`
- gRPC: `grpc.<environment>.endpoints.<project_id>.cloud.goog`

To configure this:
1. In your configuration file (e.g. `xmanager-cloud-deployment.yaml`), set the `domain_name` variable to:
   ```yaml
   domain_name: "endpoints.<project_id>.cloud.goog"
   ```
   *(Replace `<project_id>` with your Google Cloud Project ID)*

2. After compiling and deploying the core infrastructure to retrieve the Gateway's global static IP address (Step 6 of the deployment process), you must register the three subdomains. Create three YAML configuration files containing the Endpoint configurations:

   **`ui-dns.yaml`**:
   ```yaml
   swagger: "2.0"
   info:
     description: "UI DNS for XMC"
     title: "XMC UI DNS"
     version: "1.0.0"
   host: "<environment>.endpoints.<project_id>.cloud.goog"
   x-google-endpoints:
   - name: "<environment>.endpoints.<project_id>.cloud.goog"
     target: "<gateway_external_ip>"
   paths: {}
   ```

   **`api-dns.yaml`**:
   ```yaml
   swagger: "2.0"
   info:
     description: "API DNS for XMC"
     title: "XMC API DNS"
     version: "1.0.0"
   host: "api.<environment>.endpoints.<project_id>.cloud.goog"
   x-google-endpoints:
   - name: "api.<environment>.endpoints.<project_id>.cloud.goog"
     target: "<gateway_external_ip>"
   paths: {}
   ```

   **`grpc-dns.yaml`**:
   ```yaml
   swagger: "2.0"
   info:
     description: "gRPC DNS for XMC"
     title: "XMC gRPC DNS"
     version: "1.0.0"
   host: "grpc.<environment>.endpoints.<project_id>.cloud.goog"
   x-google-endpoints:
   - name: "grpc.<environment>.endpoints.<project_id>.cloud.goog"
     target: "<gateway_external_ip>"
   paths: {}
   ```

3. Deploy these service configurations to Google Cloud Endpoints:
   ```bash
   gcloud endpoints services deploy ui-dns.yaml
   gcloud endpoints services deploy api-dns.yaml
   gcloud endpoints services deploy grpc-dns.yaml
   ```
   *Note: Creating these Endpoints automatically registers public DNS A records pointing to `<gateway_external_ip>` under the `cloud.goog` zone.*

### 4. OAuth2 Client (IAP Credentials) Setup

XMC uses an application-level [Google OAuth2 flow](https://developers.google.com/identity/protocols/oauth2). This requires Google OAuth 2.0 Web Client credentials (configured via `iap_client_id` and `iap_client_secret`).

#### Step-by-Step Creation Guide
1. Navigate to the **[APIs & Services > Credentials](https://console.cloud.google.com/apis/credentials)** console page.
2. Select your Google Cloud project.
3. Configure the **OAuth consent screen** if not done previously (choose **Internal** for restricted/private access, fill in App Name, User support email, and developer contact).
4. On the Credentials page, click **+ Create Credentials** > **OAuth client ID**.
5. Select **Web application** as the application type.
6. Name your client (e.g., `XMC Web Application`).
7. Under **Authorized redirect URIs**, add:
   - `https://api.<environment>.<domain>/api/callback`
   *(Substitute `<environment>` and `<domain>` with your configured suffix and domain, e.g. `https://api.xmc.example.com/api/callback`)*.
8. Click **Create** and copy the resulting **Client ID** and **Client Secret** into your `xmanager-cloud-deployment.yaml`:
   ```yaml
   iap_client_id: "YOUR_OAUTH_CLIENT_ID"
   iap_client_secret: "YOUR_OAUTH_CLIENT_SECRET"
   ```

For detailed guidance, refer to the [OAuth 2.0 APIs Credentials Documentation](https://support.google.com/cloud/answer/6158849).

### 5. cert-manager and Google Trust Services (GTS) Setup

To configure SSL, XMC needs Google Trust Services (GTS) External Account Binding (EAB) credentials.

#### How to Obtain GTS EAB Keys
1. Enable the Public CA API in your Google Cloud Project:
   ```bash
   gcloud services enable publicca.googleapis.com
   ```
2. Request a new External Account Key:
   ```bash
   gcloud publicca external-account-keys create
   ```
3. The command will output a `keyId` and `b64MacKey` (HMAC key). Map these to:
   - **`gts_key_id`** / **`certs.gts.keyId`**: Alphanumeric ID string (e.g. `a1b2c3d4e5f6g7h8i9j0`).
   - **`gts_hmac_key`** / **`certs.gts.hmacKey`**: Base64-encoded HMAC key secret (e.g. `A1b2C3d4E5f6G7h8I9j0K1l2==`).
   - **`certs_email`**: A valid email address registered with GTS to receive certificate expiration alerts (e.g., `admin@example.com`).

Configure these in your `xmanager-cloud-deployment.yaml`:
```yaml
certs_email: "your-email@example.com"
gts_key_id: "your-gts-key-id"
gts_hmac_key: "your-gts-hmac-key"
```

---

## Approach 1: Automated Agent Deployment (xmc_deploy)

If you are running an AI-assisted agent session, the agent can automate the entire deployment of the Cluster Toolkit v2 blueprint. 

### Skill Integration and Agent Setup

Agent skill for deploying XMC is defined within the workspace:
- **Location**: The deployment skill is located in the [xmc_deploy skill](.agents/skills/xmc_deploy/SKILL.md) directory.
- **Integration**: To equip your agent session with this skill, ensure the `.agents/` directory is present in the root of the workspace. The agent automatically scans this folder upon startup and registers the workflows. No manual import should be needed.

### How to Trigger
Instruct the agent:
> Deploy XMC using the blueprint configuration at `deploy/ctk_v2/xmanager-cloud-deployment.yaml`

### How the Agent Deploys under the Hood:
1. **Pre-flight Check**: Validates the presence of `gcloud`, `kubectl`, `helm`, `terraform`, and `gcluster`.
2. **Blueprint Compilation**:
   ```bash
   ./cluster-toolkit/gcluster create deploy/ctk_v2/xmanager-cloud.yaml -d <deployment_config_path> -o ctk-out -l WARNING -w --force
   ```
3. **Deploy APIs (`apis` group)**:
   ```bash
   ./cluster-toolkit/gcluster deploy ctk-out/<deployment_name> --only apis --auto-approve
   ```
4. **Deploy Core Infrastructure (`primary` group)**:
   ```bash
   ./cluster-toolkit/gcluster deploy ctk-out/<deployment_name> --only primary --auto-approve
   ```
5. **Extract Static IP & Pause for DNS / OAuth Settings**:
   The agent retrieves the global external static IP:
   ```bash
   gcloud compute addresses describe xmc-gateway-static-ip-<environment> --global --project=<project_id> --format='value(address)'
   ```
   > [!IMPORTANT]
   > The agent will pause here and ask you to:
   > - Configure **A records** in your authoritative DNS for the external IP (e.g. `<environment>.example.com`, `api.<environment>.example.com`, `grpc.<environment>.example.com`).
   > - Add the OAuth redirect URI in your GCP Console: `https://<environment>.example.com/api/callback`.
   >
   > Once done, reply to the agent to resume execution.
6. **Deploy Compute Cluster (`compute` group)**:
   ```bash
   ./cluster-toolkit/gcluster deploy ctk-out/<deployment_name> --only compute --auto-approve
   ```
7. **Enable GKE Gateway API standard channel**:
   Before deploying the system Helm charts, the GKE cluster must have the standard Gateway API controller addon enabled:
   ```bash
   gcloud container clusters update <cluster_name> --gateway-api=standard --region=<region> --project=<project_id>
   ```
8. **Install Kubernetes Prerequisites (`kubernetes_prereqs` group)**:
   ```bash
   ./cluster-toolkit/gcluster deploy ctk-out/<deployment_name> --only kubernetes_prereqs --auto-approve
   ```
9. **Deploy Application (`xmc_app` group)**:
   ```bash
   ./cluster-toolkit/gcluster deploy ctk-out/<deployment_name> --only xmc_app --auto-approve
   ```
10. **Post-Deployment Verification**:
    The agent runs:
    ```bash
    python3 ./deploy/ctk_v2/verify_xmc.py
    ```
    And checks that the gateway status is programmed and GTS certificate is active.

---

## Approach 2: Cluster Toolkit Blueprint v2 (Recommended)

We provide automated HPC Cluster Toolkit blueprints to compile and deploy the entire GCP and GKE stack:

1. **Step-by-step Compilation**: Follow the step-by-step instructions on compilation and group-by-group deployments in the [deploy/ctk_v2/README.md](deploy/ctk_v2/README.md).
2. **Execute Deployment**: Run `gcluster deploy` command groups sequentially, checking resource state transitions as you go.
3. **Install Optional Catalog Components (`stable-2026a` Profile)**: Every optional catalog component (`redpanda`, `clickhouse`, `ingestion-gateway`) is **disabled by default**. Create a `profile-values.yaml` that enables the ones you need (`redpanda`/`clickhouse` expect the BYO add-ons from [`deploy/addons/metrics`](deploy/addons/metrics/clickhouse/README.md) to be reachable), then install the [`stable-2026a` catalog profile](deploy/profiles/stable-2026a/README.md):
   ```yaml
   # profile-values.yaml — enable the metrics pipeline
   components:
     redpanda:
       enabled: true
     clickhouse:
       enabled: true
     ingestion-gateway:
       enabled: true
   ```
   ```bash
   helm upgrade --install --reset-then-reuse-values xmc-profile ./deploy/profiles/stable-2026a -n xmc -f profile-values.yaml --wait
   ```
   If `dashboard-service` was already running before you enabled `clickhouse` (or `bigquery`), wait until `xmc-bridge` has reconciled the enabled component (use `component.xmc.dev/bigquery` when enabling BigQuery) and then restart it once so it picks up the new data engine: `kubectl wait component.xmc.dev/clickhouse -n xmc --for=condition=Ready --timeout=120s && kubectl rollout restart deployment/dashboard-service-deployment -n xmc`.

---

## Approach 3: Terraform and Helm Directly (Hands-on)

For direct control over your infrastructure:

1. **Provision Infrastructure**: Use your organization's custom Terraform pipeline to provision the GCP resources:
   - VPC network and subnetworks.
   - GKE cluster with Workload Identity enabled.
   - Cloud Storage FUSE CSI driver add-on enabled on the GKE cluster (required for GCS volumes).
   - Cloud Spanner database instance.
   - Memorystore Redis cache.
2. **Deploy Helm Release**: Point your local kubeconfig context to the cluster and deploy the application chart:
   ```bash
   helm upgrade --install xmc ./deploy/helm -n xmc --create-namespace -f my-values.yaml
   ```
   *For details on configuring values, refer to [deploy/helm/values.yaml](deploy/helm/values.yaml).*
   `xmc-bridge` is a mandatory controller in `1.0.0+`: set `xmc_bridge_custom_image` (and `ingestion_gateway_custom_image`, which `xmc-bridge` uses as the default Ingestion Gateway image) like the other `*_custom_image` values. When `xmc_bridge_custom_image` is empty the chart derives `us-docker.pkg.dev/<projectId>/xmc/xmc-bridge-<environment>:<imageTag>`, which only matches the push commands above if `<LOCATION>` is `us` and `imageTag` equals `BUILD_TAG`.
3. **Install Optional Catalog Components (`stable-2026a` Profile)**: Every optional catalog component (`redpanda`, `clickhouse`, `ingestion-gateway`) is **disabled by default**. Create a `profile-values.yaml` that enables the ones you need (see the example in Approach 2, step 3), then install the [`stable-2026a` catalog profile](deploy/profiles/stable-2026a/README.md):
   ```bash
   helm upgrade --install --reset-then-reuse-values xmc-profile ./deploy/profiles/stable-2026a -n xmc -f profile-values.yaml --wait
   ```
   If `dashboard-service` was already running before you enabled `clickhouse` (or `bigquery`), wait until `xmc-bridge` has reconciled the enabled component (use `component.xmc.dev/bigquery` when enabling BigQuery) and then restart it once so it picks up the new data engine: `kubectl wait component.xmc.dev/clickhouse -n xmc --for=condition=Ready --timeout=120s && kubectl rollout restart deployment/dashboard-service-deployment -n xmc`.

---

## Upgrading an Existing Installation

When updating an existing XMC installation to a newer version or release branch, infrastructure resources (such as VPC networks, GKE clusters, Spanner instances, and OAuth configuration) remain in place. Upgrading requires applying incremental database schema migrations, building updated container images, and rolling out application workload updates to your Kubernetes cluster.

You can perform the upgrade either using the automated script (recommended for development environments) or manually step-by-step.

### Option A: Automated Upgrade (`tools/dev_env/dev_env.py gke-upgrade`)

> [!WARNING]
> The automated upgrade script is designed **only for development and testing environments**. It builds and pushes images directly from your local workspace and triggers rolling restarts which are not suitable for production deployments. For production environments, follow the manual upgrade steps (Option B) to ensure proper release management.

To upgrade an existing running XMC development environment on GKE to the latest code in your current local branch (`HEAD`), you can use the automated upgrade command:

#### Using the Upgrade Command Directly

By default, the script uses the deployment configuration file at `deploy/ctk_v2/xmanager-cloud-deployment.yaml`. You can specify a custom configuration file using the `--config` (or `-c`) flag.

> [!TIP]
> You can optionally define environment variable overrides (e.g., `PROJECT_ID`, `K8S_NAMESPACE`, `XMANAGER_ENV`) in your active shell before running the script to bypass values defined in the YAML configuration. You can copy `.env.example` to `.env` and export them:
> ```bash
> set -o allexport; source .env; set +o allexport
> ```

Run the upgrade command (legacy `./tools/upgrade_dev_env.py` is also supported):
```bash
# Run pre-flight verification checks
python3 ./tools/dev_env/dev_env.py gke-upgrade --only check

# Upgrade environment end-to-end (checks migrations, rebuilds/pushes images, deploys app workloads, and restarts services)
python3 ./tools/dev_env/dev_env.py gke-upgrade
```

#### Automated Agent Upgrade (`xmc_upgrade` skill)

If you are running an agent session, the agent will automatically use the upgrade script. Ask the agent:
> Upgrade my XMC environment

The agent will automatically authenticate, check for database migrations, compile and push updated OCI container images from your current branch, upgrade the application workloads using Cluster Toolkit (`gcluster`), initiate a rolling rollout restart across the microservices, and verify deployment rollout health.

### Option B: Manual Upgrade Steps

Follow this step-by-step workflow to upgrade an existing XMC deployment manually:

#### Step 1: Pull the Latest Codebase

Navigate to the root of your `xmc` repository workspace and update your local repository:

```bash
git checkout main
git pull origin main
```
*(If deploying a specific release tag or release branch, check out that tag/branch instead.)*

#### Step 2: Build and Push Updated Container Images

Rebuild all XMC OCI container images from the updated source code and push them to your Google Artifact Registry repository specifying your target release tag (`<IMAGE_TAG>`) so that Kubernetes workloads pull the updated service binaries. Ensure you also update your deployment configuration file (e.g., `xmanager-cloud-deployment.yaml` or `values.yaml`) with the new `<IMAGE_TAG>` before upgrading:

```bash
export XMC_ACR_TARGET="<LOCATION>-docker.pkg.dev/<PROJECT_ID>/xmc"
export XMC_ENV="<ENVIRONMENT>"  # Match your deployment environment tag
export BUILD_TAG="<IMAGE_TAG>"

bazel run //cmd/xid_service:xid_service_image_push
bazel run //cmd/actuator:actuator_image_push
bazel run //cmd/experiment_state_server:experiment_state_server_image_push
bazel run //cmd/ui:ui_image_push
bazel run //cmd/reconciler:reconciler_image_push
bazel run //cmd/log_monitor:log_monitor_image_push
bazel run //cmd/health_service:health_service_image_push
bazel run //cmd/dashboard_service:dashboard_service_image_push
bazel run //cmd/rod_data_provider:rod_data_provider_image_push
bazel run //cmd/ingestion_gateway:ingestion_gateway_image_push
bazel run //cmd/xmc_bridge:xmc_bridge_image_push
```

#### Step 3: Run Database Schema Migrations

Existing Cloud Spanner databases (`ess` and `xid`) must be upgraded to apply any new incremental DDL migrations introduced in the updated version.

XMC provides a dedicated self-contained CLI migration tool (`//internal/storage/spanner/migrations:migrations`) built on top of [golang-migrate](https://github.com/golang-migrate/migrate) that integrates with Bazel and handles Spanner proto-descriptor updates automatically for DDL execution.

##### Directory Structure

Migration files are embedded into the migration binary and are organized in the codebase under `internal/storage/spanner/migrations/` by service component:

- `internal/storage/spanner/migrations/ess/`: Experiment State Server (`ess`) database migrations.
- `internal/storage/spanner/migrations/xid/`: XID Service (`xid`) database migrations.

Each directory contains sequential pairs of `.up.sql` and `.down.sql` migration scripts prefixed with a 3-digit sequential version number (e.g., `001_baseline.up.sql`, `002_artifactinfo.up.sql`).

##### Migrations Runner CLI Flags

The migration tool is executed using `bazel run`:

```bash
bazel run //internal/storage/spanner/migrations:migrations -- [FLAGS] [ARG]
```

| Flag | Description | Mandatory |
| :--- | :--- | :--- |
| `--command` | The migration command to execute: `up`, `down`, `version`, `force`, `init`, `create`. | Yes |
| `--migrations_dir` | Name of the migration directory (`ess` or `xid`). | Yes |
| `--project` | Google Cloud Project ID. | Yes (except `create`) |
| `--instance` | Spanner Instance ID. | Yes (except `create`) |
| `--database` | Spanner Database ID. | Yes (except `create`) |

##### Inspect Current Schema Version (`version`)

Before applying migrations, inspect the current schema version and check if either database is marked as "dirty":

```bash
# Check ESS database schema version
bazel run //internal/storage/spanner/migrations:migrations -- \
  --project="<PROJECT_ID>" \
  --instance="<SPANNER_INSTANCE_ID>" \
  --database="<ESS_DATABASE_ID>" \
  --migrations_dir="ess" \
  --command="version"

# Check XID database schema version
bazel run //internal/storage/spanner/migrations:migrations -- \
  --project="<PROJECT_ID>" \
  --instance="<SPANNER_INSTANCE_ID>" \
  --database="<XID_DATABASE_ID>" \
  --migrations_dir="xid" \
  --command="version"
```

##### Apply Pending Migrations (`up`)

Run the `up` command against both the `ess` and `xid` databases to apply all pending schema migrations sequentially:

```bash
# Apply all pending migrations for ESS database
bazel run //internal/storage/spanner/migrations:migrations -- \
  --project="<PROJECT_ID>" \
  --instance="<SPANNER_INSTANCE_ID>" \
  --database="<ESS_DATABASE_ID>" \
  --migrations_dir="ess" \
  --command="up"

# Apply all pending migrations for XID database
bazel run //internal/storage/spanner/migrations:migrations -- \
  --project="<PROJECT_ID>" \
  --instance="<SPANNER_INSTANCE_ID>" \
  --database="<XID_DATABASE_ID>" \
  --migrations_dir="xid" \
  --command="up"
```

To apply only a specific number of incremental migration steps (e.g., the next 2 migrations), pass the step count as a positional argument:

```bash
bazel run //internal/storage/spanner/migrations:migrations -- \
  --project="<PROJECT_ID>" \
  --instance="<SPANNER_INSTANCE_ID>" \
  --database="<ESS_DATABASE_ID>" \
  --migrations_dir="ess" \
  --command="up" \
  2
```

##### Additional Migration Commands & Troubleshooting

- **Revert Applied Migrations (`down`)**:
  To roll back applied migrations, run `down` with the number of steps to revert (defaults to `1`). The command previews SQL changes and prompts for confirmation:
  ```bash
  bazel run //internal/storage/spanner/migrations:migrations -- \
    --project="<PROJECT_ID>" \
    --instance="<SPANNER_INSTANCE_ID>" \
    --database="<ESS_DATABASE_ID>" \
    --migrations_dir="ess" \
    --command="down" \
    1
  ```
- **Clear a Dirty State or Adopt an Existing Database (`force`)**:
  If a migration fails halfway through execution, the schema version table is marked as "dirty". Additionally, if you are upgrading an existing database that was created before the migration system was introduced, you must use this command to set the initial schema version (e.g., 1) so that subsequent migrations can be applied. After resolving the database state, force the schema version number:
  ```bash
  bazel run //internal/storage/spanner/migrations:migrations -- \
    --project="<PROJECT_ID>" \
    --instance="<SPANNER_INSTANCE_ID>" \
    --database="<ESS_DATABASE_ID>" \
    --migrations_dir="ess" \
    --command="force" \
    <TARGET_VERSION>
  ```
- **Fresh Installation Initialization (`init`)**:
  > [!NOTE]
  > The `init` command initializes the schema version in the database migration table to the latest available migration version *without* executing any SQL files. This command is intended strictly for **fresh installations** where the full schema has already been created from baseline DDL files (`internal/storage/spanner/schema/ess-schema.sql` or `internal/storage/spanner/schema/xid-schema.sql`). Do not run `init` when upgrading an existing installation.
- **Create New Migration Files (`create`)**:
  For developers adding schema changes to XMC, generate a new pair of sequential `.up.sql` and `.down.sql` files inside `internal/storage/spanner/migrations/<migrations_dir>/`:
  ```bash
  bazel run //internal/storage/spanner/migrations:migrations -- \
    --migrations_dir="ess" \
    --command="create" \
    add_user_table
  ```
- **Manual Schema Check (`gcloud`)**:
  In case a manual check is needed for the current spanner schema:
  ```bash
  gcloud spanner databases ddl describe --project=<PROJECT_ID> --instance=<SPANNER_INSTANCE_ID> <ESS_DATABASE_ID|XID_DATABASE_ID>
  ```

#### Step 4: Upgrade Application Workloads

> [!WARNING]
> **Pre-Upgrade Requirement for `< 1.0.0` Legacy Metrics Deployments**: Deployments upgrading from `< 1.0.0` (`<= 0.18.x`) with umbrella-managed `ingestion-gateway` **MUST** follow the one-time zero-downtime cutover runbook in [docs/migrations/components.md](docs/migrations/components.md) (`prepare` + `handoff` **before** running Step 4, and `finalize` after) rather than upgrading directly. XMC `1.0.0+` removes legacy non-component gateway templates and static ClickHouse env injection (`enable_metrics` / `metrics.enabled`). Direct Helm upgrades fail closed on `componentsDeployment.enabled: false` or `ingestionGatewayDeployment.enabled: true`, and `tools/dev_env` rejects `enable_components: false` and the retired `enable_metrics` / `ingestion_gateway_deployment_enabled` / `clickhouse_{host,port,database,user}` keys; a standalone `gcluster create` only *warns* about unused variables, so CTK operators must remove those legacy keys from `xmanager-cloud-deployment.yaml` / `my-values.yaml` before upgrading (retaining `clickhouse_password` / `metrics.clickhouse.{secretName,password}` for ClickHouse credentials).
> - `secrets.csi.clickhousePasswordSecretName` now defaults to `""`. CSI releases (`secrets.csi.enabled: true`) that read the ClickHouse password from Secret Manager **must** set `secrets.csi.clickhousePasswordSecretName` explicitly (previously implied `"clickhouse-password"`) before upgrading; otherwise `dashboard-service` falls back to `Secret/clickhouse-secret`.
> - Operators on `0.18.x` who already run Components (`xmc-bridge` active, where `migrate.py prepare` refuses to run) with `Component/clickhouse` disabled while relying on custom `metrics.clickhouse.{host,port,database,user}` values must move those settings into `components.clickhouse.parameters` (`host`, `nativePort`, `defaultDatabase`, `user`, `secretName`) in `xmc-profile` and set `components.clickhouse.enabled: true` (waiting until `xmc-bridge` reconciles it and restarting once if `xmc-profile` is applied after `dashboard-service` starts: `kubectl wait component.xmc.dev/clickhouse -n xmc --for=condition=Ready --timeout=120s && kubectl rollout restart deployment/dashboard-service-deployment -n xmc`); otherwise `GetChartData` returns `FailedPrecondition: data engine is not configured` after the upgrade.

Once database schemas and container images are updated, upgrade the Kubernetes workloads in your GKE cluster.

##### Option A: Using Cluster Toolkit Blueprint v2 (Recommended)

Re-compile the blueprint so `ctk-out/<deployment_name>` picks up your updated deployment configuration (for example the new `image_tag`) and chart templates (removing `.ghpc/staged` first if upgrading across a release that deleted chart templates), then re-run deployment for the application group (`xmc_app`) to roll out updated deployments and services:

```bash
rm -rf ctk-out/<deployment_name>/.ghpc/staged
./cluster-toolkit/gcluster create deploy/ctk_v2/xmanager-cloud.yaml -d <deployment_config_path> -o ctk-out -l WARNING -w --force
./cluster-toolkit/gcluster deploy ctk-out/<deployment_name> --only xmc_app --auto-approve
```

##### Option B: Using Direct Helm Upgrade

If managing workloads via direct Helm releases, point your `kubectl` context to your GKE cluster and upgrade the `xmc` Helm chart:

```bash
helm upgrade --install xmc ./deploy/helm -n xmc --create-namespace -f my-values.yaml
```

##### Upgrade the `xmc-profile` Catalog Release (When Installed)

Whenever the `xmc-profile` catalog release is installed on the cluster, upgrade it alongside the umbrella release (first updating any pinned `imageTag` or `parameters.image` tag in `profile-values.yaml` to match `<IMAGE_TAG>`; see [After Migration: Upgrading XMC](docs/migrations/components.md#after-migration-upgrading-xmc)):

```bash
helm upgrade --install --reset-then-reuse-values xmc-profile ./deploy/profiles/stable-2026a -n xmc -f profile-values.yaml --wait
```
*(Omit `-f profile-values.yaml` if your `xmc-profile` release uses no values file.)*

#### Step 5: Post-Upgrade Verification

Run the verification script from the repository root to verify that all upgraded pods are running, endpoints respond correctly, and Gateway TLS certificates remain healthy:

```bash
python3 ./deploy/ctk_v2/verify_xmc.py
```

---

## Configuration Reference


Supply these parameters in your Cluster Toolkit overrides file (`xmanager-cloud-deployment.yaml`) or your Helm overrides (`values.yaml`):

| Parameter Name | Required? | Default Value | Used By / Description |
|---|---|---|---|
| `project_id` | **Yes** | - | Target Google Cloud Project ID |
| `deployment_name` | **Yes** | - | Unique prefix for resource naming (e.g. `xmc-v2-run`) |
| `region` | **Yes** | - | Google Cloud region for resource provisioning (e.g. `us-west1`) |
| `domain_name` | **Yes** | - | Base DNS domain name for external endpoints (e.g. `example.com`) |
| `environment` | **Yes** | - | Sub-environment descriptor for Spanner database keys (e.g. `dev`) |
| `iap_client_id` | **Yes** | - | Google OAuth Client ID for identity verification |
| `iap_client_secret`| **Yes** | - | Google OAuth Client Secret for identity verification |
| `jwt_secret` | **Yes** | - | Secret key used by ESS backend to sign session cookies |
| `allowed_emails` | Conditional | - | Comma-separated list of emails authorized to log in, including CI service accounts (see [Authentication](docs/authentication.md)) - required when `authorized_iam_role` is not provided |
| `authorized_iam_role`| Conditional | - | GCP IAM role for user workload access - required when `allowed_emails` is not provided |
| `admin_iam_role` | No | `"roles/owner"` | GCP IAM role required for administrative access (Health Service, Admin Panel) |
| `certs_email` | **Yes** | - | Owner email registered with Google Trust Services (GTS) |
| `gts_key_id` | **Yes** | - | GTS External Account Binding key ID for certificates |
| `gts_hmac_key` | **Yes** | - | GTS External Account Binding HMAC secret for certificates |
| `image_registry` | **Yes** | - | Target Google Artifact Registry repository path for XMC images |
| `image_tag` | No | `"latest"` | OCI container image tag to deploy (e.g. git commit hash or release version) |
| `namespace` | No | `"xmc"` | Target Kubernetes namespace for services |
| `gcp_public_cidrs_access_enabled` | No | `false` | Toggles public endpoints on the GKE Control Plane |
| `deletion_protection` | No | `false` | Enables deletion protection on supported GCP resources (databases, Cloud Run) |
| `authorized_cidr` | No | `"0.0.0.0/0"`| Authorized IP CIDR allowed to connect to GKE master |
| `task_signature_ttl_seconds` | No | `86400` | Validity duration of task signature tokens |
| `pause_scheduling_enabled` | No | `true` | Enables the global workload scheduling pause feature across the Admin Panel, Health Service, Actuator, and ESS |
| `log_monitor_deployment_enabled` | No | `true` | Runs the Log Monitor as a GKE Deployment (`true`) instead of a Cloud Run service (`false`) |
| `health_scheduling_resume_batch_size` | No | `100` | Number of paused tasks replayed per batch from Spanner during unpause resumption |
| `health_scheduling_resume_max_backlog` | No | `500` | Maximum unprocessed tasks in `xmc-tasks-stream` before resumption worker pauses replay |
| `use_redis_queue` | No | `true` | Uses Memorystore Redis for internal queues when true |
| `deploy_redis` | No | `true` | Provisions Memorystore Redis instance when true |
| `redis_region` | No | - | Memorystore Redis region (defaults to region above) |
