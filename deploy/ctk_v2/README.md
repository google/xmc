# XManager on Cloud (XMC) - Cluster Toolkit v2 Deployment Guide

This directory contains the official Google Cloud HPC Cluster Toolkit v2 blueprints and configurations to automate the provisioning of the complete XManager on Cloud (XMC) infrastructure stack leveraging the modern GKE-hosted topology. This is intended for initial deployment of XMC. This tooling is not intended for upgrading existing instances to latest version of XMC. 

## Introduction

This blueprint is created to help deploy XMC leveraging the current topology which uses:
1. **GKE Deployment for UI**: The UI runs inside the GKE cluster itself, instead of Cloud Run.
2. **GKE Gateway API**: Exposes the UI, HTTP API, and gRPC endpoints on a single Global External IP address using Gateway and HTTPRoute resources.
3. **App-level OAuth2**: Users are authenticated using OAuth2 client credentials and session JWTs managed by the application itself.
4. **cert-manager with Google Trust Services (GTS)**: Automates the provisioning of TLS certificates.

## Phase 1: Configuration Variables

First, copy the template deployment configuration file and open it to configure deployment variables to match your target environment:
```bash
cp deploy/ctk_v2/xmc-deployment-template.yaml deploy/ctk_v2/xmanager-cloud-deployment.yaml
```
Open `deploy/ctk_v2/xmanager-cloud-deployment.yaml` and configure the deployment variables:

### Core Variables
*   **`deployment_name`**: A unique string identifier appended to all provisioned resource names (e.g., `xmc-v2-run`).
*   **`project_id`**: The target Google Cloud Project ID where the XMC infrastructure will be deployed.
*   **`region`**: The GCP region for GKE compute node pools, Spanner, and networking assets (e.g., `us-west1`).
*   **`environment`**: An environment suffix tag used in resource names (e.g., `xmcv2`).
*   **`domain_name`**: The base DNS domain name for the API endpoints (e.g., `example.com`). For details on why a domain is required and how to configure free Google-managed domains, see [Domain Configuration in INSTALL.md](../../INSTALL.md#3-dns-and-domain-name-configuration).

### App-level Authentication Configuration
*   **`iap_client_id`** & **`iap_client_secret`**: OAuth Web Client credentials used by the application backend for Google OAuth2 flow. For step-by-step instructions on creating these, see [OAuth2 Client Setup in INSTALL.md](../../INSTALL.md#4-oauth2-client-iap-credentials-setup).
*   **`jwt_secret`**: A unique JWT secret key used by the backend to sign and verify cookies.
*   **`allowed_emails`**: A comma-separated list of email addresses permitted to sign in to XManager (e.g. `"user1@example.com,user2@example.com"`). Write the plain list: the blueprint escapes the commas for Helm itself, so do not add backslashes.
*   **`authorized_iam_role`**: The GCP IAM Role for user workload access (e.g., `projects/YOUR_GCP_PROJECT/roles/YOUR_IAM_ROLE`).
*   **`admin_iam_role`**: The GCP IAM Role required for administrative access (e.g., `roles/owner` or a custom admin role). Default is `roles/owner`.

### cert-manager GTS Configuration
For step-by-step setup of Google Trust Services (GTS) External Account Binding (EAB) keys, see [GTS cert-manager Setup in INSTALL.md](../../INSTALL.md#5-cert-manager-and-google-trust-services-gts-setup).
*   **`certs_email`**: Email address to register with cert-manager.
*   **`gts_key_id`**: GTS External Account Binding key ID.
*   **`gts_hmac_key`**: GTS External Account Binding HMAC secret.

### Feature Switches
*   **`dashboard_service_deployment_enabled`**: Boolean flag (`true`/`false`, default: `false`). Set to `true` to provision the Dashboard Service backend and UI visualization components for custom chart and dashboard visualizations.
*   **`rod_deployment_enabled`**: Boolean flag (`true`/`false`, default: `true`). Provisions the Resource Optimization & Diagnostics (ROD) Data Provider.
*   **`health_service_deployment_enabled`**: Boolean flag (`true`/`false`, default: `true`). Provisions the Health Service backing the Admin Panel.
*   **`pause_scheduling_enabled`**: Boolean flag (`true`/`false`, default: `true`). Set to `true` to enable the global workload scheduling pause feature across the Admin Panel, Health Service, Actuator, and ESS (`XMC_PAUSE_SCHEDULING_ENABLED`). Resumption batch size and backlog throttling can be tuned via `health_scheduling_resume_batch_size` (default: `100`) and `health_scheduling_resume_max_backlog` (default: `500`).
*   **`log_monitor_deployment_enabled`**: Boolean flag (`true`/`false`, default: `true`). Controls whether the Log Monitor runs as a GKE Deployment (`true`) or as a Cloud Run service (`false`).
*   **`enable_managed_mldiagnostics`**: Boolean flag (`true`/`false`, default: `false`). Set to `true` to enable Google Cloud Managed ML Diagnostics on the GKE cluster, enable the Hypercompute Cluster API, and configure IAM roles for MLRun management and GCS trace uploads.
*   **`managed_xprof_gcs_bucket`**: GCS bucket for Managed XProf profiling data (e.g. `gs://<project>-mlruns`). When set, the actuator injects the GCS destination into workload JobSets so Managed ML Diagnostics (xprof) captures land in GCS. Requires Managed ML Diagnostics enabled on the cluster (see [Managed ML Diagnostics (Managed XProf)](#managed-ml-diagnostics-managed-xprof)).

> [!IMPORTANT]
> **Metrics Dashboards Requirement**: Metrics remain opt-in and disabled by default out-of-the-box (`dashboard_service_deployment_enabled` defaults to `false`, and `redpanda`, `clickhouse`, and `ingestion-gateway` all default to `enabled: false` in `stable-2026a`), so standard XMC deployments do not require BYO ClickHouse or Redpanda. `xmc-bridge` is always deployed by the umbrella chart; to enable end-to-end "Metrics dashboards" and chart visualization functionality:
> 1. Set `dashboard_service_deployment_enabled: true` (and `clickhouse_password` if your ClickHouse instance uses a password).
> 2. Provision the Redpanda and ClickHouse backing infrastructure (see [`deploy/addons/metrics`](../addons/metrics/clickhouse/README.md)).
> 3. Enable `redpanda`, `clickhouse`, and `ingestion-gateway` in the [`stable-2026a` Component profile (`xmc-profile`)](../profiles/stable-2026a/README.md) (`xmc-bridge` installs the Ingestion Gateway workload from [its component chart](../profiles/stable-2026a/components/ingestion-gateway/README.md) and `dashboard-service` resolves ClickHouse coordinates from `Component/clickhouse`).
> 4. If `dashboard-service` was already running before step 3, restart it once so it picks up the new data engine (it decides at startup whether `Component/clickhouse` is enabled): `kubectl rollout restart deployment/dashboard-service-deployment -n xmc`.

### Managed ML Diagnostics (Managed XProf)
Managed XProf uses the native GKE Managed ML Diagnostics feature, available on GKE clusters running version **`1.35.0-gke.3065000` or later**.

Enable it in your deployment blueprint by setting the variables in `deploy/ctk_v2/xmanager-cloud-deployment.yaml`:
```yaml
vars:
  enable_managed_mldiagnostics: true
  managed_xprof_gcs_bucket: "gs://<project>-mlruns"
```
Then run deployment normally:
```bash
python3 tools/dev_env/dev_env.py gke-deploy --config deploy/ctk_v2/xmanager-cloud-deployment.yaml ...
```

For existing clusters not provisioned with `enable_managed_mldiagnostics: true`, enable the feature flag on the GKE cluster via:
```bash
gcloud container clusters update <CLUSTER_NAME> \
  --project <PROJECT_ID> --location <REGION> \
  --enable-managed-mldiagnostics
```

> **Note:** Only clusters on `1.35.0-gke.3065000+` are supported. Older clusters (which would require installing the ML Diagnostics operator via Helm) are out of scope for this deployment path.

### ClickHouse Connection & Credentials (Metrics Pipeline)
ClickHouse is Bring-Your-Own infrastructure deployed out-of-band; see the [ClickHouse Addon Guide](../addons/metrics/clickhouse/README.md). When the [`clickhouse` `Component` CR](../profiles/stable-2026a/components/clickhouse/README.md) in `xmc-profile` is enabled at pod startup, `dashboard-service` dynamically resolves its ClickHouse connection coordinates (`host`, `nativePort`, `defaultDatabase`, `user`, and `secretName`) from `Component/clickhouse` and hot-swaps on subsequent CR or Secret updates without pod restarts. If you enable `Component/clickhouse` for the first time after `dashboard-service` is already running, restart it once so it initializes its dynamic chart data engine:

```bash
kubectl rollout restart deployment/dashboard-service-deployment -n xmc
```

Enabling `Component/clickhouse` also makes ClickHouse a readiness dependency of `dashboard-service`, so keep `components.clickhouse.enabled: false` on clusters without a reachable ClickHouse instance.
*   **`clickhouse_password`**: ClickHouse password rendered into the `clickhouse-secret` Kubernetes Secret in the release namespace (never a ConfigMap). The blueprint does not enable the Secret Manager CSI driver, so this is the path it uses; chart releases that enable CSI should leave this empty and follow [Sourcing the password from Secret Manager](../addons/metrics/clickhouse/README.md#sourcing-the-password-from-secret-manager) instead.

### Database Backend (Spanner or PostgreSQL)
**`database_backend`** selects the storage backend: `spanner` (default) or `postgres`.
*   **`spanner`**: The blueprint provisions the Cloud Spanner instance, the ESS and XID databases, and applies their schemas. Existing deployments are unaffected.
*   **`postgres`**: Spanner, its schema appliers, and the `spanner.googleapis.com` API are skipped, and the log monitor runs in-cluster (the Cloud Run log monitor only supports Spanner). Point the workloads at your PostgreSQL instance with:
    *   **`postgres_host`**: PostgreSQL hostname reachable from the cluster.
    *   **`postgres_port`**: PostgreSQL port (default: `5432`).
    *   **`postgres_user`** / **`postgres_password`**: Credentials used by the workloads.
    *   **`postgres_ess_database`** / **`postgres_xid_database`**: Databases for the Experiment State Server and XID service.
    *   **`postgres_ssl_mode`**: libpq `sslmode`. Empty uses the chart default (`verify-full`).

> **Note:** The `tools/dev_env` deploy and upgrade tooling skips Spanner schema migrations when `database_backend` is `postgres` (overridable via the `DB_BACKEND` environment variable).

---

## 🏁 Phase 2: How to Deploy

### Step 1: Compile the Blueprint
Navigate into your master directory containing the `cluster-toolkit` folder and compile the blueprint:

```bash
./cluster-toolkit/gcluster create deploy/ctk_v2/xmanager-cloud.yaml -d deploy/ctk_v2/xmanager-cloud-deployment.yaml -o ctk-out -l WARNING -w --force
```
This command compiles the modules and generates the `xmc-v2-run` deployment folder inside `ctk-out/` containing the generated Terraform code.

> [!IMPORTANT]
> `gcluster create --force` copies the Helm chart into `ctk-out/<deployment_name>/.ghpc/staged/` but never deletes files that were removed from the repository, so deleted templates keep being deployed. When re-compiling an existing deployment after Helm templates were removed or renamed, delete that directory first (`rm -rf ctk-out/<deployment_name>/.ghpc/staged`); `gcluster create` re-stages every file. The `tools/dev_env` deploy and upgrade tooling does this automatically.

### Step 2: Provision the Infrastructure
Execute the deployment to begin provisioning:

```bash
./cluster-toolkit/gcluster deploy ctk-out/xmc-v2-run
```
Type `a` and press Enter to approve the rollout of each group when prompted by the CLI.

---

## 🧹 Phase 3: How to Destroy

To safely tear down all provisioned infrastructure, run:

```bash
./cluster-toolkit/gcluster destroy ctk-out/xmc-v2-run --robust
```

---

## 🔍 Post-Deployment Verification

Once deployment completes successfully, run the post-deployment verification script:

```bash
python3 ./deploy/ctk_v2/verify_xmc.py
```
This script checks the status of all pods, services, gateway routing, and certificates in the `xmc` namespace.
