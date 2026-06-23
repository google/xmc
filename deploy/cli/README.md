# XManager CLI

## Introduction


[XManager CLI](https://github.com/google-deepmind/xmanager) is a python library for interacting with Xmanager Cloud (and other Xmanager flavors). This directory is aimed at helping with Xmanager CLI installation specifically for working with XMC backend. 


## Installation

The script `install_xmanager.sh` automates:
1. Creating a Python virtual environment.
2. Cloning the official `xmanager` library (`xm-cloud` branch) and `googleapis` repository.
3. Overwriting outdated gRPC proto definitions inside `xmanager` with the fresh XMC API protos.
4. Compiling and installing the package inside the virtual environment.

### Prerequisites
- **Git** (version 2.25+ recommended for remote/GitHub sparse checkouts)
- **Python 3**

### Basic Usage

If you are running the script from a local clone of the `xmc` repository:
```bash
./install_xmanager.sh
```
This automatically locates your local `api/protos` definitions and builds them inside `./venv`.

### Options

The script accepts several command-line flags to customize the installation:

```bash
./install_xmanager.sh [options]

Options:
  --protos-loc <url_or_path>     Location of XMC protos. Can be a local directory path or a 
                                 GitHub repository tree URL.
                                 (default: local checkout if available, else https://github.com/google/xmc/tree/main/api/protos)
  --xmanager-branch <branch>     The XManager Git branch/tag to clone (default: xm-cloud)
  --venv <path>                  Target directory for the Python virtual environment (default: ./venv)
  -h, --help                     Show usage information
```

#### Example: Installing with Remote Protos
```bash
./install_xmanager.sh --venv ./my_env --protos-loc https://github.com/google/xmc/tree/main/api/protos
```

### Activating the Virtual Environment
After installation completes successfully, activate the environment:
```bash
source ./venv/bin/activate
```

### Verification
You can verify the installation is working correctly by checking imports:
```bash
python3 -c "import xmanager_cloud"
python3 -c "import xmanager_cloud.experiment_state_server.experiment_state_api"
```

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
python launch.py
```

`launch.py` automatically uses `python-dotenv` to locate the `.env` file in parent directories, load the variables, and start the experiment without needing manual prompt input.