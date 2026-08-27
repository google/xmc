# Development Environment Tooling (`tools/dev_env/`)

The `tools/dev_env/` package provides automation tools and a unified command-line interface (`dev_env.py`) for managing XManager on Cloud (XMC) development environments.

---

## Unified CLI Entrypoint (`dev_env.py`)

The unified CLI `dev_env.py` consolidates environment lifecycle operations for remote Google Kubernetes Engine (GKE) deployments into a single extensible entrypoint.

### Usage

```bash
# Direct invocation via Python
python3 tools/dev_env/dev_env.py [command] [options...]

# Or via top-level forwarding wrapper
./tools/dev_env.py [command] [options...]
```

### Global Flags

| Flag | Short | Description |
| :--- | :--- | :--- |
| `--verbose` | `-v` | Enable detailed debug logging output |
| `--quiet` | `-q` | Suppress non-error output (log ERROR level only) |
| `--version` | | Print dev_env version and exit |
| `--help` | `-h` | Display command help and usage examples |

---

## Subcommands Reference

### 1. `gke-deploy` (Deploy Remote GKE Environment)
Compiles Cluster Toolkit (`gcluster`) blueprints and orchestrates step-by-step remote infrastructure and service deployment.

```bash
python3 tools/dev_env/dev_env.py gke-deploy \
  --config deploy/ctk_v2/xmanager-cloud-deployment.yaml \
  --project-id <PROJECT_ID> \
  --environment <ENV_NAME> \
  --dns-project <DNS_PROJECT_ID> \
  --dns-zone <DNS_ZONE_NAME>
```

#### Options
- `--config`, `-c`: Path to the Cluster Toolkit deployment YAML configuration file (required).
- `--project-id`, `-p`: Google Cloud project ID (required, or via `PROJECT_ID` env var).
- `--environment`, `-e`: Deployment environment name (required, or via `XMANAGER_ENV` env var).
- `--dns-project`: Google Cloud project hosting Cloud DNS (required).
- `--dns-zone`: Cloud DNS managed zone name (required).
- `--start-from`: Start execution from a specific step (`compile`, `apis`, `primary`, `dns`, `secondary`, `helm`, `finalize`).
- `--only`: Execute only a single step (`compile`, `apis`, `primary`, `dns`, `secondary`, `helm`, `finalize`).
- `--auto-approve`, `-y`: Automatically approve Terraform and Cluster Toolkit prompts.
- `--skip-terraform-init`: Skip `terraform init` during deployment.

---

### 2. `gke-upgrade` (Upgrade Remote GKE Services)
Incrementally rebuilds changed microservices, pushes OCI container images to Artifact Registry, and rolls out Helm chart updates or Spanner migrations without destroying infrastructure.

```bash
python3 tools/dev_env/dev_env.py gke-upgrade \
  --config deploy/ctk_v2/xmanager-cloud-deployment.yaml \
  --project-id <PROJECT_ID> \
  --environment <ENV_NAME>
```

#### Options
- `--config`, `-c`: Path to deployment YAML configuration file.
- `--project-id`, `-p`: Google Cloud project ID.
- `--environment`, `-e`: Deployment environment name.
- `--run-migrations`, `-m`: Run Cloud Spanner database schema migrations during upgrade.
- `--start-from`: Start upgrade from a specific step (`images`, `migrate`, `helm`, `finalize`).
- `--only`: Execute only a single upgrade step (`images`, `migrate`, `helm`, `finalize`).

---

### 3. `spanner` (Cloud Spanner Database Management)
Automates verification, single-batch DDL schema creation with compiled protobuf descriptor sets, and teardown for Cloud Spanner databases used by XMC microservices.

```bash
# Create all required Spanner databases for the local environment
python3 tools/dev_env/dev_env.py spanner create \
  --spanner-instance xmc-local \
  --ess-db ess-local \
  --xid-db xid-local

# Run SDL schema migrations against existing Spanner databases
python3 tools/dev_env/dev_env.py spanner migrate \
  --spanner-instance xmc-local \
  --ess-db ess-local \
  --xid-db xid-local

# Drop/delete Spanner databases during local environment cleanup
python3 tools/dev_env/dev_env.py spanner drop \
  --spanner-instance xmc-local \
  --ess-db ess-local \
  --xid-db xid-local
```

#### Actions
- `create`: Checks for database existence, builds protobuf descriptor binary sets (`//api/protos/experiment:experiment_messages_descriptor`, `//api/protos/xid:xid_messages_descriptor`), and creates databases with single-batch DDL in parallel.
- `migrate`: Executes idempotent SDL database schema migrations against existing Cloud Spanner databases. Automatically skips schema migrations if a database was newly created (as initial DDL already contains full schema).
- `drop`: Drops the specified databases from the Cloud Spanner instance.

---

### 4. `cluster` (Local KinD Cluster Management)
Manages the lifecycle of the local Kubernetes-in-Docker (KinD) cluster, verifies pre-flight dependencies, configures port mappings, and injects host authentication credentials and TLS certificates.

```bash
# Verify prerequisites and create the local KinD cluster
python3 tools/dev_env/dev_env.py cluster create --cluster-name xmc-local

# Tear down the local KinD cluster
python3 tools/dev_env/dev_env.py cluster delete --cluster-name xmc-local
```

#### Actions
- `create`:
  1. Validates host prerequisites (`docker`, `kind`, `kubectl`, `helm`, `openssl`, `gcloud`, `bazel`).
  2. Provisions a KinD cluster configured with extra port mappings:
     - `8080` (Local HTTP Gateway Proxy)
     - `5556` (Dex OIDC Identity Provider)
  3. Injects Kubernetes secrets from the host environment:
     - `gcp-sa-key`: Extracted from active `gcloud auth application-default` ADC credentials.
     - `backend-oauth-secrets`: Shared OIDC client credentials for Dex authentication.
     - `validation-secret`: Self-signed SAN TLS certificate for webhook validation and token signing.
     - `redis-secret`: Local Redis cache authentication credentials.
- `delete`: Safely deletes the local KinD cluster and its underlying containers.

---

### 5. `status` (Cluster Status & Health Inspection)
Displays the operational status of the local KinD cluster, pod readiness across `xmc` and `dex` namespaces, and endpoint health.

```bash
python3 tools/dev_env/dev_env.py status --cluster-name xmc-local
```

#### Output Sections
- **Cluster & Node Readiness**: KinD cluster control-plane and worker node readiness.
- **Support & Service Pods**: Pod status table for core services and dependencies (`dex`, `redis`, `gateway-proxy`, `experiment_state_server`, `ui`, etc.).
- **Endpoint Health**: HTTP probes verifying local Gateway Proxy (`http://localhost:8080`) and Dex OIDC (`http://localhost:5556/dex/.well-known/openid-configuration`).

---

### 6. `logs` (Stream Microservice Logs)
Streams or tails container stdout/stderr logs from a specific microservice running in the local KinD cluster.

```bash
# Tail last 50 lines of logs for experiment_state_server
python3 tools/dev_env/dev_env.py logs experiment_state_server --lines 50

# Follow continuous logs in real-time
python3 tools/dev_env/dev_env.py logs ui -f
```

---

### 7. Generic Aliases (`deploy` / `upgrade`)
For convenience, generic `deploy` and `upgrade` commands are supported and forward to their respective target handlers:

```bash
# Equivalent to gke-deploy
python3 tools/dev_env/dev_env.py deploy --target gke --config ... --dns-project ... --dns-zone ...

# Equivalent to gke-upgrade
python3 tools/dev_env/dev_env.py upgrade --target gke --config ...
```

---

## Configuration Architecture (`LocalDevConfig`)

The `tools/dev_env/config.py` module defines `LocalDevConfig` and provides unified configuration discovery and resolution.

### Resolution Precedence

Configuration values are resolved using strict layered precedence:

```
CLI Flags (--project-id, --environment, --config)
  └── Environment Variables (PROJECT_ID, XMANAGER_ENV, XMC_CONFIG_PATH)
        └── YAML Configuration File (deploy/ctk_v2/*.yaml or deploy/local/*.yaml)
              └── Built-in Defaults
```

### Config File Discovery

When `--config` is not explicitly provided, `dev_env.py` searches candidate locations in order:
1. `deploy/local/xmanager-cloud-local.yaml` (Developer local configuration)
2. `deploy/ctk_v2/xmanager-cloud-deployment.yaml` (Active deployment configuration)
3. `deploy/ctk_v2/xmanager-cloud-deployment-staging.yaml` (Staging configuration)

### Local Configuration Template

A sample local configuration template is provided at [`deploy/local/xmanager-cloud-local-template.yaml`](../../deploy/local/xmanager-cloud-local-template.yaml). Copy it to create your local environment config:

```bash
cp deploy/local/xmanager-cloud-local-template.yaml deploy/local/xmanager-cloud-local.yaml
```

### Feature Flags & Enabled Services

`LocalDevConfig` dynamically evaluates feature flags from YAML configuration to compute the list of enabled microservices (`enabled_services`):

| Feature Flag in YAML | Enabled Microservice Target | Default |
| :--- | :--- | :--- |
| `enable_metrics: true` | `ingestion_gateway` | `true` |
| `enable_components: true` | `components` | `false` |
| `rod_deployment_enabled: true` | `rod_data_provider` | `false` |

*Core base services* (`experiment_state_server`, `xid_service`, `actuator`, `reconciler`, `ui`, `dashboard_service`, `health_service`) are enabled by default.

### Dynamic Resource Naming Rules

When resource names are omitted from the YAML configuration, `LocalDevConfig` automatically derives dynamic naming conventions from the active `environment`:

- **Cloud Spanner Instance**: `xmc-<environment>` (e.g. `xmc-local`)
- **ESS Database**: `ess-<environment>` (e.g. `ess-local`)
- **XID Database**: `xid-<environment>` (e.g. `xid-local`)
- **KinD Cluster Name**: `xmc-<environment>` (e.g. `xmc-local`)

### CLI Configuration Overrides

Override configuration settings for local development directly from the command line:

```bash
# Override Spanner instance, databases, and cluster name
python3 tools/dev_env/dev_env.py \
  --spanner-instance custom-instance \
  --ess-db custom-ess-db \
  --xid-db custom-xid-db \
  --cluster-name custom-kind-cluster \
  --auto-migrate
```

---

## Testing

Run unit tests for the `tools/dev_env` package with Bazel:

```bash
bazel test //tools/dev_env:...
```
