# `xmanager-cloud`: XManager on Cloud Python Package

> [!IMPORTANT]
> This package is under construction and is **not published to PyPI yet**. Today it ships the
> XMC gRPC client protos, the bundled `xdash` SDK, and the `xmc login` / `xmc logout` commands.
> The `xm_cloud` launcher, the remaining `xmc` commands, and the `xmanager` dependency arrive in
> later changes. Until then, set up launch environments with
> [`deploy/cli/install_xmanager.py`](../../../deploy/cli/install_xmanager.py) as described in
> [CLI Setup](../../../deploy/cli/README.md).

## Contents

| Top-level module | Source | Contents |
| :--- | :--- | :--- |
| `xmanager_cloud` | [`src/xmanager_cloud/`](src/xmanager_cloud/) plus protos compiled at build time | `experiment_state_server.proto`, `xid_service.proto`, and `dashboard_service.proto` generated `*_pb2` / `*_pb2_grpc` modules |
| `xm_cloud` | [`src/xm_cloud/`](src/xm_cloud/) | The `xmc` console script ([Command-line interface](#command-line-interface)) |
| `xdash` | [`sdks/python/xdash/`](../xdash/), copied at build time | Dashboard SDK and the `xdash` console script ([README](../xdash/README.md)) |

The generated modules keep the import paths that `install_xmanager.py` and the examples use
today, for example `xmanager_cloud.experiment_state_server.proto.api_pb2`.

## Command-line interface

The `xmc` console script needs only the standard library, so it works without `xmanager`:

| Command | Purpose |
| :--- | :--- |
| `xmc login [--no-browser] [--timeout SECONDS] [--status \| --print-token]` | Signs in to an XMC deployment (XMC `>= 0.16.0`) with a browser-based PKCE flow and caches the token |
| `xmc logout [--all]` | Removes the cached token for the current deployment, or for every deployment with `--all` |

Both commands find the deployment from `XMANAGER_ENDPOINT`, `XMC_API_URL`, `XMANAGER_UI_URL`, or
`XMC_DOMAIN`, and use the credentials file `$XMC_CREDENTIALS_FILE` or
`$XDG_CONFIG_HOME/xmc/credentials.json` (default `~/.config/xmc/credentials.json`), the same
file that `xmanager login` writes. Tokens are keyed by deployment origin, normalized the way
`xdash` and the examples look them up (for example, `grpc.api.<domain>` and `https://<domain>:443`
both map to `https://<domain>`). `xmanager login` in `xmanager` 0.8.x skips some of these
normalizations, so for such inputs a token cached by one CLI may not be found by the other.
Run `xmc <command> --help` for details, and see
[Authentication](../../../docs/authentication.md) for how the XMC backend issues CLI tokens.

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

## Compatibility with legacy `xmanager`

`xmanager` releases before `0.9.0rc1` (notably `0.8.5.post1`) can bundle their own
`xmanager_cloud` protos and `xdash`.
Loading both copies registers the `cloud.xmanager.v1` descriptors twice and crashes the process,
so importing `xmanager_cloud` with such a release installed raises an `ImportError` (provided
`xmanager-cloud` was installed last; a legacy `xmanager` installed afterwards overwrites the
check). Both
distributions own the same files, so upgrading `xmanager` deletes them; reinstall
`xmanager-cloud` afterwards:

```bash
pip install -U "xmanager>=0.9.0"  # add --pre while only a 0.9.0 release candidate exists
pip install --force-reinstall --no-deps xmanager-cloud
```

Environments without `xmanager` are unaffected.

## Testing

- `bazel test //sdks/python/xmanager-cloud/...` covers the build staging and smoke-check
  tooling, and the `xm_cloud.cli` unit tests.
- [`xmanager-cloud-package.yml`](../../../.github/workflows/xmanager-cloud-package.yml) builds the
  wheel and sdist, installs each into clean virtualenvs on Python 3.11–3.14 against the
  protobuf floor (`5.29.6`) and the latest `<7` release, then runs
  [`install_smoke_check.py`](install_smoke_check.py), `xmc --help`, and the `xmanager_cloud` and
  `xm_cloud.cli` unit tests against the installed package. The `xm_cloud.cli` tests use `absl-py`,
  which the workflow installs for testing only. The sdist is not installed on Python 3.14 (see
  [Building](#building)).
