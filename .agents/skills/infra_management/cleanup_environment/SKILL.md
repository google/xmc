---
name: XMC Cleanup Environment
description: Guides the agent through tearing down an XMC environment deployed via Cluster Toolkit safely.
---

# Cleanup XMC Environment Workflow

Tears down deployed XMC environment resources safely to avoid cost leakages.

> [!WARNING]
> Permanently deletes GKE clusters, VPC networks, and Cloud Spanner databases and backups.

## 1. Prerequisites & User Approval
- Strictly lock path search to `./ctk-out/` in immediate directory.
- Verify `deletion_protection = false` in `terraform.tfvars`.
- Ask user explicit confirmation via `ask_question` before executing destroy commands.

## 2. Teardown Steps
1. **Delete Spanner Backups**: `gcloud spanner backups delete <backup_id> --instance=xmc-<env> --project=<project>`
2. **Run Toolkit Destroy**: `./cluster-toolkit/gcluster destroy ctk-out/<deployment_name> --auto-approve`
3. **Delete Cloud DNS Records**: Delete environment A records via `gcloud dns record-sets delete`.
4. **Clean Local Config**: Remove kubeconfig context and local `ctk-out/<deployment_name>` folder.

Refer to `references/resource_teardown_specs.md` for teardown failure mitigations.
