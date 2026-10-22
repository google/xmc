# XManager CLI

## Introduction

[XManager CLI](https://github.com/google-deepmind/xmanager) is a Python library for interacting with XManager on Cloud (`xm_cloud`) and other XManager execution backends.

The published `xmanager` package on PyPI (`xmanager>=0.8.2`) bundles `xmanager.xm_cloud` and the precompiled gRPC proto bindings (`xmanager_cloud.experiment_state_server.proto.*`, `xmanager_cloud.xid_service.proto.*`, and `xmanager_cloud.dashboard_service.proto.*`).

---

## Installation

### Option 1: Direct `pip` Installation

Create and activate a Python virtual environment, then install `xmanager` from PyPI:

```bash
python3 -m venv .venv
source .venv/bin/activate
pip install --upgrade pip
pip install "xmanager>=0.8.2"

# To pin a specific version:
# pip install "xmanager==0.8.2"
```

### Option 2: Automated Setup Script (`install_xmanager.py`)

You can also use [`install_xmanager.py`](install_xmanager.py) to set up a virtual environment and install `xmanager` along with the XMC runtime client dependencies (`absl-py`, `google-auth`, `googleapis-common-protos`, `grpcio`, `protobuf`, `python-dotenv`, `requests`).

It automates:
1. Creating a Python virtual environment (default: `./venv`).
2. Upgrading `pip` and installing `xmanager` and XMC client dependencies from PyPI.
3. Verifying that `xmanager.xm_cloud` and the bundled XMC gRPC proto modules import cleanly.

#### Prerequisites
- **Python 3** (with `venv` and `pip`)

#### Basic Usage

From the root of your local clone of the `xmc` repository:
```bash
python3 deploy/cli/install_xmanager.py
```

#### Options

```bash
python3 deploy/cli/install_xmanager.py [options]

Options:
  --venv <path>                  Target directory for the Python virtual environment
                                 (default: ./venv, or VENV_DIR environment variable)
  --xmanager-version <version>   Optional XManager PyPI version tag to pin (e.g. 0.8.2)
                                 (default: XMANAGER_VERSION env var, or xmanager>=0.8.2)
  -h, --help                     Show usage information
```

#### Example: Pinning a Specific Version
```bash
python3 deploy/cli/install_xmanager.py --venv ./my_env --xmanager-version 0.8.2
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
