
## Protocol Buffers & gRPC Stubs (Step 1)

All proto files are located inside the `./api/protos` directory. They share the `cloud.xmanager.v1` package name and are built with modern **Bazel** rules, translating internal-style import paths (e.g. `third_party/xmanager_cloud/...`) automatically.

### Build Commands

To build all Protocol Buffer definitions and their corresponding Go gRPC/stub libraries, run:

```bash
bazel build //api/protos/...
```

This will build the following targets:
- `//api/protos/xid:messages_go_proto` (Xid messages stubs)
- `//api/protos/xid:api_go_proto` (Xid service gRPC/API stubs)
- `//api/protos/experiment:messages_go_proto` (Experiment messages stubs)
- `//api/protos/experiment:api_go_proto` (Experiment service gRPC/API stubs)
- `//api/protos/task_queue:task_go_proto` (Task queue message stubs)

### Compatibility Packages

A set of compatibility packages is defined under `//internal/compat/...` to route legacy, Google-specific library imports back to standard public Go packages:
- `protobuf/field_mask_go_proto` -> `google.golang.org/protobuf/types/known/fieldmaskpb`
- `longrunning/operations_go_proto` -> `google.golang.org/genproto/googleapis/longrunning`
- `grpc_proto/health_go_proto` -> `google.golang.org/grpc/health/grpc_health_v1`

## Custom AIP-160 Filter & Parser Engine (Step 3)

The repository includes a custom, zero-dependency AIP-160 compliant filter parsing and query translation engine under `//api/filter/go/filter` and `//api/filter/go/queryfilterbase`. This parser replaces legacy dependencies with standard Go stubs and includes custom priority logical operator parsing (implicit space-conjunction, explicit `AND`, explicit `OR`), type safety verification, regular expression constraints, and disjunction IN list optimizations.

### Build and Test Commands

To build all filter component library targets, run:
```bash
bazel build //api/filter/...
```

To run the complete suite of parser, compiler, and query generator tests, run:
```bash
bazel test //internal/query_generator/... --nocache_test_results
```

This will run and pass the following test suites:
- `//internal/query_generator:equalityvisitor_test` (LHS/RHS equivalence verification)
- `//internal/query_generator:querytools_test` (Heuristic and table index candidate selection stubs)
- `//internal/query_generator/filter:filter_test` (Spanner filter visitor and type safety integration)
- `//internal/query_generator/normalizer:normalizer_test` (Filter normalization visitor)
- `//internal/query_generator:liststatement_test` (End-to-end list statements translation integration)

## Private Code Tree & Internal Packages (Step 5)

Dedicated convenient targets are defined at the repository root `BUILD.bazel` to build and verify all private package library modules:

- **Build all decoupled core library modules**:
  ```bash
  bazel build //:internal_libs
  ```

- **Run all core unit test suites**:
  ```bash
  bazel test //:internal_tests
  ```

This test suite executes the following verified tests:
- `//internal/auth:auth_test` (User and Service Account role access authorization stubs)
- `//internal/logging:logging_test` (Standard operational logs output format constraints)
- `//internal/security:security_test` (Hot-reloaded certificates and programmatic key conversions)
- `//internal/task_queue:task_queue_test` (Cloud Tasks actuation payloads and OIDC assertion queues)
- `//internal/validator:validator_test` (AIP-compliant list parameter schema validations)
- All custom AIP-160 Query Generator unit tests listed under Step 3.

### Running Spanner Database Health Unit Tests

Unit tests under `//internal/common:health_test` verify database query latency by connecting to a Cloud Spanner Emulator. If a background emulator is not running, these tests will timeout. To run a local Spanner Emulator in Docker:

1. **Start the Spanner Emulator Docker container**:
   ```bash
   docker run -d -p 9010:9010 -p 9020:9020 gcr.io/cloud-spanner-emulator/emulator
   ```

2. **Configure the connection environment variable**:
   ```bash
   export SPANNER_EMULATOR_HOST=localhost:9010
   ```

3. **Run the health tests target directly**:
   ```bash
   bazel test //internal/common:health_test
   ```

## Building & Testing Core Application Services (Step 6)

The primary daemon services are defined under the `./cmd` directory and can be built and tested standalone using their corresponding Bazel rules:

### Reconciler Service
The **Reconciler** manages custom Kubernetes resources (JobSets, Workloads, and ClusterQueues) and orchestrates scheduling and priority transitions.

- **Build the Reconciler binary**:
  ```bash
  bazel build //cmd/reconciler:reconciler
  ```

- **Run all reconciler test suites**:
  ```bash
  bazel test //cmd/reconciler/...
  ```

This test suite executes:
- `//cmd/reconciler/clusterqueue:clusterqueue_test` (ClusterQueue cache state sync checks)
- `//cmd/reconciler/kueue_sync:kueue_sync_test` (Pending workloads scheduling state checks)
- `//cmd/reconciler/shared:shared_test` (State update batcher and throttling logic)
- `//cmd/reconciler/jobset:jobset_test` (JobSet state to AIP transitions and converters)
- `//cmd/reconciler/workload:workload_test` (Workload custom converters)

### Log Monitor Service
The **Log Monitor** subscribes to execution log entries on Pub/Sub and updates work unit status entities accordingly.

- **Build the Log Monitor binary**:
  ```bash
  bazel build //cmd/log_monitor:log_monitor
  ```

- **Run the log monitor test suite**:
  ```bash
  bazel test //cmd/log_monitor/...
  ```

## Continuous Integration

[![XMC Unit Tests](https://github.com/google/xmc/actions/workflows/unit-tests.yml/badge.svg)](https://github.com/google/xmc/actions/workflows/unit-tests.yml)

Automated unit test suites and code quality checks are executed on:
* **Pull Requests** targeting any branch.
* **Direct Pushes** to the `test` (main baseline) and `feature/unit_tests_action` branches.
* **Manual Dispatch** (`workflow_dispatch`) via the GitHub Actions UI.

### Implemented Linting & Code Quality Rules

To maintain pristine architecture, canonical styling, and avoid common defects across the monorepo, code quality rules are enforced locally via a pre-commit hook (`.githooks/pre-commit`).

The following specific linting rules and static analysis toolchains are implemented across our supported languages:

#### 1. Go Code (`golangci-lint`)
Go source files (`.go`) are analyzed using the **`golangci-lint`** orchestrator. The following core rules and static analysis checks are enforced:
* **`gofmt` / `goimports`**: Enforces canonical whitespace, indentation, and canonical alphabetical ordering of standard and third-party import statements.
* **`govet`**: Inspects code for suspicious constructs, shadowed variables, un-keyed composite literals, and printf-style argument format mismatches.
* **`staticcheck`**: Applies state-of-the-art static analysis to detect unused constants/variables, out-of-bounds slice access, unclosed HTTP response bodies, dead code paths, and deprecated API calls.
* **`errcheck` / `gosimple`**: Ensures all returned error values are explicitly checked and simplifies redundant boolean, channel, or loop constructs.

#### 2. Bazel Build Files (`buildifier`)
Bazel build graphs (`BUILD`, `BUILD.bazel`, `WORKSPACE`, `MODULE.bazel`, and `.bzl` files) are formatted and analyzed using the official Bazel **`buildifier`** toolchain. Enforced rules include:
* **Canonical Formatting**: Standardizes indentation (4 spaces for rule declarations), canonical keyword ordering, and proper multi-line list formatting.
* **Target Sorting**: Automatically sorts `srcs`, `deps`, and `data` attribute lists alphabetically to prevent merge conflicts.
* **Lint Warnings (`-lint=warn`)**: Flags deprecated Bazel syntax (such as legacy `load()` statements), un-pinned external dependencies, unused variable declarations inside `.bzl` macros, and non-canonical label references.

#### Auto-Fixing Code Locally
Developers can automatically format their code and fix lint violations locally before pushing by enabling the repository pre-commit hook:
```bash
git config core.hooksPath .githooks
```

---

## Global Repository Commands

To build the entire project codebase or run all decoupled standalone test suites at once:

- **Build all components and stubs**:
  ```bash
  bazel build //...
  ```

- **Run all unit and integration test suites**:
  ```bash
  bazel test //...
  ```

- **Build Core codebase**:
  ```bash
  bazel build //cmd/... //internal/...
  ```

- **Build all OCI images**:
  ```bash
  bazel build //cmd/actuator:actuator_image_tarball \
            //cmd/experiment_state_server:experiment_state_server_image_tarball \
            //cmd/log_monitor:log_monitor_image_tarball \
            //cmd/reconciler:reconciler_image_tarball \
            //cmd/xid_service:xid_service_image_tarball
  ```

- **Push all OCI images to GCP Artifact Registry**:
  Define the environment variables and run the corresponding push targets:
  ```bash
  export XMC_ACR_TARGET=us-docker.pkg.dev/my-project/my-registry
  export XMC_ENV=dev # Optional, defaults to "dev"
  export BUILD_TAG=latest # Optional, defaults to "latest"

  bazel run //cmd/actuator:actuator_image_push
  bazel run //cmd/experiment_state_server:experiment_state_server_image_push
  bazel run //cmd/log_monitor:log_monitor_image_push
  bazel run //cmd/reconciler:reconciler_image_push
  bazel run //cmd/xid_service:xid_service_image_push
  ```

---

## Deployment Topologies

XMC supports two deployment topologies using automated HPC Cluster Toolkit blueprints:

### 1. Cluster Toolkit Blueprint v1 ([deploy/ctk_v1](deploy/ctk_v1))
This is the legacy/initial topology which uses:
- **GKE Deployments**: Runs the backend services (Reconciler, Actuator, and Experiment State Server) on GKE.
- **Web UI on Cloud Run**: The frontend UI is hosted separately on Google Cloud Run.
- **Identity-Aware Proxy (IAP)**: Protects backend services and manages identity/access control at the infrastructure load balancer.
- **State & Messaging**: Uses Cloud Spanner for state storage, Memorystore Redis or Cloud Tasks for background queues, and Pub/Sub for log streams.
- **DNS/IP**: Public IPs and Managed DNS zones.

For more details, see the [deploy/ctk_v1/README.md](deploy/ctk_v1/README.md).

### 2. Cluster Toolkit Blueprint v2 ([deploy/ctk_v2](deploy/ctk_v2))
This is the modern/current topology which consolidates components:
- **Unified GKE Hosting**: The frontend UI runs inside the GKE cluster itself, instead of Cloud Run.
- **GKE Gateway API**: Exposes the UI, HTTP API, and gRPC endpoints on a single Global External IP address using Gateway and HTTPRoute resources.
- **App-level OAuth2**: Users are authenticated using OAuth2 client credentials and session JWTs managed by the application itself, removing infrastructure-level IAP requirements.
- **cert-manager with Google Trust Services (GTS)**: Automates the provisioning of TLS certificates.
- **State & Messaging**: Cloud Spanner for state storage and Memorystore Redis for internal queues.

For more details, see the [deploy/ctk_v2/README.md](deploy/ctk_v2/README.md).

---

## Installation & Deployment

XManager on Cloud (XMC) can be installed using one of the following deployment paths depending on your tooling and workflow preferences.

### 1. Terraform and Helm (Hands-on)
For a direct, hands-on infrastructure deployment:
1. **Infrastructure**: Deploy the VPC networks, GKE cluster, Cloud Spanner, and Memorystore Redis resources manually or via your organization's custom Terraform pipeline.
2. **Application Helm Release**: Connect your local `kubectl` to the GKE cluster and deploy the application chart using Helm:
   ```bash
   helm upgrade --install xmc ./deploy/helm -n xmc --create-namespace -f my-values.yaml
   ```
   *For details on custom values configurations, see [deploy/helm/values.yaml](deploy/helm/values.yaml).*

### 2. Cluster Toolkit Blueprints (Recommended)
We provide automated HPC Cluster Toolkit blueprints to provision the entire cloud-native resource stack in a single orchestration workflow:
* **XMC v2 Blueprint**: Configures GKE clusters, Spanner, Redis, static IPs, and Google Cloud Certificate Manager SSL maps.
* For step-by-step instructions on compilation and group-by-group deployments, see [deploy/ctk_v2/README.md](deploy/ctk_v2/README.md).

### 3. Automated Agent Deployment (`deploy_xmc`)
If you are running an agentic session, the deployment can be fully automated using the `deploy_xmc` skill. For details on how to run this skill, see [Antigravity Agent Workflows](#antigravity-agent-workflows-ai-assisted) below.

---

## Antigravity Agent Workflows (AI-Assisted)

This repository includes pre-configured **Antigravity Agent Skills** located under the [.agents/skills/](.agents/skills/) directory. These skills define structured workflows that allow the agent to automate complex operational and development tasks.

### 1. Automated Blueprint Deployment (`deploy_xmc`)
The `deploy_xmc` skill compiles and deploys Cluster Toolkit v2 blueprints. It validates system dependencies, deploys GCP resource groups sequentially, pauses to prompt for manual DNS/OAuth registrations, and runs verification checks.

* **How to trigger**: Ask the agent to deploy the environment using the config overrides path, for example:
  > Deploy XMC using the blueprint configuration at `deploy/ctk_v2/xmanager-cloud-deployment.yaml`
* **Workflow Details**: See [deploy_xmc/SKILL.md](.agents/skills/deploy_xmc/SKILL.md).

### 2. PR Comment-Driven Development (`pr_comment_workflow`)
The `pr_comment_workflow` skill automates resolving review comments on Pull Requests. The agent fetches comment context, determines affected packages and GKE deployments, runs unit tests and coverage checks, commits code, replies to the comment, pushes container images, and restarts GKE deployments.

* **How to trigger**: Ask the agent to resolve a specific GitHub discussion/review link, for example:
  > Address the PR review comment: `https://github.com/google/xmc/pull/15#discussion_r124892`
* **Workflow Details**: See [pr_comment_workflow/SKILL.md](.agents/skills/pr_comment_workflow/SKILL.md).

---

### Configuration Reference


The following parameters can be supplied in your Cluster Toolkit overrides file (`xmanager-cloud-deployment.yaml`) or your Helm overrides (`values.yaml`):

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
| `allowed_emails` | **Yes** | - | Comma-separated list of user emails authorized to log in |
| `authorized_iam_role`| **Yes** | - | GCP IAM role granted to GKE workload identities |
| `certs_email` | **Yes** | - | Owner email registered with Google Trust Services (GTS) |
| `gts_key_id` | **Yes** | - | GTS External Account Binding key ID for certificates |
| `gts_hmac_key` | **Yes** | - | GTS External Account Binding HMAC secret for certificates |
| `xid_image` | **Yes** | - | Container image path for the XID service |
| `actuator_image` | **Yes** | - | Container image path for the Actuator daemon |
| `ess_image` | **Yes** | - | Container image path for the Experiment State Server (ESS) |
| `ui_image` | **Yes** | - | Container image path for the Frontend UI portal |
| `reconciler_image` | **Yes** | - | Container image path for the Kueue Reconciler service |
| `log_monitor_image`| **Yes** | - | Container image path for the Pub/Sub Log Monitor |
| `namespace` | No | `"xmc"` | Target Kubernetes namespace for services |
| `gcp_public_cidrs_access_enabled` | No | `false` | Toggles public endpoints on the GKE Control Plane |
| `authorized_cidr` | No | `"0.0.0.0/0"`| Authorized IP CIDR allowed to connect to GKE master |
| `task_signature_ttl_seconds` | No | `86400` | Validity duration of task signature tokens |
| `use_redis_queue` | No | `true` | Uses Memorystore Redis for internal queues when true |
| `deploy_redis` | No | `true` | Provisions Memorystore Redis instance when true |
| `redis_region` | No | - | Memorystore Redis region (defaults to region above) |