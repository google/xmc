# OSS Vizier Go/gRPC Proto Stubs

This package generates Go Protobuf and gRPC v2 client stubs
(`xmanager_cloud/pkg/study/vizierpb`) for `vizier.VizierService` directly from
the upstream `@oss_vizier` `http_archive` declared in
[MODULE.bazel](../../../MODULE.bazel) (Apache-2.0).

## Pinned Version

- **Upstream Repository**: <https://github.com/google/vizier>
- **Pinned Version**: `google-vizier==0.1.24` (tag `v0.1.24`)
- **Pinned Commit**: `7ce0ae865a7d47dc9549757d32747c0df6746b81`

The `@oss_vizier` archive **must stay in lockstep** with
[test/e2e/testdata/vizier/requirements.txt](../../../test/e2e/testdata/vizier/requirements.txt),
which pins `google-vizier==0.1.24`.

## Updating

1. Bump `google-vizier` in
   [test/e2e/testdata/vizier/requirements.txt](../../../test/e2e/testdata/vizier/requirements.txt)
   and resolve the matching `vX.Y.Z` tag commit SHA in `google/vizier`.
2. Update the `urls`, `strip_prefix`, and `sha256` of `http_archive(name = "oss_vizier", ...)`
   in [MODULE.bazel](../../../MODULE.bazel) (and update this `README.md`).
3. Run `bazel test //pkg/study/vizierpb/... //tools/lint/...` to verify proto
   compilation, gRPC stub generation, and lint compliance.
