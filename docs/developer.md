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

All proto files are located inside the [api/protos](../api/protos) directory. They share the `cloud.xmanager.v1` package name and are built with **Bazel** (`rules_proto` and `rules_go`).

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

### Breaking-Change Check (`buf_breaking_test`)

[`//api:protos_breaking_test`](../api/BUILD.bazel) runs with `bazel test` and fails on backward-incompatible changes to the first-party protobuf APIs:
* It compares the `proto_library` targets listed in `_PROTOS` with the committed baseline [`api/testdata/protos_baseline.binpb`](../api/testdata/protos_baseline.binpb), using [rules_buf](https://buf.build/docs/cli/build-systems/bazel/) and the strict `FILE` rules from [`api/buf.yaml`](../api/buf.yaml). Bundled `google/*` files are ignored; unstable packages such as `components/v1alpha1` are checked because their messages are persisted.
* A new `proto_library` under `//api` must be added to `_PROTOS`; CI fails, listing the uncovered files, until it is. `api/filter/filter_expression.proto` has no `proto_library` and is not covered.

To accept an intentional breaking change, refresh the baseline in the same pull request:
```bash
bazel build //api:all_protos_descriptor_set && install -m 0644 \
  bazel-bin/api/all_protos_descriptor_set.pb api/testdata/protos_baseline.binpb
```

Refreshing the baseline, or changing `api/buf.yaml` or `api/BUILD.bazel`, needs a fresh human approval: these are protected files for [incremental PR approval](../.github/scripts/README.md), so a pull request that changes them is never auto-reapproved.

---

## Custom AIP-160 Filter & Parser Engine

The repository includes a custom, zero-dependency AIP-160 compliant filter parsing and query translation engine under `//api/filter/go/filter` and `//api/filter/go/queryfilterbase`. This parser replaces legacy dependencies with standard Go stubs and includes custom priority logical operator parsing (implicit space-conjunction, explicit `AND`, explicit `OR`), type safety verification, regular expression constraints, and disjunction IN list optimizations. SQL syntax that differs by backend is isolated behind `queryfilterbase.Dialect`, implemented in `//internal/query_generator/dialect/spanner` and `//internal/query_generator/dialect/postgres`.

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
- `//internal/query_generator/filter:filter_test` (database-agnostic filter visitor with Spanner and PostgreSQL dialect coverage, type safety integration)
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

## Running Affected Unit Tests

`.githooks/pre-commit` automatically runs [`tools/run_affected_tests.py`](../tools/run_affected_tests.py) to execute the Bazel unit tests affected by your changes (scoped via [`resolve_test_scope.py`](../tools/benchmark/resolve_test_scope.py) to match CI). To run it standalone or inspect the resolved scope (`--dry-run`), run `python3 ./tools/run_affected_tests.py --help`.

To test one directory, run `bazel test //<dir>/...`. To list its test targets, run `bazel query 'tests(//<dir>/...)'`. Go code imports Bazel-generated protobuf packages, so host `go build`, `go vet`, and `go test` do not work. The UI suite (`bazel test //cmd/ui/...`, `npm test`, `npm run lint`) needs `npm ci --legacy-peer-deps` in `cmd/ui` first in every fresh checkout or worktree.

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

### 6. Agent Doc Path References (`check_agent_doc_paths.py`)
Agent skills and rules (`.agents/**/*.md`, `AGENTS.md`) are checked by [`tools/lint/check_agent_doc_paths.py`](../tools/lint/check_agent_doc_paths.py):
* Fails when a repo path inside a code span or fenced block is neither a tracked file nor a tracked directory, so agents never copy a stale path into a command.
* Placeholders (`<name>`, `*`, `...`) only require their directory prefix to exist; runtime-created or illustrative paths are listed in the script's `ALLOWLIST` with a reason, and entries no doc references fail the check.
* The pre-commit hook runs it when agent docs are staged or files are deleted/renamed (always in `--check` mode); CI always runs it.

---

### Local Pre-Commit Hook Usage

Developers can run the pre-commit hook locally in two modes:

* **Auto-fix mode (default for git commits)**:
  ```bash
  ./.githooks/pre-commit
  ```
  Automatically formats files and adds missing headers. Formatter fixes to files that were fully staged before the hook ran are restaged automatically ([`tools/lint/precommit_autostage.py`](../tools/lint/precommit_autostage.py)); fixes to partially staged, unstaged, or unrelated files fail the hook so you can review and stage them. Set `XMC_PRECOMMIT_NO_AUTOSTAGE=1` to disable auto-restaging. Affected Bazel unit tests are skipped when every staged path is Markdown or under `docs/`. Per-file linters and formatters (Ruff, Prettier, ESLint, `terraform fmt`, `addlicense`) only process staged files. Cross-file checks (mypy, TFLint, `helm lint`, Trivy, agent doc paths) also run when a staged delete or rename removes a file they cover. When mypy runs, it checks the same repo-wide file list as CI, so edits that break unchanged importers fail locally.

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
