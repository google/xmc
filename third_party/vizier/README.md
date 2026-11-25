# OSS Vizier Service Protos

This directory vendors the minimal set of Open Source Vizier (OSS Vizier)
protobuf definitions required to generate Go client and gRPC stubs for the
`vizier.VizierService` API.

## Upstream Source & Pinned Version

- **Upstream Repository**: <https://github.com/google/vizier>
- **Pinned Version**: `google-vizier==0.1.24` (tag `v0.1.24`)
- **Pinned Commit**: `7ce0ae865a7d47dc9549757d32747c0df6746b81`
- **License**: Apache-2.0 (see [LICENSE](LICENSE))

The pinned version here **must stay synchronized** with
[test/e2e/testdata/vizier/requirements.txt](../../test/e2e/testdata/vizier/requirements.txt),
which pins `google-vizier==0.1.24`.

## Included Files

Only the three `.proto` files needed for `VizierService` and `Study` are
vendored:

- `key_value.proto` (from `vizier/_src/service/key_value.proto`)
- `study.proto` (from `vizier/_src/service/study.proto`)
- `vizier_service.proto` (from `vizier/_src/service/vizier_service.proto`)
- `LICENSE` (from `LICENSE`)

`pythia_service.proto` and `vizier_oss.proto` are intentionally excluded
because neither is imported by `vizier_service.proto` or `study.proto`.

## Local Modifications

The `.proto` declarations, packages, imports, messages, enums, and services are
verbatim from upstream. The only local addition is the standard 14-line
`// Copyright 2024 Google LLC` Apache-2.0 comment header prepended to each
`.proto` file so the repository's `addlicense -check` pre-commit and CI gate
passes. Do not add `option go_package` lines to the `.proto` files; Bazel
configures the Go import path (`xmanager_cloud/third_party/vizier`) via
`go_proto_library` in [BUILD.bazel](BUILD.bazel).

## Updating

1. Verify the target `google-vizier` version in
   [test/e2e/testdata/vizier/requirements.txt](../../test/e2e/testdata/vizier/requirements.txt)
   and resolve its corresponding `vX.Y.Z` tag commit SHA in `google/vizier`.
2. Re-fetch `LICENSE`, `vizier/_src/service/key_value.proto`,
   `vizier/_src/service/study.proto`, and
   `vizier/_src/service/vizier_service.proto` at that commit SHA.
3. Prepend the 14-line Apache-2.0 license header to each `.proto` file.
4. Update the pinned version and commit SHA in this `README.md`.
5. Run `bazel test //third_party/vizier/... //tools/lint/...` and pre-commit
   checks to verify compilation and lint compliance.
