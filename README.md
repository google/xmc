# Standalone Build Setup

This repository has been migrated from `google3` to standalone Git (supporting Git-on-Borg and GitHub) using a standalone build architecture. All legacy Google dependencies and google3 tools have been decoupled.

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

The repository includes a custom, zero-dependency AIP-160 compliant filter parsing and query translation engine under `//api/filter/go/filter` and `//api/filter/go/queryfilterbase`. This parser replaces legacy `google3` dependencies with standard Go stubs and includes custom priority logical operator parsing (implicit space-conjunction, explicit `AND`, explicit `OR`), type safety verification, regular expression constraints, and disjunction IN list optimizations.

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

