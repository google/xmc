# Infrastructure Management & Safety Rules

Safety rules, path locks, and deletion protection checks for Cluster Toolkit (gcluster) and GKE operations.

---

## 1. Strict Path Locks & Artifact Boundaries
- **Strict Path Lock**: Compilation and deployment artifacts MUST strictly reside in `./ctk-out/` in the immediate working directory.
- **No Global Traversal**: Never run global find operations across parent directories or worktrees to locate `ctk-out/` folders.

## 2. Blueprint Deployment & Upgrade Safety
- **Enforced Choice Prompt**: Prompt the user to choose between Automated Setup (`deploy_dev_env.py`) or Manual Step-by-Step deployment (`gcluster`).
- **GKE Gateway API Verification**: Ensure GKE Gateway API standard channel is explicitly enabled before deploying application charts.
- **Migration Approval Gate**: When upgrading XMC via `upgrade_dev_env.py`, stop and require explicit user approval before executing pending database migrations.

## 3. Deletion Protection & Teardown Safety
- **Explicit User Confirmation**: Always confirm destructive teardowns with the user using `ask_question`.
- **Deletion Protection Check**: Verify `deletion_protection = false` in `terraform.tfvars` before executing destroy commands.
- **Spanner Backup Teardown Order**: Delete active Spanner backups (`gcloud spanner backups delete`) prior to destroying Spanner instances to prevent Terraform blockages.
