# Example: Creating Dashboards & Charts with `xdash`

This example demonstrates how to use the **`xdash`** Python SDK to programmatically construct XMC Performance Dashboards and Charts comparing multiple experiment runs.

## Files

- **`create_dashboard.py`**: Python script showing how to:
  - Build custom 4-plot experiment charts (`val/loss` and `train/accuracy` KPI & scatter plots).
  - Create those charts first, then assemble a dashboard that references them by ID using
    `DashboardBuilder` — `CreateDashboard` stores chart IDs and never creates the charts itself.
  - Connect to the XMC Dashboard Service via `DashboardClient`.
  - Inspect evaluated chart metrics data from the DataEngine.
- **`install_venv.sh`**: Shell script that compiles the required Python protobuf modules via Bazel, creates a local Python virtual environment (`.venv`), installs runtime dependencies (`grpcio`, `protobuf`), and installs `xdash` in editable mode.
- **`requirements.txt`**: Python dependencies list.

## 1. Installation & Dependencies

### Option A: Via Automated Setup Script (Recommended)

Run the installation script from this directory to compile Bazel protobuf bindings, create `.venv`, and install dependencies:

```bash
./install_venv.sh
```

### Option B: Manual Virtual Environment & Dependencies

If managing an existing virtual environment:

```bash
pip install -r requirements.txt
```

## 2. Running the Example

Activate the virtual environment and execute the script:

```bash
source .venv/bin/activate
python3 create_dashboard.py
```

### Customizing the Endpoint or Dashboard ID
You can customize the target Dashboard Service endpoint, dashboard ID, or title:

```bash
python3 create_dashboard.py \
    --endpoint "localhost:8080" \
    --dashboard-id "custom_vcc_comparison" \
    --title "My Custom VCC Sweep Dashboard"
```
