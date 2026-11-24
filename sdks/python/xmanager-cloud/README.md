# `xmanager-cloud`: XManager on Cloud Python Package

> [!IMPORTANT]
> This package is under construction and is **not published to PyPI yet**. Today it ships the
> XMC gRPC client protos and the bundled `xdash` SDK only. The `xm_cloud` launcher, the `xmc` CLI,
> and the `xmanager` dependency arrive in later changes. Until then, set up launch environments with
> [`deploy/cli/install_xmanager.py`](../../../deploy/cli/install_xmanager.py) as described in
> [CLI Setup](../../../deploy/cli/README.md).

## Contents

| Top-level module | Source | Contents |
| :--- | :--- | :--- |
| `xmanager_cloud` | [`src/xmanager_cloud/`](src/xmanager_cloud/) plus protos compiled at build time | `experiment_state_server.proto`, `xid_service.proto`, and `dashboard_service.proto` generated `*_pb2` / `*_pb2_grpc` modules |
| `xdash` | [`sdks/python/xdash/`](../xdash/), copied at build time | Dashboard SDK and the `xdash` console script ([README](../xdash/README.md)) |

The generated modules keep the import paths that `install_xmanager.py` and the examples use
today, for example `xmanager_cloud.experiment_state_server.proto.api_pb2`.

## Building

```bash
python3 -m pip install build
python3 -m build --outdir sdks/python/xmanager-cloud/dist sdks/python/xmanager-cloud
```

The build compiles
[`api/protos/{experiment,xid,dashboard}`](../../../api/protos/) with `grpcio-tools==1.70.0` and
`protobuf==5.29.6`, pinned in `[build-system] requires`. Generated code refuses to load on a
protobuf runtime older than the `protoc` that produced it, so building at the floor keeps the
package usable on every runtime in the supported `protobuf>=5.29.6,<7.0.0` range.

The sdist is self-contained: [`setup.py`](setup.py) copies the `.proto` files and the `xdash`
sources into a `_staged/` directory, so it builds outside this repository. Build it from source
explicitly with:

```bash
pip install --no-binary xmanager-cloud sdks/python/xmanager-cloud/dist/xmanager_cloud-*.tar.gz
```

`grpcio-tools==1.70.0` publishes no Python 3.14 wheel, so building from the sdist on Python 3.14
compiles `grpcio-tools` from source. The wheel is pure Python and installs on every supported
version.

## Testing

- `bazel test //sdks/python/xmanager-cloud/...` covers the build staging logic.
- [`xmanager-cloud-package.yml`](../../../.github/workflows/xmanager-cloud-package.yml) builds the
  sdist and then the wheel from the unpacked sdist, which runs the staging and proto compile
  hooks outside the repository tree.
