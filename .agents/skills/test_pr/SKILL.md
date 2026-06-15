---
name: Test PR
description: Automates checking out a pull request, building modified OCI images, deploying to a GKE test environment, prompting for manual verification, and posting results back to the PR.
---

# Test PR Workflow

This skill automates testing a GitHub Pull Request in a running GKE development environment.

## 1. Input & PR Checkout
The agent accepts a GitHub Pull Request URL or number.

### Command
```bash
# 1. Checkout the branch associated with the PR
gh pr checkout <pr_number>

# 2. Pull the latest commits on the branch
git pull
```

---

## 2. Load Environment Configuration
Parse variables from `.env` in the root of the workspace:
* `PROJECT_ID`
* `GKE_CLUSTER`
* `K8S_NAMESPACE`
* `GCP_REGION`
* `XMC_DOMAIN`
* `XMC_ACR_TARGET`

Set GKE credentials context:
```bash
gcloud container clusters get-credentials <GKE_CLUSTER> \
    --region <GCP_REGION> \
    --project <PROJECT_ID>
```

---

## 3. Rebuild and Deploy to GKE
Run the helper script to build/push modified containers and restart deployments:
```bash
export XMC_ACR_TARGET="<XMC_ACR_TARGET>"
export K8S_NAMESPACE="<K8S_NAMESPACE>"

./tools/deploy_pr_images.py <base_branch>
```

### Handling Helm Changes
If the script prints `RESULT:HELM_UPGRADE_REQUIRED`, run a Helm upgrade to deploy updated charts:
```bash
# Extract environment name (e.g. from XMC_DOMAIN prefix or GKE_CLUSTER name)
# Typically values-cttest8e.yaml for cluster xmc-cttest8d-cttest8e
helm upgrade --install <GKE_CLUSTER> deploy/helm \
    -n <K8S_NAMESPACE> \
    -f deploy/helm/environments/values-<environment>.yaml
```

Wait for GKE deployments to complete rollouts:
```bash
kubectl rollout status deployment/ui-deployment -n <K8S_NAMESPACE>
kubectl rollout status deployment/xid-service-deployment -n <K8S_NAMESPACE>
```

*In case of any errors, collect the pod logs and rollout status to display a summary to the user.*

---

## 4. Prompt for Manual Verification
The agent pauses execution and prompts the user using the `ask_question` tool:
- "The deployment is complete. Please verify the changes at the target environment (https://<XMC_DOMAIN>). What is the test result?"
  - Option 1: "Pass (successful)"
  - Option 2: "Failed (no pass)"

---

## 5. Post Feedback to PR
Based on user selection:
* **If Pass**: Run the following command and finish successfully:
  ```bash
  gh pr comment <pr_number> --body "Manual testing passed on target environment: https://<XMC_DOMAIN>"
  ```
* **If Failed**: Print the status and stop execution (do not comment on the PR).
