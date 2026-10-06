# Developer Documentation

This document describes how to build, test, and lint the codebase, configure continuous integration, and set up your local development environment.

New to the codebase? Read the [Architecture Overview](architecture/overview.md) first, then
[Use Cases §3](architecture/use_cases.md#3-new-contributor) for where to find the code and which
invariants are enforced.

---

## Tools & Scripts Virtual Environment

All automation tools, deployment scripts (`tools/dev_env/dev_env.py`, `tools/dev_env/deploy_dev_env.py`, `tools/dev_env/upgrade_dev_env.py`), and verification scripts (`deploy/ctk_v2/verify_xmc.py`) require a Python virtual environment. Virtual environments must be explicitly created using **Python 3.13.14**:

```bash
python3.13 -m venv ./tools/venv
source ./tools/venv/bin/activate
pip install --upgrade pip
pip install -r ./tools/common/requirements.txt
```

Or run the automated bootstrap script using Python 3.13.14:
```bash
python3 ./tools/common/setup_venv.py --venv-dir ./tools/venv --requirements ./tools/common/requirements.txt
```

---

## Local Development & Hybrid Execution (`tools/dev_env`)

For rapid inner-loop development without deploying full environments to GKE, developers can run the XMC control plane locally inside a Docker-based KinD cluster:

* **Full Local KinD Bootstrap**: `python3 tools/dev_env/dev_env.py up`
* **Hybrid Mode (KinD Control Plane + Remote GKE Data Plane)**: `python3 tools/dev_env/dev_env.py up --remote-k8s-cluster <CLUSTER_NAME>` (or `--remote-k8s`)
  Connects the local KinD control plane to a remote GKE cluster for real JobSet execution, Kueue quota admission, and TPU topologies.
* **Smart Fast Microservice Reload**: `python3 tools/dev_env/dev_env.py reload`
  Recompiles modified Go/Python/UI services and updates running KinD pods in ~5-10 seconds.
* **Cluster Teardown**: `python3 tools/dev_env/dev_env.py down`

For complete architectural details, prerequisites, and CLI options, see the [Development Environment Tooling Guide](../tools/dev_env/README.md).

---

## Protocol Buffers & gRPC Stubs

All proto files are located inside the [api/protos](../api/protos) directory. They share the `cloud.xmanager.v1` package name and are built with modern **Bazel** rules, translating internal-style import paths (e.g. `third_party/xmanager_cloud/...`) automatically.

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

- **Cross-compiling OCI images**:
  By default the `build` command uses build host architecture for target deployment processor architecture (i.e. running `bazel build` on an ARM processor will build ARM OCI images). To enforce a specific target deployment platform (for example `arm` vs `amd64`) please use the `--config` flag. For example adding `--config=linux_amd64` will ensure that the Go services are built for the `amd64` architecture:
  ```bash
  bazel build --config=linux_amd64 //cmd/actuator:actuator_image_tarball \
            //cmd/experiment_state_server:experiment_state_server_image_tarball \
            //cmd/log_monitor:log_monitor_image_tarball \
            //cmd/reconciler:reconciler_image_tarball \
            //cmd/xid_service:xid_service_image_tarball
  ```
  For the UI image, specify the target platform via the `XMC_UI_PLATFORM` environment variable:
  ```bash
  XMC_UI_PLATFORM=linux/amd64 bazel build //cmd/ui:ui_image_tarball
  ```

- **Push all OCI images to GCP Artifact Registry**:
  Define the environment variables and run the corresponding push targets:
  ```bash
  export XMC_ACR_TARGET=us-docker.pkg.dev/my-project/my-registry
  export XMC_ENV=dev # Optional, defaults to "dev"
  export BUILD_TAG=latest # Optional, defaults to "latest"

### Compatibility Packages

A set of compatibility packages is defined under `//internal/compat/...` to route legacy, Google-specific library imports back to standard public Go packages:
- `protobuf/field_mask_go_proto` -> `google.golang.org/protobuf/types/known/fieldmaskpb`
- `longrunning/operations_go_proto` -> `google.golang.org/genproto/googleapis/longrunning`
- `grpc_proto/health_go_proto` -> `google.golang.org/grpc/health/grpc_health_v1`

### Breaking-Change Check (`buf breaking`)

[`tools/check_proto_breaking.py`](../tools/check_proto_breaking.py) detects backward-incompatible changes to the first-party protobuf APIs:
* Builds the aggregate descriptor set [`//api:all_protos_descriptor_set`](../api/BUILD.bazel) for the working tree (including uncommitted changes) and for the base revision, which is checked out in a temporary `git worktree` that is always removed afterwards.
* Compares both with [`buf breaking`](https://buf.build/docs/breaking/) using the strict `FILE` rules from [`api/buf-breaking.yaml`](../api/buf-breaking.yaml). The rules are read from the base revision so that a change cannot relax its own check: edits to them take effect once merged, and the working tree's copy is used, with a warning, only if the base revision has none. Bundled third-party `google/*` files are ignored; unstable packages such as `components/v1alpha1` are checked because their messages are persisted.
* Fails, listing the uncovered files, if a `proto_library` under `//api` is missing from `//api:all_protos_descriptor_set`. `api/filter/filter_expression.proto` has no `proto_library` and is not covered.

To run the check, [install buf](https://buf.build/docs/cli/installation/) on your `PATH` and run:
```bash
# Compares against the merge-base of HEAD and origin/main.
./tools/check_proto_breaking.py
# Compares against another revision; extra Bazel flags apply to both builds.
./tools/check_proto_breaking.py --base-ref HEAD~1 --bazel-flag=--disk_cache="${HOME}/.cache/bazel-disk"
```
The base revision must already define `//api:all_protos_descriptor_set`. It is built in a fresh Bazel output base, so the first run can take a few minutes; a shared `--disk_cache` speeds up repeated runs.

CI runs this check on pull requests; see [Protobuf Breaking Changes](#6-protobuf-breaking-changes-buf-breaking) for when it runs and how to override it.

---

## Custom AIP-160 Filter & Parser Engine

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

---

## Private Code Tree & Internal Packages

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
- All custom AIP-160 Query Generator unit tests listed under the Custom AIP-160 Filter & Parser Engine section.

### Running Spanner Database Health Unit Tests

Unit tests under `//internal/common/health:health_test` verify database query latency by connecting to a Cloud Spanner Emulator. If a background emulator is not running, these tests will timeout. To run a local Spanner Emulator in Docker:

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
   bazel test //internal/common/health:health_test
   ```

---

## Building & Testing Core Application Services

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

### UI Service
The **UI** is the frontend portal of XMC, hosting the Angular single-page application.

- **Build the UI OCI image tarball**:
  ```bash
  bazel build //cmd/ui:ui_image_tarball
  ```

- **Run the UI and server unit tests**:
  ```bash
  bazel test //cmd/ui/...
  ```


---

## Continuous Integration

[![XMC Unit Tests](https://github.com/google/xmc/actions/workflows/unit-tests.yml/badge.svg)](https://github.com/google/xmc/actions/workflows/unit-tests.yml)

Automated unit test suites and code quality checks are executed on:
* **Pull Requests** targeting any branch.
* **Direct Pushes** to the `test` (main baseline) and `feature/unit_tests_action` branches.
* **Manual Dispatch** (`workflow_dispatch`) via the GitHub Actions UI.

---

## Linting & Code Quality Rules

To maintain pristine architecture, canonical styling, and avoid common defects across the monorepo, code quality rules are enforced both locally via a pre-commit hook (`.githooks/pre-commit`) and in CI via GitHub Actions ([`.github/workflows/lint.yml`](../.github/workflows/lint.yml)).

The following linting rules and static analysis toolchains are implemented:

### 1. Python Code (`ruff`)
Python source files (`.py`) are formatted and linted according to PEP 8 using **`ruff`**:
* **`ruff check`**: Enforces PEP 8 rules, Pyflakes errors, and import sorting (`isort`).
* **`ruff format`**: Standardizes Python code layout and formatting across the project.

### 2. Go Code (`gofmt`)
Go source files (`.go`) are formatted using standard **`gofmt`**:
* Enforces canonical whitespace, indentation, and standard Go code formatting (`gofmt -s`).

### 3. UI Code (`prettier` & `eslint`)
Angular and TypeScript frontend code under `cmd/ui` is formatted and linted using **Prettier** and **ESLint**:
* **`prettier`**: Enforces consistent styling on `.ts`, `.html`, and `.scss` files (`npm run format:check`).
* **`eslint`**: Enforces Angular best practices, import sorting, and TypeScript strictness (`npm run lint`).

### 4. Bazel Build Files (`buildifier`)
Bazel build graphs (`BUILD`, `BUILD.bazel`, `WORKSPACE`, `MODULE.bazel`, and `.bzl` files) are formatted and analyzed using **`buildifier`**:
* Enforces canonical indentation, keyword ordering, and label formatting (`buildifier -mode=check`).

### 5. License Headers (`addlicense`)
Source files are checked for Google LLC Apache 2.0 license headers using **`addlicense`**:
* Enforces copyright headers on all managed source code files (`.go`, `.py`, `.ts`, `.html`, `.css`, `.proto`, `.tf`, `.sh`, Bazel files).
* Ignores data/schema files (`.json`), lockfiles (`go.mod`, `go.sum`, `*.lock`, `package.json`, `pyproject.toml`), and `third_party/` directories.

### 6. Protobuf Breaking Changes (`buf breaking`)
Pull requests are checked for backward-incompatible protobuf API changes with the [breaking-change check](#breaking-change-check-buf-breaking):
* Runs only in CI, not in the pre-commit hook, and only for `pull_request` events that change a `.proto` file, an `api/**/BUILD.bazel` file, `api/buf-breaking.yaml`, `tools/check_proto_breaking.py`, `.github/scripts/proto-breaking-override.js`, or `.github/workflows/lint.yml`. It is skipped for `merge_group` and `workflow_dispatch` runs.
* Compares the pull request merge commit against its first parent, the tip of the base branch, using the base branch's `api/buf-breaking.yaml`, and always lists the findings in the job summary.
* **Override**: if a break is intentional, someone other than the pull request author (typically a maintainer) adds the `proto-breaking-change-approved` label and then re-runs the job. The check then runs with `--allow-breaking`: the job summary still lists the breaking changes and names who applied the label, but they no longer fail the job; build errors and uncovered `proto_library` targets still do. The approval also covers later pushes until the label is removed. Only the label's most recent application counts: it is ignored if that was by the pull request author or by a bot. If the label lookup fails, the check stays blocking. Maintainers create this label in the repository when the check is rolled out.
* The override decision lives in [`.github/scripts/proto-breaking-override.js`](../.github/scripts/proto-breaking-override.js). CI does not run the `.github/scripts` unit tests yet; run them locally with `node --test .github/scripts/*.test.js`.

---

### Local Pre-Commit Hook Usage

Developers can run the pre-commit hook locally in two modes:

* **Auto-fix mode (default for git commits)**:
  ```bash
  ./.githooks/pre-commit
  ```
  Automatically formats files and adds missing headers.

* **Check-only mode (mirrors CI validation)**:
  ```bash
  ./.githooks/pre-commit --check
  ```
  Verifies all code formatting without altering files, returning non-zero if violations exist.

To enable the hook automatically for every `git commit`:
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
            //cmd/ui:ui_image_tarball \
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
  bazel run //cmd/ui:ui_image_push
  bazel run //cmd/xid_service:xid_service_image_push
  ```
