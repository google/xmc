---
name: XMC Deploy Environment
description: Automates compiling and step-by-step deployment of the XMC v2 Cluster Toolkit blueprint.
---

# Deploy XMC Workflow

Guides the agent through deploying XMC infrastructure using Cluster Toolkit v2.

> [!IMPORTANT]
> **Enforced Choice Prompt**: Ask user to choose between:
> - **Option 1: Automated Dev Setup** (`python3 ./tools/deploy_dev_env.py`)
> - **Option 2: Manual Step-by-Step Deployment** (`gcluster`)

## 1. Option 1: Automated Dev Setup
```bash
# Check configurations
python3 ./tools/deploy_dev_env.py --config <config_path> --dns-project <dns_proj> --dns-zone <dns_zone> --only check

# Run automated deployment
python3 ./tools/deploy_dev_env.py --config <config_path> --dns-project <dns_proj> --dns-zone <dns_zone>
```

## 2. Option 2: Manual Step-by-Step Deployment
1. **Compile Blueprint**: `./cluster-toolkit/gcluster create deploy/ctk_v2/xmanager-cloud.yaml -d <config> -o ctk-out -l WARNING -w --force`
2. **Deploy Groups**: Deploy `apis`, `primary`, `compute`, `kubernetes_prereqs`, `xmc_app` sequentially using `gcluster deploy ctk-out/<deployment_name> --only <group> --auto-approve`. Refer to `references/gcluster_groups.md` for full group specifications and DNS pause instructions.

## 3. Verification & Validation
```bash
python3 ./deploy/ctk_v2/verify_xmc.py
```
