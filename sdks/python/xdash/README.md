# `xdash`: Python SDK & CLI for XMC Metrics Dashboards

`xdash` is a Python SDK and command-line interface (CLI) for creating, retrieving, and inspecting XMC Metrics Dashboards and Charts. It provides fluent builder models, pre-configured test data presets, and a gRPC client to communicate with the XMC Dashboard Service.

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

### Building Custom Charts and Plots

Use the fluent `ChartBuilder`, `DashboardBuilder`, and plot helper functions (`summary_kpi_plot`,
`scatter_plot`, `bar_plot`):

```python
from xdash import (
    ChartBuilder,
    DashboardBuilder,
    DashboardClient,
    scatter_plot,
    summary_kpi_plot,
)

# Build a chart for Experiment 1001, Work Unit 1
chart = (
    ChartBuilder(name="experiments/1001/workUnits/1/charts/val_metrics")
    .for_experiment(xid=1001, wid=1)
    .set_title("Validation Overview")
    .set_grid_layout(columns=2)
    .add_plot(summary_kpi_plot("val_kpi", "Validation Loss KPI", ["val/loss"]))
    .add_plot(scatter_plot("val_curve", "Validation Loss Curve", ["val/loss"]))
    .build()
)

# Attach to a new Dashboard
dashboard = (
    DashboardBuilder(name="dashboards/exp_1001_dashboard")
    .set_title("Experiment 1001 Dashboard")
    .add_chart(chart)
    .build()
)

with DashboardClient() as client:
    created = client.create_dashboard(dashboard)
```

`create_chart` attaches a chart to a **WorkUnit** parent — `experiments/{xid}/workUnits/{wid}` —
which is the only parent the `CreateChart` RPC accepts.

### Creating a Dashboard from Presets

Use the built-in VCC comparison dashboard preset (featuring experiments `234911122`, `234911123`,
`234911124`):

```python
from xdash import DashboardClient, create_vcc_comparison_dashboard

dashboard = create_vcc_comparison_dashboard(
    dashboard_id="my_vcc_comparison", title="VCC Baseline vs Tuned Runs"
)

with DashboardClient(endpoint="localhost:8080") as client:
    created_dashboard = client.create_dashboard(dashboard)
    print(f"Created: {created_dashboard.name}")
```

A runnable end-to-end version of this, including a virtualenv installer, lives in
[`examples/xdash_dashboard/`](../../../examples/xdash_dashboard/).

## CLI Usage (`xdash`)

You can run `xdash` directly via Bazel:

```bash
# Create the standard VCC comparison dashboard preset
bazel run //sdks/python/xdash:xdash_bin -- create-preset --dashboard-id vcc_comparison

# Create a dashboard with standard comparison charts for specific experiment IDs
bazel run //sdks/python/xdash:xdash_bin -- create-dashboard \
    --title "Hyperparameter Sweep" \
    --dashboard-id "hp_sweep" \
    --xids "234911122,234911123,234911124"

# Create a chart under an experiment work unit. --xid and --wid default to the
# IDs in --parent, and are rejected if given and disagreeing with it.
bazel run //sdks/python/xdash:xdash_bin -- create-chart \
    --parent "experiments/234911122/workUnits/1" \
    --title "Validation Overview" \
    --metrics "val/loss,train/accuracy"

# List every dashboard known to the service
bazel run //sdks/python/xdash:xdash_bin -- list

# Retrieve an existing dashboard
bazel run //sdks/python/xdash:xdash_bin -- get dashboards/vcc_comparison

# Retrieve a single chart (any of the three chart resource patterns works)
bazel run //sdks/python/xdash:xdash_bin -- get experiments/234911122/workUnits/1/charts/baseline

# Fetch evaluated ChartData for a specific chart
bazel run //sdks/python/xdash:xdash_bin -- get-data experiments/234911122/workUnits/1/charts/baseline
```

Or as a Python module, once the generated protos are on `PYTHONPATH` (see
[Installation / Usage in Bazel](#installation--usage-in-bazel) above):

```bash
python3 -m xdash list
python3 -m xdash get dashboards/vcc_comparison
python3 -m xdash --endpoint cloud.xmanager.googleapis.com:443 list
python3 -m xdash --insecure --endpoint dev-box.corp:8080 list
```
