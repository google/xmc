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
