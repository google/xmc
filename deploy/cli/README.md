# XManager CLI

## Introduction

[XManager CLI](https://github.com/google-deepmind/xmanager) is a Python library for interacting with XManager on Cloud (`xm_cloud`) and other XManager execution backends.

---

## Canonical Installation (PyPI)

For users, workloads, and examples interacting with XMC, the standard and recommended installation method is directly from PyPI via **`pip`**:

```bash
python3 -m venv .venv
source .venv/bin/activate
pip install --upgrade pip
pip install xmanager

# To pin or override a specific version:
# pip install "xmanager==0.8.2"
```

The published `xmanager` package bundles `xmanager.xm_cloud` and the precompiled gRPC proto bindings (`xmanager_cloud.experiment_state_server.proto.*`, `xmanager_cloud.xid_service.proto.*`, and `xmanager_cloud.dashboard_service.proto.*`).

---

## Developer Utility: Building from Source (`install_xmanager.py`)

The script `install_xmanager.py` is a **developer and CI utility** used when developing against unreleased local proto changes (`api/protos`) before they have been published to PyPI.

It automates:
1. Creating a Python virtual environment.
2. Cloning the official `xmanager` repository (`xm-cloud` branch) and `googleapis` repository.
3. Compiling the fresh XMC API protos from `api/protos` into the local `xmanager` source tree.
4. Installing the package into the virtual environment in editable mode.

### Prerequisites for Building from Source
- **Git** (version 2.25+ recommended for remote/GitHub sparse checkouts)
- **Python 3**

### Basic Usage

From the root of your local clone of the `xmc` repository:
```bash
python3 deploy/cli/install_xmanager.py
```
This automatically locates your local `api/protos` definitions and compiles them inside `./venv`.

### Options

The script accepts several command-line flags to customize the installation:

```bash
python3 deploy/cli/install_xmanager.py [options]

Options:
  --protos-loc <url_or_path>     Location of XMC protos. Can be a local directory path or a 
                                 GitHub repository tree URL.
                                 (default: local checkout if available, else https://github.com/google/xmc/tree/main/api/protos)
  --xmanager-branch <branch>     The XManager Git branch/tag to clone (default: xm-cloud)
  --venv <path>                  Target directory for the Python virtual environment (default: ./venv)
  --dashboard-protos-only        Only build and install the Bazel Dashboard Service protobuf modules
  -h, --help                     Show usage information
```

#### Example: Installing with Remote Protos
```bash
python3 deploy/cli/install_xmanager.py --venv ./my_env --protos-loc https://github.com/google/xmc/tree/main/api/protos
```

### Verification
You can verify the installation is working correctly by checking imports:
```bash
python3 -c "import xmanager.xm_cloud; import xmanager_cloud.experiment_state_server.experiment_state_api; print('Verified!')"
```

---

## Launching Experiments

The directory includes a toy experiment launcher `launch.py` to test and interact with XManager on Cloud (XMC).

### 1. Configure the Environment Variables
Before running the launcher, create a `.env` file in the root of the `xmc` repository by copying the example template:

```bash
cp ../../.env.example ../../.env
```

Open the newly created `../../.env` file and customize it with your specific details (such as your `PROJECT_ID`, `IAP_CLIENT_ID`, and domain endpoints).

### 2. Run the Launcher
With the Python virtual environment active and the `.env` file configured, execute the `launch.py` script:

```bash
python3 launch.py
```

`launch.py` automatically uses `python-dotenv` to locate the `.env` file in parent directories, load the variables, and start the experiment without needing manual prompt input.
