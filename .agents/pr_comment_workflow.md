# PR Comment-Driven Development Workflow

This document proposes a structured agent workflow that automates the process of addressing GitHub Pull Request comments. 

```mermaid
graph TD
    A["User provides PR comment link"] --> B["Agent runs fetch_pr_comment.py"]
    B --> C["Agent extracts: body, file path, line"]
    C --> D["Agent performs Bazel Query to find affected binaries"]
    D --> E["Agent prepares Implementation Plan"]
    E --> F["Agent runs Unit Tests & Coverage"]
    F --> G["Agent builds & pushes OCI Images"]
    G --> H["Agent performs Kubectl Rollout Restart"]
    H --> I["Agent reports status & updates PR comment"]
```

---

## 1. Input & Comment Details Fetching

The agent accepts a comment link. The helper script `tools/fetch_pr_comment.py` makes a call to the GitHub API (using the `GITHUB_TOKEN` environment variable if repository access requires authentication) to retrieve the raw comment context.

### Execution Command
```bash
./tools/fetch_pr_comment.py "<github_comment_url>"
```

### Result Schema
```json
{
  "type": "review_comment",
  "comment_id": 12345678,
  "author": "reviewer_username",
  "body": "Comment text describing the request (e.g., 'Refactor this to avoid nested if-else checks')",
  "file_path": "pkg/auth/oauth2/handlers.go",
  "line": 150,
  "diff_hunk": "@@ -148,5 +148,8 @@...",
  "commit_id": "a1b2c3d4e5f6..."
}
```

---

## 2. Dependency Mapping & Impact Analysis

To make the workflow precise, the agent must figure out which Bazel binaries and Kubernetes deployments are impacted by the changed file.

### Bazel Package Dependency Mapping
The agent runs `bazel query` to discover all `go_binary` targets that transitively depend on the modified package:

```bash
# 1. Identify the package of the modified file
# E.g. file_path = "pkg/auth/oauth2/handlers.go" -> package //pkg/auth/oauth2

# 2. Query all binaries depending on it
bazel query "kind(go_binary, rdeps(//..., //pkg/auth/oauth2))"
```

*Example Output:*
- `//cmd/xid_service:xid_service`
- `//cmd/experiment_state_server:experiment_state_server`

### Mapping Binaries to GKE Deployments
Each `go_binary` mapped above is associated with an OCI image builder and GKE deployment:

| Bazel Target | Push Command Target | Helm Template File | GKE Deployment Name |
| :--- | :--- | :--- | :--- |
| `//cmd/xid_service:xid_service` | `//cmd/xid_service:xid_service_image_push` | `deploy/helm/templates/xid/deployment.yaml` | `xid-service-deployment` |
| `//cmd/experiment_state_server` | `//cmd/experiment_state_server:ess_image_push` | `deploy/helm/templates/ess/deployment.yaml` | `experiment-state-server-deployment` |
| `//cmd/actuator:actuator` | `//cmd/actuator:actuator_image_push` | `deploy/helm/templates/actuator/deployment.yaml` | `actuator-deployment` |
| `//cmd/reconciler:reconciler` | `//cmd/reconciler:reconciler_image_push` | `deploy/helm/templates/reconciler/deployment.yaml` | `reconciler-deployment` |
| `//cmd/log_monitor:log_monitor` | `//cmd/log_monitor:log_monitor_image_push` | N/A | N/A |
| `//cmd/ui` (Frontend TS/HTML) | `//cmd/ui:ui_image_push` | `deploy/helm/templates/ess/ui-deployment.yaml` | `ui-deployment` |

---

## 3. Implementation Plan Preparation

Based on the parsed review comment and impacted targets, the agent creates a draft implementation plan for your review. It will format the plan as a Markdown artifact in the brain folder:

- **Analysis**: Visual code excerpt and summary of the reviewer's concern.
- **Proposed Code Changes**: Target file path, exact lines to modify, and a git diff style code snippet.
- **Affected Components list**: Which binaries and deployments need to be verified.
- **Execution steps**: A sequence of commands to verify and deploy.

---

## 4. Test Verification & Code Coverage Check

To verify code correctness, the agent runs all relevant unit tests and measures code coverage.

### Run Unit Tests
```bash
# Run unit tests for the affected package
bazel test //pkg/auth/oauth2/...
```

### Check Coverage
To verify test coverage, the agent runs:
```bash
bazel coverage --combined_report=lcov //pkg/auth/oauth2/...
```
The output coverage file is generated under `bazel-out/_coverage/_coverage_report.dat`.
The agent will parse this file (or generate a summary) to report the package test coverage percentage and point out any lines missing tests.

---

## 5. Build, Push, and GKE Redeployment

Using the `.env` configuration file, the agent updates the deployment environment.

### Target Environment (Extracted from `.env`)
- **GCP Project ID**: `xmc-test-project-6`
- **GKE Cluster**: `xmc-cttest8d-cttest8e`
- **GCP Region**: `europe-central2`
- **K8S Namespace**: `xmc`
- **Artifact Registry Target**: `us-docker.pkg.dev/xmc-test-project-6/xmc`

### Rebuild and Push Containers
For each affected binary (e.g. `xid_service`), the agent sets up the environment variables and runs the push script:
```bash
# Set build environment variables
export XMC_ACR_TARGET="us-docker.pkg.dev/xmc-test-project-6/xmc"
export XMC_ENV="dev"

# Build and push the container image to GCP Artifact Registry
bazel run //cmd/xid_service:xid_service_image_push
```

### Rollout Restart in GKE Cluster
The agent initiates a rolling update to download the newly pushed image (tagged `:latest`):

```bash
# 1. Authenticate to the GKE Cluster
gcloud container clusters get-credentials xmc-cttest8d-cttest8e \
    --region europe-central2 \
    --project xmc-test-project-6

# 2. Restart the deployment to pick up the updated container image
kubectl rollout restart deployment/xid-service-deployment -n xmc

# 3. Monitor rollout status
kubectl rollout status deployment/xid-service-deployment -n xmc
```

---

## 6. Workflow Example

Here is a step-by-step example of how you can invoke this agent:

1. **You ask**:
   > Fix PR comment: `https://github.com/google/xmc/pull/15#discussion_r124892`

2. **The agent executes**:
   - Downloads comment content: `"Move localhost IP checks to a shared helper"`
   - Identifies code location: `pkg/auth/oauth2/handlers.go:120`
   - Runs `bazel query` and maps changes to `//cmd/xid_service` and `//cmd/experiment_state_server`.
   - Proposes refactored code.

3. **You approve the plan**.
4. **The agent executes**:
   - Edits the files.
   - Runs unit tests and coverage calculations.
   - Pushes updated OCI containers via `bazel run //cmd/xid_service:xid_service_image_push`.
   - Restarts GKE pods: `kubectl rollout restart deployment/xid-service-deployment -n xmc`.
   - Replies to the conversation thread with the test outputs and confirmation of successful rollout.
