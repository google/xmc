# `xdash`: Python SDK & CLI for XMC Metrics Dashboards

`xdash` is a Python SDK and command-line interface (CLI) for creating, retrieving, and inspecting XMC Metrics Dashboards and Charts. It provides fluent builder models, pre-configured test data presets, and a gRPC client to communicate with the XMC Dashboard Service.

## Installation / Usage in Bazel

To depend on `xdash` in Bazel Python rules, add:
```python
deps = [
    "//sdks/python/xdash",
]
```

## Python SDK Examples

### 1. Creating a Dashboard from Presets
Use the built-in VCC comparison dashboard preset (featuring experiments `234911122`, `234911123`, `234911124`):

```python
from xdash import DashboardClient, create_vcc_comparison_dashboard

client = DashboardClient(endpoint="localhost:8080")
dashboard = create_vcc_comparison_dashboard(
    dashboard_id="my_vcc_comparison",
    title="VCC Baseline vs Tuned Runs"
)

created_dashboard = client.create_dashboard(dashboard)
print(f"Created: {created_dashboard.name}")
```

### 2. Building Custom Charts and Plots
Use the fluent `ChartBuilder`, `DashboardBuilder`, and plot helper functions (`summary_kpi_plot`, `scatter_plot`, `bar_plot`):

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

client = DashboardClient()
created = client.create_dashboard(dashboard)
```

## CLI Usage (`xdash`)

You can run `xdash` directly via Bazel or as a Python module:

```bash
# Create the standard VCC comparison dashboard preset
bazel run //sdks/python/xdash:xdash_bin -- create-preset --dashboard-id vcc_comparison

# Create a dashboard with standard comparison charts for specific experiment IDs
bazel run //sdks/python/xdash:xdash_bin -- create-dashboard \
    --title "Hyperparameter Sweep" \
    --dashboard-id "hp_sweep" \
    --xids "234911122,234911123,234911124"

# Retrieve an existing dashboard
bazel run //sdks/python/xdash:xdash_bin -- get dashboards/vcc_comparison

# Fetch evaluated ChartData for a specific chart
bazel run //sdks/python/xdash:xdash_bin -- get-data experiments/234911122/workUnits/1/charts/baseline
```
