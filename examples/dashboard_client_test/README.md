# XManager Dashboard Client Test Example

This directory contains an example demonstrating how to install the XManager CLI with Dashboard Service protobuf definitions in a Python virtual environment and use `dashboard.py` to interact with the Dashboard Service (`cloud.xmanager.v1.DashboardService`).

## Prerequisites
1. **Python 3.14+** and **Git 2.25+** installed.
2. An optional `.env` configuration file in the repository root (copy from `.env.example`).
3. A running instance of the XMC Dashboard Service accessible at the target endpoint (defaults to `localhost:8080`).

## Installation
Run `install_cli.sh` to create a local virtual environment `./venv` and install `xmanager_cloud` with the compiled Dashboard protobuf definitions (`api/protos/dashboard`):

```bash
./install_cli.sh
```

Activate the virtual environment:

```bash
source ./venv/bin/activate
```

## Running the Launcher
With the virtual environment active, run the launcher script to connect to the Dashboard Service and list available dashboards:

```bash
python3 launch.py
```

## Authentication
By default, `dashboard.py` connects via an insecure gRPC channel (`localhost:8080`), which is suitable for local development or port-forwarding.

If `IAP_CLIENT_ID` and `XMC_CLIENT_SA` are set in your environment or `.env` file, `dashboard.py` automatically generates a Google IAM OIDC identity token via service account impersonation and connects using an authenticated SSL secure channel (`grpc.secure_channel`), injecting `x-goog-user-email` headers to match the authentication mechanism used by other XMC endpoints.

