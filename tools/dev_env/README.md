# Development Environment Tooling (`tools/dev_env/`)

The `tools/dev_env/` package provides automation tools and a unified command-line interface (`dev_env.py`) for managing XManager on Cloud (XMC) development environments.

---

## Unified CLI Entrypoint (`dev_env.py`)

The unified CLI `dev_env.py` consolidates environment lifecycle operations for remote Google Kubernetes Engine (GKE) deployments into a single extensible entrypoint.

### Usage

```bash
# Direct invocation via Python
python3 tools/dev_env/dev_env.py [command] [options...]

# Or via top-level forwarding wrapper
./tools/dev_env.py [command] [options...]
```

### Global Flags

| Flag | Short | Description |
| :--- | :--- | :--- |
| `--verbose` | `-v` | Enable detailed debug logging output |
| `--quiet` | `-q` | Suppress non-error output (log ERROR level only) |
| `--version` | | Print dev_env version and exit |
| `--help` | `-h` | Display command help and usage examples |

---

## Subcommands Reference

### 1. `gke-deploy` (Deploy Remote GKE Environment)
Compiles Cluster Toolkit (`gcluster`) blueprints and orchestrates step-by-step remote infrastructure and service deployment.

```bash
python3 tools/dev_env/dev_env.py gke-deploy \
  --config deploy/ctk_v2/xmanager-cloud-deployment.yaml \
  --project-id <PROJECT_ID> \
  --environment <ENV_NAME> \
  --dns-project <DNS_PROJECT_ID> \
  --dns-zone <DNS_ZONE_NAME>
```

#### Options
- `--config`, `-c`: Path to the Cluster Toolkit deployment YAML configuration file (required).
- `--project-id`, `-p`: Google Cloud project ID (required, or via `PROJECT_ID` env var).
- `--environment`, `-e`: Deployment environment name (required, or via `XMANAGER_ENV` env var).
- `--dns-project`: Google Cloud project hosting Cloud DNS (required).
- `--dns-zone`: Cloud DNS managed zone name (required).
- `--start-from`: Start execution from a specific step (`compile`, `apis`, `primary`, `dns`, `secondary`, `helm`, `finalize`).
- `--only`: Execute only a single step (`compile`, `apis`, `primary`, `dns`, `secondary`, `helm`, `finalize`).
- `--auto-approve`, `-y`: Automatically approve Terraform and Cluster Toolkit prompts.
- `--skip-terraform-init`: Skip `terraform init` during deployment.

---

### 2. `gke-upgrade` (Upgrade Remote GKE Services)
Incrementally rebuilds changed microservices, pushes OCI container images to Artifact Registry, and rolls out Helm chart updates or Spanner migrations without destroying infrastructure.

```bash
python3 tools/dev_env/dev_env.py gke-upgrade \
  --config deploy/ctk_v2/xmanager-cloud-deployment.yaml \
  --project-id <PROJECT_ID> \
  --environment <ENV_NAME>
```

#### Options
- `--config`, `-c`: Path to deployment YAML configuration file.
- `--project-id`, `-p`: Google Cloud project ID.
- `--environment`, `-e`: Deployment environment name.
- `--run-migrations`, `-m`: Run Cloud Spanner database schema migrations during upgrade.
- `--start-from`: Start upgrade from a specific step (`images`, `migrate`, `helm`, `finalize`).
- `--only`: Execute only a single upgrade step (`images`, `migrate`, `helm`, `finalize`).

---

### 3. Generic Aliases (`deploy` / `upgrade`)
For convenience, generic `deploy` and `upgrade` commands are supported and forward to their respective target handlers:

```bash
# Equivalent to gke-deploy
python3 tools/dev_env/dev_env.py deploy --target gke --config ... --dns-project ... --dns-zone ...

# Equivalent to gke-upgrade
python3 tools/dev_env/dev_env.py upgrade --target gke --config ...
```

---

## Configuration Architecture (`LocalDevConfig`)

The `tools/dev_env/config.py` module defines `LocalDevConfig` and provides unified configuration discovery and resolution.

### Resolution Precedence

Configuration values are resolved using strict layered precedence:

```
CLI Flags (--project-id, --environment, --config)
  └── Environment Variables (PROJECT_ID, XMANAGER_ENV, XMC_CONFIG_PATH)
        └── YAML Configuration File (deploy/ctk_v2/*.yaml or deploy/local/*.yaml)
              └── Built-in Defaults
```

### Config File Discovery

When `--config` is not explicitly provided, `dev_env.py` searches candidate locations in order:
1. `deploy/local/xmanager-cloud-local.yaml` (Developer local configuration)
2. `deploy/ctk_v2/xmanager-cloud-deployment.yaml` (Active deployment configuration)
3. `deploy/ctk_v2/xmanager-cloud-deployment-staging.yaml` (Staging configuration)

### Local Configuration Template

A sample local configuration template is provided at [`deploy/local/xmanager-cloud-local-template.yaml`](../../deploy/local/xmanager-cloud-local-template.yaml). Copy it to create your local environment config:

```bash
cp deploy/local/xmanager-cloud-local-template.yaml deploy/local/xmanager-cloud-local.yaml
```

---

## Testing

Run unit tests for the `tools/dev_env` package with Bazel:

```bash
bazel test //tools/dev_env:...
```
