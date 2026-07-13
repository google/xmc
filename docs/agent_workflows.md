# Agent Workflows (AI-Assisted)

This repository includes pre-configured **Agent Skills** located under the [.agents/skills/](../.agents/skills) directory. These skills define structured workflows that allow the agent to automate complex operational and development tasks.

> [!WARNING]
> Do not modify the skill definition markdown files (`SKILL.md`) in the `.agents` folder directly.

---

## 1. Automated Blueprint Deployment (`deploy_xmc`)

The `deploy_xmc` skill compiles and deploys Cluster Toolkit v2 blueprints. It validates system dependencies, deploys GCP resource groups sequentially, pauses to prompt for manual DNS/OAuth registrations, and runs verification checks.

* **How to trigger**: Ask the agent to deploy the environment using the config overrides path, for example:
  > Deploy XMC using the blueprint configuration at `deploy/ctk_v2/xmanager-cloud-deployment.yaml`
* **Workflow Details**: See [deploy_xmc/SKILL.md](../.agents/skills/deploy_xmc/SKILL.md).

---

## 2. PR Comment-Driven Development (`pr_comment_workflow`)

The `pr_comment_workflow` skill automates resolving review comments on Pull Requests. The agent fetches comment context, determines affected packages and GKE deployments, runs unit tests and coverage checks, commits code, replies to the comment, pushes container images, and restarts GKE deployments.

* **How to trigger**: Ask the agent to resolve a specific GitHub discussion/review link, for example:
  > Address the PR review comment: `https://github.com/google/xmc/pull/15#discussion_r124892`
* **Workflow Details**: See [address_pr_comment/SKILL.md](../.agents/skills/address_pr_comment/SKILL.md).

---

## 3. Automated Environment Upgrade (`upgrade_xmc`)

The `upgrade_xmc` skill automates upgrading an existing XMC environment on GKE to run the latest code from the current branch (`HEAD`), leveraging a local `.env` configuration file in the repository root. It validates system dependencies, checks and runs pending database migrations, builds and pushes updated OCI container images using Bazel, upgrades the application workloads using Cluster Toolkit (`gcluster`), triggers a rolling restart of all GKE workloads, and runs verification checks.

* **How to trigger**: Ask the agent to upgrade the environment, for example:
  > Upgrade my XMC environment using the local `.env` file
* **Workflow Details**: See [upgrade_xmc/SKILL.md](../.agents/skills/upgrade_xmc/SKILL.md).

