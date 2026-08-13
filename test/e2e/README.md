# Headless UI E2E Testing for XManager Cloud (XMC)

This directory contains resources for running **End-to-End (E2E) validations** for XManager Cloud using Playwright against an ephemeral Kubernetes architecture mimicking production.

## Status

**Currently, this suite is meant for LOCAL execution and validation only.**
It does *not* execute in our automated PR CI/CD environment yet. This "Step 0" implementation provides a sandbox for engineers to quickly validate cross-component interactions locally without needing a dedicated GKE project or manual deployment.

The long-term goal is to hook these tests into GitHub Actions using a similar isolated strategy (with Ephemeral Spanner instances) as proven by this local PoC.

## Architecture

This testing strategy bypasses standard GCP infrastructure dependencies:
*   **Kubernetes Engine:** Deploys to a local `KinD` (Kubernetes in Docker) instance.
*   **Artifact Registry:** Builds and pushes local Bazel images directly to KinD via `kind load docker-image`.
*   **Google OAuth / IAP:** Uses the `auth.mockAuth` mechanism to bypass IAP and inject a mock JWT token.
*   **Spanner:** It still relies on a **real** Cloud Spanner instance (since the Spanner emulator misses certain features). It auto-provisions ephemeral databases, applies schema migrations, and injects your active user credentials (`GOOGLE_APPLICATION_CREDENTIALS`) to authenticate with Spanner locally.

## Prerequisites

Before running the suite, ensure you have the following installed:
1.  **Docker Desktop / Daemon** (must be running)
2.  **KinD** (`brew install kind` or `go install sigs.k8s.io/kind@latest`)
3.  **kubectl**
4.  **helm**
5.  **bazel**
6.  **gcloud** (Authenticated: `gcloud auth application-default login`)
7.  **uv** or **pip** (For Python environment management)

## How to Run

### 1. Provision the Environment

A dedicated teardown-and-bootstrap script is provided to stand up the local replica:

```bash
cd xmc/
export GCP_PROJECT_ID=your-project-id
export SPANNER_INSTANCE=your-spanner-instance-name
./test/e2e/run_local_e2e.sh
```

**What this script does:**
- Destroys any old `xmc-local-e2e` KinD cluster.
- Creates a fresh `xmc-local-e2e` KinD cluster.
- Builds all XMC binaries via `bazel build //cmd/...`.
- Injects those images into the KinD node.
- Installs Kueue, JobSet, and Redis via Helm.
- Provisions new temporary Spanner databases starting with `e2e-xmc-local`.
- Runs schema migrations on those databases.
- Copies the `deploy/helm` repo, patches it to use local images, and installs it.
- Patches the Pod specs to mount your local `GOOGLE_APPLICATION_CREDENTIALS` (crucial for Spanner).

*Note: You must have `GCP_PROJECT_ID`, `SPANNER_INSTANCE`, and `GOOGLE_APPLICATION_CREDENTIALS` exported in your shell before running the script.*

### 2. Stand Up Port Forwards

Once the cluster is up and all pods show `Running` (verify via `kubectl get pods`), expose the mock gateway:

```bash
# Port-forward the UI frontend
kubectl port-forward svc/ui-service 8080:80 &
# Port-forward the API backend
kubectl port-forward svc/api-service 8081:80 &
```

### 3. Run the Playwright Suite

The tests use Playwright for Python. We recommend using a virtual environment:

```bash
cd test/e2e
uv venv
source .venv/bin/activate
uv pip install pytest playwright requests
playwright install  # Downloads headless browser binaries

pytest test_ui_experiment.py
```

## Troubleshooting

- **`CrashLoopBackOff` on XMC services:** This is typically due to Spanner authorization failures. Ensure your Application Default Credentials (`gcloud auth application-default login`) were correctly mounted from `~/.config/gcloud/application_default_credentials.json`.
- **ErrImagePull:** If XMC pods cannot pull images, ensure `bazel build //cmd/...` succeeded and that `kind load docker-image` correctly populated the cluster.
- **Kueue / Webhook Errors:** If `helm upgrade kueue` hangs or fails, delete `mutatingwebhookconfiguration` and `validatingwebhookconfiguration` resources in the cluster and try again.
