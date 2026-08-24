# `xdash`: Python SDK & CLI for XMC Metrics Dashboards

`xdash` is a Python SDK and command-line interface (CLI) for creating, retrieving, and inspecting XMC Metrics Dashboards and Charts. It provides a gRPC client to communicate with the XMC Dashboard Service.

## Installation / Usage in Bazel

To depend on `xdash` in Bazel Python rules, add:

```python
deps = [
    "//sdks/python/xdash",
]
```

Bazel is currently the only supported way to consume `xdash`. The package imports its generated
protobuf modules through `xdash/_protos.py`, and those modules are produced by the
`//api/protos/dashboard` `py_proto_library` targets at build time rather than checked in — so a
plain `pip install` of this directory would import but fail at first use. Outside Bazel, build
those targets and put their output on `PYTHONPATH` yourself.

## Python SDK Examples

### Reading Dashboards and Charts

`DashboardClient` wraps the DashboardService read RPCs. It owns its gRPC channel, so use it as a
context manager to release the connection deterministically:

```python
from xdash import DashboardClient

with DashboardClient(endpoint="localhost:8080") as client:
    for dashboard in client.list_dashboards():
        print(f"{dashboard.name}: {dashboard.title}")

    dashboard = client.get_dashboard("dashboards/vcc_comparison")
    chart = client.get_chart("experiments/234911122/workUnits/1/charts/baseline")
    print(f"{chart.title} has {len(chart.plots)} plots")
```

The endpoint defaults to the `XMC_DASHBOARD_ENDPOINT` environment variable, falling back to
`localhost:8080`.

### Transport Security

The channel is TLS by default for every endpoint except a local one (`localhost`, `127.0.0.1`,
`::1`, `0.0.0.0`), because the hosted service rejects plaintext. Pass `secure=` to override that
default, or `credentials=` to supply your own `grpc.ChannelCredentials`:

```python
DashboardClient(endpoint="cloud.xmanager.googleapis.com:443")  # TLS
DashboardClient(endpoint="dev-box.corp:8080", secure=False)  # plaintext
DashboardClient(endpoint="mtls.example.com:443", credentials=my_creds)  # custom credentials
```

The CLI exposes the same override as `--insecure`.

## CLI Usage (`xdash`)

You can run `xdash` directly via Bazel:

```bash
# List every dashboard known to the service
bazel run //sdks/python/xdash:xdash_bin -- list

# Retrieve an existing dashboard
bazel run //sdks/python/xdash:xdash_bin -- get dashboards/vcc_comparison

# Retrieve a single chart (any of the three chart resource patterns works)
bazel run //sdks/python/xdash:xdash_bin -- get experiments/234911122/workUnits/1/charts/baseline
```

Or as a Python module, once the generated protos are on `PYTHONPATH` (see
[Installation / Usage in Bazel](#installation--usage-in-bazel) above):

```bash
python3 -m xdash list
python3 -m xdash get dashboards/vcc_comparison
python3 -m xdash --endpoint cloud.xmanager.googleapis.com:443 list
python3 -m xdash --insecure --endpoint dev-box.corp:8080 list
```
