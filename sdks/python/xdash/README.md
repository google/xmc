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

### Authentication

When connecting to an IAP-protected XMC deployment, `DashboardClient` attaches an `authorization: Bearer <token>` gRPC metadata header using the following precedence:

1. **Static Bearer Token (`XMC_AUTH_TOKEN` or `AUTH_TOKEN`)**: Uses a pre-minted OIDC token directly from the environment.
2. **Automatic IAM OIDC Token Generation (`XMC_CLIENT_SA` + `IAP_CLIENT_ID`)**: When `XMC_AUTH_TOKEN` and `AUTH_TOKEN` are unset and both `XMC_CLIENT_SA` and `IAP_CLIENT_ID` are set in the environment, `DashboardClient` automatically mints an OIDC ID token via the IAM Credentials `generateIdToken` API (using Application Default Credentials) and caches it across RPC calls for 55 minutes.

### Building Custom Charts and Plots

Use the fluent `ChartBuilder`, `DashboardBuilder`, and plot helper functions (`summary_kpi_plot`,
`scatter_plot`, `bar_plot`):

```python
from xdash import (
    ChartBuilder,
    DashboardBuilder,
    DashboardClient,
    chart_id_from_name,
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

with DashboardClient() as client:
    # Phase 1: create the charts. CreateDashboard never creates charts, so they must
    # exist before anything can reference them.
    created_charts = client.create_charts([chart])

    # Phase 2: create the dashboard, referencing the IDs the server assigned.
    builder = DashboardBuilder(name="dashboards/exp_1001_dashboard").set_title(
        "Experiment 1001 Dashboard"
    )
    for created_chart in created_charts:
        builder.add_chart_id(chart_id_from_name(created_chart.name))

    created = client.create_dashboard(builder.build())
```

`create_chart` attaches a chart to a **WorkUnit** parent — `experiments/{xid}/workUnits/{wid}` —
which is the only parent the `CreateChart` RPC accepts. `create_charts` is the batch convenience
wrapper: it derives each parent from the chart's own `xid`/`wid`, so `for_experiment()` must have
been called on every chart it is given.

### Referencing Charts from a Dashboard

A `Dashboard` stores a list of bare chart IDs (`chart_ids`), not embedded `Chart` messages, and it
does not create the charts it points at. Two builder methods cover both spellings:

```python
builder.add_chart_id("f47ac10b-58cc-4372-a567-0e02b2c3d479")  # a bare ID
builder.add_chart(created_chart)  # a created Chart; the ID is taken from chart.name
```

Both reject blanks, IDs containing a `/` (a full resource name is not a chart ID — pass it through
`chart_id_from_name()` first), and IDs already referenced by the same dashboard. Use `add_chart()`
only with a chart the server has returned: a locally built chart whose `name` you chose yourself
would embed an ID that does not exist yet.

Two consequences of the ID-only model are worth knowing at call sites:

- `GetDashboard` returns `chart_ids` **sorted and de-duplicated**, so the order you added them in
  is not preserved. Drive layout from `chart_layout` positions, not from list order.
- A `GridPosition` for a chart must set `item_id` to the raw chart ID, matching an entry in
  `chart_ids`; a full resource name will not be matched to any chart.

### Creating a Dashboard from Presets

Use the built-in VCC comparison dashboard preset (featuring experiments `234911122`, `234911123`,
`234911124`):

```python
from xdash import (
    DashboardClient,
    chart_id_from_name,
    create_vcc_comparison_charts,
    create_vcc_comparison_dashboard,
)

with DashboardClient(endpoint="localhost:8080") as client:
    created_charts = client.create_charts(create_vcc_comparison_charts())
    dashboard = create_vcc_comparison_dashboard(
        dashboard_id="my_vcc_comparison",
        title="VCC Baseline vs Tuned Runs",
        chart_ids=[chart_id_from_name(c.name) for c in created_charts],
    )
    created_dashboard = client.create_dashboard(dashboard)
    print(f"Created: {created_dashboard.name}")
```

`create_vcc_comparison_dashboard()` defaults `chart_ids` to the preset slugs (`baseline`,
`tuned_v1`, `tuned_v2`), which is only correct if the charts were created under exactly those IDs.
Pass the IDs back from `create_charts()` whenever the server may assign them itself.

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
