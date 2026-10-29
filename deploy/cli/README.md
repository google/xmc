# XManager CLI

## Introduction

[XManager CLI](https://github.com/google-deepmind/xmanager) is a Python library for interacting with XManager on Cloud (`xm_cloud`) and other XManager execution backends.

The published `xmanager` package on PyPI (`xmanager>=0.8.5.post1`) bundles `xmanager.xm_cloud` and the precompiled gRPC proto bindings (`xmanager_cloud.experiment_state_server.proto.*`, `xmanager_cloud.xid_service.proto.*`, and `xmanager_cloud.dashboard_service.proto.*`).

---

## Installation

### Recommended: Automated Setup & Local Proto Compilation (`install_xmanager.py`)

Use [`install_xmanager.py`](install_xmanager.py) to set up a virtual environment, install optional `requirements.txt` files, install `xmanager` and XMC runtime client dependencies (`absl-py`, `google-auth`, `googleapis-common-protos`, `grpcio`, `grpcio-tools`, `protobuf`, `python-dotenv`, `requests`) from PyPI, compile the fresh local XMC `.proto` definitions from [`api/protos`](../../api/protos) (`experiment`, `xid`, and `dashboard`) into the virtual environment's `xmanager_cloud` package, and install local `sdks/python/xdash` in editable mode after purging obsolete wheel-bundled `xdash` copies.

> [!NOTE]
> Because `install_xmanager.py` overwrites the `*_pb2.py` and `*_pb2_grpc.py` modules shipped by the `xmanager` wheel inside `site-packages/xmanager_cloud`, you must re-run `install_xmanager.py` after any `pip` operation (such as `pip install -U xmanager` or `--force-reinstall`) that modifies `xmanager`. It also purges any stale wheel-bundled `site-packages/xdash` copy so that standard import resolution loads the repository's local `xdash` SDK rather than outdated wheel artifacts.

It automates:
1. Creating a Python virtual environment (default: `./venv`).
2. Upgrading `pip`, installing any `-r` / `--requirements` files, and installing `xmanager` and XMC client dependencies from PyPI.
3. Compiling the local XMC API protos from `api/protos` (`experiment`, `xid`, and `dashboard`) directly into `site-packages/xmanager_cloud` via `grpc_tools.protoc` so local proto changes take effect immediately without waiting for a PyPI release.
4. Purging the obsolete wheel-bundled `site-packages/xdash` package and installing local repository `sdks/python/xdash` in editable mode (`pip install --no-deps -e`).
5. Verifying that `xmanager.xm_cloud`, `xdash`, `xdash.auth`, and the compiled XMC gRPC proto modules import cleanly.

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
  --xmanager-version <spec>      Optional xmanager version (e.g. 0.8.5.post1), specifier
                                 (e.g. '>=0.8.5.post1,<0.9'), or full PEP 508 requirement
                                 (e.g. 'xmanager @ git+https://github.com/google-deepmind/xmanager@main')
                                 (default: XMANAGER_VERSION env var, or xmanager>=0.8.5.post1)
  -r, --requirements <path>      Optional requirements file to install before applying
                                 xmanager and runtime dependency pins (repeatable)
  -h, --help                     Show usage information
```

#### Example: Pinning a Specific Version or Requirements File
```bash
python3 deploy/cli/install_xmanager.py --venv ./my_env --xmanager-version 0.8.5.post1 --requirements requirements.txt
```

### Direct `pip` Installation (Without Local Proto Overlay)

If you do not need unreleased local `.proto` changes from `api/protos`, you can also install `xmanager` directly from PyPI:

```bash
python3 -m venv .venv
source .venv/bin/activate
pip install --upgrade pip
pip install "xmanager>=0.8.5.post1"
```

### Verification
You can verify the installation is working correctly by checking imports:
```bash
python3 -c "import xmanager.xm_cloud; import xmanager_cloud.experiment_state_server.experiment_state_api; import xdash; import xdash.auth; print('Verified!')"
```

---

## Authentication

Interactive users should authenticate as themselves with `xmanager login` ([google/xmc#1309](https://github.com/google/xmc/issues/1309), requires XMC `>= 0.16.0`). This replaces the shared `XMC_CLIENT_SA` service account:

```bash
xmanager login   # Reads XMANAGER_ENDPOINT, XMC_API_URL, XMANAGER_UI_URL, or XMC_DOMAIN from the environment.
```

`xmanager login` caches your token in `$XMC_CREDENTIALS_FILE` or `$XDG_CONFIG_HOME/xmc/credentials.json` (default `~/.config/xmc/credentials.json`), and the XManager CLI loads it automatically as a Bearer token and reads your email from it. Over SSH or on a headless machine, `xmanager login` falls back to a copy-and-paste flow on its own (or pass `--no-browser`). Tokens last 12 hours by default. Run `xmanager login --status` to check yours and `xmanager logout` to remove the cached token.

For CI service accounts (`XMC_CLIENT_SA`) and for sharing experiments through ACLs, see [Authentication and Experiment Access](../../docs/authentication.md).

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
With the Python virtual environment active, the `.env` file configured, and signed in via `xmanager login` (see [Authentication](#authentication)), execute the `launch.py` script:

```bash
python3 launch.py
```

`launch.py` automatically uses `python-dotenv` to locate the `.env` file in parent directories, load the variables, and start the experiment without needing manual prompt input.
