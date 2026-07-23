---
name: XMC Upgrade Environment
description: Upgrades an existing XMC environment on GKE to the latest current branch state.
---

# Upgrade XMC Environment Workflow

Upgrades an existing XMC environment to run code from the current local repository branch (`HEAD`).

## 1. Check Configuration & Database Migrations
```bash
# Check environment and GKE connectivity
python3 ./tools/upgrade_dev_env.py --only check

# Check for pending Spanner database migrations
python3 ./tools/upgrade_dev_env.py --only migrate
```

> [!IMPORTANT]
> If pending migrations exist (Exit 10), prompt the user for explicit approval before running migrations.

## 2. Execute Upgrade
```bash
# Case A: No pending migrations
python3 ./tools/upgrade_dev_env.py --start-from images

# Case B: User approved migrations
python3 ./tools/upgrade_dev_env.py --start-from migrate --run-migrations
```

## 3. Verification & Validation
Refer to `references/migration_steps.md` for troubleshooting step flags. Verify environment health:
```bash
ENVIRONMENT="${XMANAGER_ENV:-dev}" python3 ./deploy/ctk_v2/verify_xmc.py
```
