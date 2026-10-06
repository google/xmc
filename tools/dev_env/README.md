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

### Local KinD Environment Commands

#### 1. `up` (Complete Local Environment Bootstrap)
Bootstraps the entire end-to-end local XMC developer stack in a single automated command:
1. Provisions Cloud Spanner databases (ESS & XID) with compiled protobuf descriptor sets.
2. Creates the local KinD cluster with ingress port mappings (`8080`, `5556`).
3. Injects host ADC credentials, Dex client secrets, and self-signed TLS certificates.
4. Applies support manifests (Dex OIDC, Redis, Gateway Proxy, dummy CRDs).
5. Compiles Bazel OCI image tarballs in parallel and side-loads them into KinD nodes.
6. Synthesizes Helm `values.yaml` and deploys the XMC Helm chart into KinD.

```bash
# Bootstrap local environment with default configuration
python3 tools/dev_env/dev_env.py up

# Recreate KinD cluster and rebuild all images
python3 tools/dev_env/dev_env.py up --recreate-cluster

# Skip image compilation (e.g. if images are already loaded)
python3 tools/dev_env/dev_env.py up --no-build-images
```

##### Options
- `--config`, `-c`: Path to YAML deployment configuration.
- `--cluster-name`: KinD cluster name (defaults to dynamic `xmc-<environment>`).
- `--recreate-cluster`: Delete and recreate the KinD cluster if it already exists.
- `--no-build-images`: Skip OCI image compilation and side-loading.
- `--no-deploy-helm`: Skip Helm chart deployment.
- `--no-auto-migrate`: Skip automatic Cloud Spanner schema migration execution.
- `--remote-k8s-context`: Explicit kubectl context for the remote GKE cluster (e.g. `gke_<PROJECT>_<REGION>_<CLUSTER>`).
- `--remote-k8s`: Shorthand for `--remote-k8s-context` (or enables hybrid mode auto-detecting the cluster context from the deployment config).
- `--remote-k8s-namespace`: Namespace on the remote cluster hosting the ServiceAccount and JobSets (default: `xmc`).
- `--remote-k8s-service-account`: ServiceAccount used to mint the remote access token (default: `actuator-service-account`).

---

#### 2. `down` (Tear Down Local Environment)
Tears down the local KinD cluster and releases local Docker and system resources.

```bash
# Delete local KinD cluster
python3 tools/dev_env/dev_env.py down

# Delete local KinD cluster and drop Spanner databases
python3 tools/dev_env/dev_env.py down --drop-db
```

##### Options
- `--cluster-name`: KinD cluster name to delete.
- `--drop-db`: Also drop Cloud Spanner databases associated with the local environment.

---

#### 3. `reload` (Smart Fast Microservice Reload)
Incrementally recompiles modified microservices with Bazel, updates container images in KinD, and triggers targeted Kubernetes rollout restarts without recreating the cluster or rerunning full deployments (~5-10s iteration speed).

```bash
# Auto-detect modified source files and reload affected microservices
python3 tools/dev_env/dev_env.py reload

# Continuous watch mode (debounced hot reload on file save)
python3 tools/dev_env/dev_env.py reload -w
python3 tools/dev_env/dev_env.py reload --watch --watch-interval 2.0

# Force incremental Helm chart update
python3 tools/dev_env/dev_env.py reload --helm

# Explicitly reload specific microservices by canonical name or alias
python3 tools/dev_env/dev_env.py reload ess ui

# Reload using full canonical snake_case or kebab-case service names
python3 tools/dev_env/dev_env.py reload experiment_state_server actuator reconciler
python3 tools/dev_env/dev_env.py reload experiment-state-server xid-service

# Reload all enabled microservices
python3 tools/dev_env/dev_env.py reload all

# Synchronize Helm chart manifests alongside reload
python3 tools/dev_env/dev_env.py reload ess --helm
```

##### Options
- `[services...]`: Positional microservice names or aliases to rebuild and reload (or omit for auto-detection).
- `--watch`, `-w`: Run in continuous filesystem watch mode, automatically detecting code edits and reloading affected services on save.
- `--interval`: Polling interval in seconds for watch mode (default: `1.5`).
- `--base-ref`: Optional git reference or commit to compare against for change detection (e.g. `origin/main`).
- `--helm`: Force an incremental Helm chart upgrade.
- `--cluster-name`: KinD cluster name (defaults to dynamic `xmc-<environment>`).
- `--tag`: Docker image tag (default: `local-dev`).

##### Supported Service Aliases & Canonical Names
Aliases are convenience shortcuts for CLI ergonomics. Any canonical microservice target (`snake_case` or `kebab-case`) can always be specified directly even if it does not have a dedicated alias in `SERVICE_ALIASES`:

| Alias | Canonical Microservice Target |
| :--- | :--- |
| `ess` | `experiment_state_server` |
| `xid` | `xid_service` |
| `ui`, `frontend`, `web` | `ui` |
| `gw`, `gateway` | `ingestion_gateway` (`gateway-proxy`) |
| `actuator` | `actuator` |
| `reconciler` | `reconciler` |
| `dash`, `dashboard` | `dashboard_service` |
| `health` | `health_service` |
| `rod` | `rod_data_provider` |
| `comp` | `components` |
| `all` | Reload all enabled microservices |

> [!TIP]
> When adding a new microservice under `cmd/<new_service>`, it works immediately with `dev_env.py reload <new_service>` or `dev_env.py reload <new-service>` without needing to define an alias in `tools/dev_env/service_resolver.py`.

---

#### 4. `status` (Cluster Status & Health Inspection)
Displays the operational status of the local KinD cluster, pod readiness across `xmc` and `dex` namespaces, and endpoint health.

```bash
python3 tools/dev_env/dev_env.py status --cluster-name xmc-local
```

##### Output Sections
- **Cluster & Node Readiness**: KinD cluster control-plane and worker node readiness.
- **Support & Service Pods**: Pod status table for core services and dependencies (`dex`, `redis`, `gateway-proxy`, `experiment_state_server`, `ui`, etc.).
- **Endpoint Health**: HTTP probes verifying local Gateway Proxy (`http://localhost:8080`) and Dex OIDC (`http://localhost:5556/dex/.well-known/openid-configuration`).

---

#### 5. `logs` (Stream Microservice Logs)
Streams or tails container stdout/stderr logs from a specific microservice running in the local KinD cluster.

```bash
# Tail last 50 lines of logs for experiment_state_server
python3 tools/dev_env/dev_env.py logs experiment_state_server --lines 50

# Follow continuous logs in real-time
python3 tools/dev_env/dev_env.py logs ui -f
```

---

#### 6. `build-images` (Parallel OCI Container Builds & KinD Side-Loading)
Compiles Bazel OCI image tarballs in parallel and side-loads them directly into the local KinD cluster nodes without remote container registry pushes.

```bash
# Build and load all enabled microservice images into KinD
python3 tools/dev_env/dev_env.py build-images --cluster-name xmc-local

# Build all discovered buildable services across the repository
python3 tools/dev_env/dev_env.py build-images all --cluster-name xmc-local

# Build and load specific microservices
python3 tools/dev_env/dev_env.py build-images \
  --services experiment_state_server ui \
  --cluster-name xmc-local
```

##### Dynamic Service Discovery & OCI Conventions
`ImageBuilder` relies on dynamic repository discovery rather than hardcoded service dictionaries:
- **Automatic Discovery**: Scans `cmd/*/BUILD.bazel` for `oci_image_for_service` or `_image_tarball` definitions.
- **Convention Derivation**:
  - **Bazel Package**: `cmd/<service>`
  - **Bazel Tarball Target**: `//cmd/<service>:<service>_image_tarball`
  - **Output Archive**: `<service>_image_tarball.tar` (under `bazel-bin/` or cross-platform fallback `bazel-out/`)
  - **Docker Tag**: `<service>_image:latest`
- **Extensibility**: Adding a new Go or UI microservice under `cmd/<service>` with an OCI target works immediately with `dev_env.py build-images` without requiring tooling modifications.

##### Options
- `--services`, `-s`: Specific microservice images to build (defaults to all enabled services, or `all` for all discovered repository microservices).
- `--cluster-name`: Target KinD cluster name for image side-loading (`kind load docker-image`).
- `--tag`: Image tag applied to local container images (defaults to `local-dev`).
- `--push`: Push built container images to remote Artifact Registry (used for GKE deployments).
- `--no-load-kind`: Skip loading built container images into KinD nodes.

---

#### 7. `cluster` (Local KinD Cluster Management)
Manages the lifecycle of the local Kubernetes-in-Docker (KinD) cluster, verifies pre-flight dependencies, configures port mappings, and injects host authentication credentials and TLS certificates.

```bash
# Verify prerequisites and create the local KinD cluster
python3 tools/dev_env/dev_env.py cluster create --cluster-name xmc-local

# Tear down the local KinD cluster
python3 tools/dev_env/dev_env.py cluster delete --cluster-name xmc-local
```

##### Actions
- `create`:
  1. Validates host prerequisites (`docker`, `kind`, `kubectl`, `helm`, `openssl`, `gcloud`, `bazel`).
  2. Provisions a KinD cluster configured with extra port mappings (`8080`, `5556`).
  3. Injects Kubernetes secrets from host ADC, Dex client credentials, and self-signed TLS certificates.
- `delete`: Safely deletes the local KinD cluster and its underlying containers.

---

#### 8. `spanner` (Cloud Spanner Database Management)
Automates verification, single-batch DDL schema creation with compiled protobuf descriptor sets, schema migrations, and teardown for Cloud Spanner databases.

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

##### Actions
- `create`: Checks for database existence, builds protobuf descriptor binary sets (`//api/protos/experiment:experiment_messages_descriptor`, `//api/protos/xid:xid_messages_descriptor`), and creates databases with single-batch DDL in parallel.
- `migrate`: Executes idempotent SDL database schema migrations against existing Cloud Spanner databases. Automatically skips schema migrations if a database was newly created (as initial DDL already contains full schema).
- `drop`: Drops the specified databases from the Cloud Spanner instance.

---

### Remote GKE Deployment Commands

#### 9. `gke-deploy` (Deploy Remote GKE Environment)
Compiles Cluster Toolkit (`gcluster`) blueprints and orchestrates step-by-step remote infrastructure and service deployment.

```bash
python3 tools/dev_env/dev_env.py gke-deploy \
  --config deploy/ctk_v2/xmanager-cloud-deployment.yaml \
  --project-id <PROJECT_ID> \
  --environment <ENV_NAME> \
  --dns-project <DNS_PROJECT_ID> \
  --dns-zone <DNS_ZONE_NAME>
```

##### Options
- `--config`, `-c`: Path to the Cluster Toolkit deployment YAML configuration file (required).
- `--project-id`, `-p`: Google Cloud project ID (required, or via `PROJECT_ID` env var).
- `--environment`, `-e`: Deployment environment name (required, or via `XMANAGER_ENV` env var).
- `--dns-project`: Google Cloud project hosting Cloud DNS (required).
- `--dns-zone`: Cloud DNS managed zone name (required).
- `--start-from`: Start execution from a specific step (`compile`, `apis`, `primary`, `dns`, `secondary`, `helm`, `finalize`).
- `--only`: Execute only a single step (`compile`, `apis`, `primary`, `dns`, `secondary`, `helm`, `finalize`).
- `--auto-approve`, `-y`: Automatically approve Terraform and Cluster Toolkit prompts.
- `--skip-terraform-init`: Skip `terraform init` during deployment.

> [!NOTE]
> The `compile` step (and the `app` step of `gke-upgrade`) deletes `ctk-out/<deployment_name>/.ghpc/staged/` before running `gcluster create --force`, so Helm templates removed from the repository are not re-deployed from a stale staged copy. It also passes a SHA-256 of `deploy/helm` as the `helm_chart_content_hash` blueprint variable (recorded in the umbrella Helm release description), so template-only chart changes trigger a real Helm upgrade that prunes removed objects.

> [!NOTE]
> For step-by-step developer verification workflows, port-forwarding, and manual testing procedures on developer clusters, see the [Developer Cluster Setup & Manual Testing Guide](../../deploy/local/README.md).

---

#### 10. `gke-upgrade` (Upgrade Remote GKE Services)
Incrementally rebuilds changed microservices, pushes OCI container images to Artifact Registry, and rolls out Helm chart updates or Spanner migrations without destroying infrastructure.

```bash
python3 tools/dev_env/dev_env.py gke-upgrade \
  --config deploy/ctk_v2/xmanager-cloud-deployment.yaml \
  --project-id <PROJECT_ID> \
  --environment <ENV_NAME>
```

##### Options
- `--config`, `-c`: Path to deployment YAML configuration file.
- `--project-id`, `-p`: Google Cloud project ID.
- `--environment`, `-e`: Deployment environment name.
- `--run-migrations`, `-m`: Run Cloud Spanner database schema migrations during upgrade.
- `--start-from`: Start upgrade from a specific step (`images`, `migrate`, `helm`, `finalize`).
- `--only`: Execute only a single upgrade step (`images`, `migrate`, `helm`, `finalize`).

---

### 11. Generic Aliases (`deploy` / `upgrade`)
For convenience, generic `deploy` and `upgrade` commands are supported and forward to their respective target handlers:

```bash
# Equivalent to gke-deploy
python3 tools/dev_env/dev_env.py deploy --target gke --config ... --dns-project ... --dns-zone ...

# Equivalent to gke-upgrade
python3 tools/dev_env/dev_env.py upgrade --target gke --config ...
```

---

## Helm Deployment Layer (`HelmDeployer`)

The `tools/dev_env/helm_deployer.py` module encapsulates Helm chart rendering, local values generation, and rollout orchestration for the XMC application stack on Kubernetes.

### Local Values Overlay (`deploy/helm/values-local.yaml`)
To adapt the production XMC Helm chart for local development, `values-local.yaml` overrides cloud-specific configurations:
- Disables GCP Ingress and Workload Identity annotations.
- Routes backend traffic through local HTTP Gateway Proxy (`localhost:8080`) and Dex OIDC (`localhost:5556`).
- Enables development-mode authentication and CORS policies.

### Dynamic Values Generation
`HelmDeployer.generate_local_values()` dynamically renders a unified values file by combining:
1. **Base Values**: Loaded from `deploy/helm/values-local.yaml`.
2. **Spanner Configuration**: Local Spanner instance, databases (`ess_database`, `xid_database`), and emulator endpoints.
3. **Container Images**: Local image repositories and image tags (`local-dev`) side-loaded via KinD.
4. **Feature Flags**: Dynamic activation of optional microservices (`enable_metrics`, `enable_components`, `rod_deployment_enabled`, `health_service_deployment_enabled`, `dashboard_service_deployment_enabled`).

### Deployment & Rollout Monitoring
`HelmDeployer.deploy()` applies the Helm release (`xmc`) and invokes `wait_for_rollouts()`:
- Automatically discovers all active deployments in the target namespace (`kubectl get deployments`).
- Monitors rollout status (`kubectl rollout status`) with configurable timeouts to ensure all microservices transition to ready state.

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
| `enable_metrics: true` | `ingestion_gateway` | `false` |
| `enable_components: true` | `components` | `false` |
| `rod_deployment_enabled: true` | `rod_data_provider` | `true` |
| `health_service_deployment_enabled: true` | `health_service` (Admin Panel) | `true` |
| `dashboard_service_deployment_enabled: true` | `dashboard_service` | `false` |

*Core base services* (`experiment_state_server`, `xid_service`, `actuator`, `reconciler`, `ui`) are always enabled.

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

## Helm Chart Sanitization & Deployment Diagnostics

When deploying to local KinD clusters, `HelmDeployer` prepares an isolated, sanitized copy of the production Helm chart:

### 1. Cloud-Only Resource Pruning
The sanitizer dynamically removes Google Cloud-specific resources that require GKE or external cloud controllers:
- **Gateway API CRDs & HTTPRoutes**: Stripped in favor of the lightweight local NGINX Gateway Proxy (`deploy/local/gateway-proxy.yaml`).
- **Google ManagedCertificates**: Replaced with local self-signed TLS secrets.
- **BackendConfig & Cloud Armor**: Omitted on local KinD environments.
- **Horizontal & Vertical Pod Autoscalers (HPA / VPA)**: Removed to avoid local CPU metric contention.

### 2. Local Pod Optimizations
- **Image Pull Policy**: Automatically patched from `Always` to `IfNotPresent` across all deployment templates, ensuring locally built images loaded via `kind load docker-image` are reused instantly without registry pull failures.

### 3. Automated Rollout Failure Diagnostics
If a deployment fails or times out during `dev_env up` or Helm upgrade:
- Automatically captures failing pod descriptions (`kubectl describe pod`).
- Fetches recent pod container stdout/stderr log snippets (`kubectl logs --tail=50`).
- Dumps Kubernetes namespace events (`kubectl get events --sort-by=.metadata.creationTimestamp`) to aid immediate troubleshooting.

---

## Smart Change Detection & Fast Reload Architecture

The `ChangeDetector` module inspects the active git working tree and staged diffs (`git status --porcelain`, `git diff --name-only HEAD`) and maps modified paths to affected microservices:

### Path Mapping Rules
- `cmd/experiment_state_server/**`, `internal/storage/spanner/**`, `api/protos/experiment/**` $\rightarrow$ `experiment_state_server`
- `cmd/xid_service/**`, `api/protos/xid/**` $\rightarrow$ `xid_service`
- `cmd/actuator/**` $\rightarrow$ `actuator`
- `cmd/reconciler/**` $\rightarrow$ `reconciler`
- `cmd/ui/**` $\rightarrow$ `ui`
- `cmd/ingestion_gateway/**`, `api/protos/metrics/**` $\rightarrow$ `ingestion_gateway`
- `cmd/dashboard_service/**`, `internal/dashboard/**`, `api/protos/dashboard/**` $\rightarrow$ `dashboard_service`
- `cmd/health_service/**`, `api/protos/health/**` $\rightarrow$ `health_service`
- `cmd/rod_data_provider/**`, `internal/rod/**` $\rightarrow$ `rod_data_provider`
- **Shared Code** (`internal/common/**`, `internal/auth/**`, `pkg/**`): Rebuilds and reloads all enabled microservices.
- **Helm Templates / Manifests** (`deploy/helm/**`, `test/e2e/manifests/**`): Triggers incremental Helm chart upgrade.

### Iteration Speed Benchmarks

| Operation | Typical Latency | Notes |
| :--- | :--- | :--- |
| **Go Backend Reload** (`ess`, `xid`, etc.) | **~5–8 seconds** | Bazel incremental build + `kind load` + `kubectl rollout restart` |
| **Angular UI Reload** (`ui`) | **~6–10 seconds** | BuildKit cache hit + `kind load` + `kubectl rollout restart` |
| **Live Helm Upgrade** (`--helm`) | **~3–5 seconds** | Sanitized chart diff application without cluster restart |
| **Full Bootstrap** (`dev_env up`) | **~35–45 seconds** | Full Spanner DDL + KinD provision + parallel builds + Helm deploy |

---

## Environment Architecture & Scope: Local KinD vs. Remote GKE

The development tooling supports two distinct deployment tiers tailored for different phases of engineering. It is important to distinguish between **capabilities that are inherently cloud-dependent (never reachable locally)** and **capabilities that are reachable or planned for local KinD**.

```
+-----------------------------------------------------------------------------------+
|                              XMC Development Tiers                                |
+-------------------------------------------------+---------------------------------+
|              Local KinD Environment             |       Remote GKE Deployment     |
|             (tools/dev_env/dev_env.py)          |      (tools/upgrade_dev_env.py) |
+-------------------------------------------------+---------------------------------+
|  • Sub-second Inner-Loop Fast Reload            |  • Real Multi-Slice TPU Topologies |
|  • Web UI & API Gateway Routing                 |  • NVIDIA GPU Accelerators      |
|  • Dex OIDC Authentication & Session JWTs       |  • Cloud Storage FUSE Mounts    |
|  • Cloud Spanner DDL Migrations & Queries       |  • Workload Identity & GCP IAM  |
|  • Open-Schema Dummy CRDs (JobSet / Kueue)      |  • Multi-Cluster MultiKueue     |
|  • (Roadmap) CPU Mock & Kueue Quota Controllers |  • Dynamic ProvisioningRequests |
+-------------------------------------------------+---------------------------------+
```

### 1. Inherent Cloud Dependencies (Never Reachable Locally)
Certain hardware and cloud infrastructure primitives cannot run inside a local Docker container / KinD cluster and strictly require a live Google Cloud GKE deployment (`dev_env gke-deploy` / `dev_env gke-upgrade`):
- **Specialized Accelerators & Topologies**: Physical Cloud TPUs (`google.com/tpu` v4, v5e, v5p, v6e), multi-host optical circuit switches (OCS), and NVIDIA Cloud GPUs.
- **Cloud IAM & Storage Integrations**: Google Cloud Workload Identity Federation, Cloud Storage FUSE CSI driver mounting real GCS buckets, Google Cloud Armor, and Google-managed OAuth / Identity-Aware Proxy (IAP).
- **Dynamic GCE Capacity Reservations**: Integrating Kueue `ProvisioningRequest` CRDs with Google Compute Engine (GCE) dynamic capacity reservation APIs and GKE Cluster Autoscaler.

### 2. Local KinD Scope: Current Baseline vs. Reachable Capabilities

| Capability | Current Local KinD Baseline | Reachable on KinD (Roadmap) | Remote GKE |
| :--- | :---: | :---: | :---: |
| **Control Plane & Web UI** | ✅ Live (`dev_env up`, `reload`) | ✅ Live | ✅ Live |
| **Dex OIDC Authentication** | ✅ Live (`dex.yaml`) | ✅ Live | ✅ Cloud IAP / OAuth |
| **Cloud Spanner Migrations** | ✅ Live (`spanner.py`) | ✅ Live | ✅ Real Cloud Spanner |
| **CRD API Schema Validation** | ✅ Schema Registered (`dummy_crds.yaml`)| ✅ Full Upstream CRDs | ✅ GKE Add-ons |
| **Kueue Local Quota & Admission**| ⚠️ CRD Registered (No Controller) | 🟢 **Reachable** (Kueue Controller) | ✅ Live Real Queues |
| **Reconciler State Machine** | ⚠️ Go Unit/Integration Tests | 🟢 **Reachable** (Upstream Controllers) | ✅ Live Real Pods |
| **CPU Mock Workload Execution** | ❌ Controller not installed | 🟢 **Reachable** (Mock JobSets/Pods) | ✅ Live Real Pods |
| **Multi-Cluster MultiKueue** | ❌ Single KinD Cluster | 🟢 **Reachable** (Dual-KinD on Docker Net)| ✅ Live Multi-Cluster |
| **TPU / GPU Hardware Execution** | ❌ Never Reachable | ❌ Never Reachable | ✅ Live Real Hardware |
| **Dynamic GCE ProvisioningRequests**| ❌ Never Reachable | ❌ Never Reachable | ✅ Live Cloud Capacity |

#### Current Baseline with Dummy CRDs:
To keep local startup near-instantaneous (~35s) and memory overhead minimal, KinD installs open-schema CustomResourceDefinitions ([`dummy_crds.yaml`](../../test/e2e/manifests/dummy_crds.yaml)) for `JobSet` and `Kueue` (`x-kubernetes-preserve-unknown-fields: true`).
- **What "CRD Registered (No Controller)" means**: The CRD schemas are registered on the Kubernetes API server, allowing `kubectl apply`, Helm, and client CRUD operations from `actuator` and `reconciler` to succeed without schema validation rejections (`no matches for kind "JobSet"`). However, the background controller manager pods that reconcile CRDs into worker pods are inactive.
- **What is tested in-memory**: The complete Reconciler state machine (`SCHEDULING` $\rightarrow$ `STARTING` $\rightarrow$ `RUNNING` $\rightarrow$ `COMPLETED`/`FAILED`/`PARTIALLY_FAILED`, `ImagePullBackOff`, unschedulable handling, Non-Restarting JobSets Contract) is verified via comprehensive Go table-driven integration tests (`cmd/reconciler/jobset/converter_test.go`).

#### Reachable Local Enhancements (Roadmap):
1. **Single-Cluster Workload Controllers (`--with-workload-controllers`)**:
   - Deploys upstream `sigs.k8s.io/jobset` and `sigs.k8s.io/kueue` into the local KinD cluster.
   - Kueue manages local queue admission, quota limits, and priority preemption on standard CRs (`ClusterQueue`, `LocalQueue`, `ResourceFlavor`, `WorkloadPriorityClass`).
   - Creating a `JobSet` spawns actual child `Job` and `Pod` containers running mock CPU tasks locally on Docker.
   - `reconciler` observes real pod lifecycles and updates WorkUnit database records live in Spanner.
2. **Dual-KinD Multi-Cluster MultiKueue (`--multi-cluster`)**:
   - Bootstraps two KinD clusters on the same Docker bridge network (`xmc-mgmt` and `xmc-worker`).
   - Because MultiKueue only requires control-plane API server connectivity (`kube-apiserver` client traffic over the internal Docker network `https://xmc-worker-control-plane:6443`) and does *not* require pod overlay routing, cross-cluster workload dispatching and status synchronization can be validated 100% locally without cloud infrastructure.

### 3. Hybrid Execution Model: KinD Control Plane + Remote GKE Data Plane (`--remote-k8s`)

For verifying real JobSet execution, Kueue quota admission, multi-slice TPU topologies, or running agentic regression testing without deploying the full control plane into GKE, XMC supports a **hybrid execution topology**:

```
+-------------------------------------------------------------+
|               Local Host / Docker (KinD)                    |
|                                                             |
|  [ Web UI / Gateway ]  <--->  [ Dex OIDC ]                 |
|           |                                                 |
|  [ ESS / XID Services ] <------------------------+          |
|           |                                      |          |
|  [ Actuator & Reconciler ]                       |          |
|           | (Static Kubeconfig via /etc/remote-k8s)         |
+-----------|--------------------------------------|----------+
            | (HTTPS)                              | (RPC)
            v                                      v
+------------------------------------+  +---------------------+
|        Remote GKE Data Plane       |  |  GCP Cloud Spanner  |
|                                    |  +---------------------+
|  • Kueue Controller Manager        |  | • experiment-db     |
|  • JobSet Controller Manager       |  | • xids-db           |
|  • Real Nodes / TPUs / Worker Pods |  +---------------------+
+------------------------------------+
```

#### 3.1 Why Use Hybrid Mode?

| Topology | Control Plane | Data Plane (JobSets/Pods) | Best For |
| :--- | :--- | :--- | :--- |
| **Pure KinD (`dev_env up`)** | Local KinD | Local KinD (Dummy CRDs or mock CPU pods) | Instant inner-loop API testing, UI iteration, fast unit/E2E verification without cloud resources. |
| **Hybrid Mode (`--remote-k8s`)** | Local KinD | Remote GKE Cluster (Real Nodes/TPUs/Kueue) | Fast inner-loop dev while testing actual JobSet creation, Kueue admission, TPU multi-slice execution, and real pod scheduling. |
| **Full GKE Deployment** | Remote GKE | Remote GKE | Production staging, formal release qualification, multi-user environments. |

#### 3.2 Prerequisites & Obtaining Remote Cluster Credentials
1. **Google Cloud SDK**: Active GCP authentication with Application Default Credentials (ADC) for Cloud Spanner access:
   ```bash
   gcloud auth login
   gcloud auth application-default login
   ```
2. **Obtaining GKE Credentials & Understanding Context Names**:
   Fetch credentials for your remote cluster using `gcloud`:
   ```bash
   gcloud container clusters get-credentials <CLUSTER_NAME> \
     --region <REGION> \
     --project <PROJECT_ID>
   ```
   Running `gcloud container clusters get-credentials` creates an entry in your local `~/.kube/config` following GKE's standard naming convention:
   ```text
   gke_<PROJECT_ID>_<REGION>_<CLUSTER_NAME>
   ```
   You can view your currently active context at any time with:
   ```bash
   kubectl config current-context
   ```
   Or list all available contexts on your machine:
   ```bash
   kubectl config get-contexts
   ```
3. **Remote Cluster Setup**: The remote GKE cluster must have the target namespace (`xmc`) and a ServiceAccount with RBAC permissions to manage JobSets and Kueue workloads (by default, `actuator-service-account` provisioned by Cluster Toolkit v2).

#### 3.3 Step-by-Step Quickstart Walkthrough

##### Step 1: Launch Hybrid Development Environment
Start the local KinD control plane and bind it to your remote GKE data plane using one of the following methods:

```bash
# Method A (Recommended): Explicitly pass your remote cluster's kubectl context
python3 tools/dev_env/dev_env.py up --remote-k8s-context gke_my-project_us-central1_my-cluster

# Or use your currently active kubectl context
python3 tools/dev_env/dev_env.py up --remote-k8s-context $(kubectl config current-context)

# Method B: Auto-detect from deployment config if using xmanager-cloud-deployment.yaml
python3 tools/dev_env/dev_env.py up --remote-k8s
```

What `dev_env up --remote-k8s` executes automatically:
- Extracts the remote API server URL and CA certificate from your host kubeconfig.
- Mints a 24-hour bearer token for `actuator-service-account` on the remote GKE cluster via the Kubernetes TokenRequest API.
- Mounts a static kubeconfig into the local KinD cluster inside the `remote-k8s-config` Secret.
- Deploys the local XMC Helm chart with `k8s.remote.enabled=true`, configuring local Actuator and Reconciler to manage remote JobSets.

##### Step 2: Verify Cluster Status
Check that all local microservices are running and connected:
```bash
python3 tools/dev_env/dev_env.py status
```

##### Step 3: Launch an Experiment
Submit an experiment to the local Experiment State Server:
```bash
# Via REST API
curl -X POST http://localhost:8080/v1/experiments \
  -H "Content-Type: application/json" \
  -d '{"experiment_id": "hybrid-test", "display_name": "Hybrid Test"}'
```
Or launch via the Python SDK / test script.

##### Step 4: Verify Remote JobSet Scheduling
Observe the workload progressing on the **remote GKE cluster**:
```bash
# Check JobSets created by the local actuator on the remote cluster
kubectl --context <REMOTE_CONTEXT> -n xmc get jobsets

# Check Kueue workload admission
kubectl --context <REMOTE_CONTEXT> -n xmc get workloads

# Check physical pods running on remote nodes/TPUs
kubectl --context <REMOTE_CONTEXT> -n xmc get pods -l jobset.sigs.k8s.io/jobset-name
```

Simultaneously, inspect local controller logs in KinD:
```bash
python3 tools/dev_env/dev_env.py logs actuator
python3 tools/dev_env/dev_env.py logs reconciler
```

##### Step 5: Fast Inner-Loop Microservice Reload
When modifying Actuator, Reconciler, or ESS source code, use fast reload to update local containers in ~5-10 seconds without touching the remote GKE cluster:
```bash
python3 tools/dev_env/dev_env.py reload
```

##### Step 6: Teardown
Tear down the local KinD cluster when finished:
```bash
python3 tools/dev_env/dev_env.py down
```

#### 3.4 Configuration Reference

| Option | CLI Flag | Environment Variable | Configuration YAML | Default |
| :--- | :--- | :--- | :--- | :--- |
| **Enable Hybrid Mode** | `--remote-k8s` | `REMOTE_K8S=true` | `remote_k8s: true` | `false` |
| **Remote K8s Context** | `--remote-k8s-context <ctx>` | `REMOTE_K8S_CONTEXT` | `remote_k8s_context` | `gke_<proj>_<reg>_<cluster>` or active context |
| **Remote Namespace** | `--remote-k8s-namespace <ns>` | `REMOTE_K8S_NAMESPACE` | `remote_k8s_namespace` | `xmc` |
| **ServiceAccount** | `--remote-k8s-service-account <sa>` | `REMOTE_K8S_SERVICE_ACCOUNT` | `remote_k8s_service_account` | `actuator-service-account` |

Example `dev_env.yaml`:
```yaml
remote_k8s: true
remote_k8s_context: "gke_my-project_us-central1_my-cluster"
remote_k8s_namespace: "xmc"
remote_k8s_service_account: "actuator-service-account"
```

#### 3.5 Security Model & Zero Cloud-SDK Invariant
- **Zero Cloud SDK in Containers**: Actuator and Reconciler container images contain no Google Cloud SDK, `gcloud` CLI, or Google IAM credential libraries. The hybrid configuration uses standard Kubernetes TokenRequest bearer tokens mounted via standard `/etc/remote-k8s/kubeconfig`.
- **RBAC Scoping**: The minted token carries only the permissions granted to `actuator-service-account` on the remote cluster (limited to managing JobSets and reading pod statuses in the `xmc` namespace).
- **Spanner Security**: Local containers connect to Cloud Spanner through standard host ADC volume mounts (`~/.config/gcloud/application_default_credentials.json`), ensuring identity and auditing consistency with GCP IAM.

#### 3.6 Troubleshooting

- **Error: "Token output was empty" or "Failed to mint token"**:
  - Ensure your host `gcloud` session has not expired: run `gcloud auth login` and refresh credentials.
  - Verify that `kubectl create token actuator-service-account -n xmc` succeeds on the remote context directly.
- **Error: "Failed to query remote cluster configuration"**:
  - Verify that the context name passed to `--remote-k8s-context` matches an entry in `kubectl config get-contexts`.
- **JobSet Created but Not Scheduling**:
  - Check remote Kueue cluster queues: `kubectl --context <REMOTE_CONTEXT> get clusterqueues`.
  - Ensure the requested compute resources (CPUs, GPUs, TPUs) have matching ResourceFlavors on the remote GKE cluster.

---

## Testing

Run unit tests for the `tools/dev_env` package with Bazel:

```bash
bazel test //tools/dev_env:...
```
