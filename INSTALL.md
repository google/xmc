# Installation & Deployment Guide

This guide details the three paths to install and deploy XManager on Cloud (XMC):

1. **Automated Agent Deployment**: Using the pre-configured `deploy_xmc` agent skill.
2. **Cluster Toolkit Blueprint v2**: The recommended manual pathway using automated orchestration blueprints.
3. **Terraform and Helm**: The direct, hands-on infrastructure deployment pathway.

---

## Prerequisites

Before launching any deployment pathway, ensure you have the following CLI tools installed:

| CLI Tool | Verification Command | Expected Outcome |
| :--- | :--- | :--- |
| **`gcloud`** | `gcloud --version` | Prints Google Cloud SDK version info |
| **`kubectl`** | `kubectl version --client` | Prints Kubernetes client version |
| **`helm`** | `helm version` | Prints Helm client version |
| **`terraform`** | `terraform --version` | Prints Terraform version (minimum 1.12.2) |
| **`ghpc`** | `./cluster-toolkit/ghpc version` | Prints gcluster/ghpc tool version |

---

## Approach 1: Automated Agent Deployment (deploy_xmc)

If you are running an AI-assisted agent session, the agent can automate the entire deployment of the Cluster Toolkit v2 blueprint. 

### How to Trigger
Instruct the agent:
> Deploy XMC using the blueprint configuration at `deploy/ctk_v2/xmanager-cloud-deployment.yaml`

### How the Agent Deploys under the Hood:
1. **Pre-flight Check**: Validates the presence of `gcloud`, `kubectl`, `helm`, `terraform`, and `ghpc`.
2. **Blueprint Compilation**:
   ```bash
   ./cluster-toolkit/ghpc create deploy/ctk_v2/xmanager-cloud.yaml -d <deployment_config_path> -o ctk-out -l WARNING -w --force
   ```
3. **Deploy APIs (`apis` group)**:
   ```bash
   ./cluster-toolkit/ghpc deploy ctk-out/xmc-v2-run --only apis --auto-approve
   ```
4. **Deploy Core Infrastructure (`primary` group)**:
   ```bash
   ./cluster-toolkit/ghpc deploy ctk-out/xmc-v2-run --only primary --auto-approve
   ```
5. **Extract Static IP & Pause for DNS / OAuth Settings**:
   The agent retrieves the global external static IP:
   ```bash
   gcloud compute addresses describe xmc-gateway-static-ip-<environment> --global --project=<project_id> --format='value(address)'
   ```
   > [!IMPORTANT]
   > The agent will pause here and ask you to:
   > - Configure **A records** in your authoritative DNS for the external IP (e.g. `<environment>.example.com`, `api.<environment>.example.com`, `grpc.<environment>.example.com`).
   > - Add the OAuth redirect URI in your GCP Console: `https://<environment>.example.com/api/callback`.
   >
   > Once done, reply to the agent to resume execution.
6. **Deploy Compute Cluster (`compute` group)**:
   ```bash
   ./cluster-toolkit/ghpc deploy ctk-out/xmc-v2-run --only compute --auto-approve
   ```
7. **Enable GKE Gateway API standard channel**:
   Before deploying the system Helm charts, the GKE cluster must have the standard Gateway API controller addon enabled:
   ```bash
   gcloud container clusters update xmc-v2-test-<environment> --gateway-api=standard --region=<region> --project=<project_id>
   ```
8. **Install Kubernetes Prerequisites (`kubernetes_prereqs` group)**:
   ```bash
   ./cluster-toolkit/ghpc deploy ctk-out/xmc-v2-run --only kubernetes_prereqs --auto-approve
   ```
9. **Deploy Application (`xmc_app` group)**:
   ```bash
   ./cluster-toolkit/ghpc deploy ctk-out/xmc-v2-run --only xmc_app --auto-approve
   ```
10. **Post-Deployment Verification**:
    The agent runs:
    ```bash
    ./deploy/ctk_v2/verify_xmc.sh
    ```
    And checks that the gateway status is programmed and GTS certificate is active.

---

## Approach 2: Cluster Toolkit Blueprint v2 (Recommended)

We provide automated HPC Cluster Toolkit blueprints to compile and deploy the entire GCP and GKE stack:

1. **Step-by-step Compilation**: Follow the step-by-step instructions on compilation and group-by-group deployments in the [deploy/ctk_v2/README.md](deploy/ctk_v2/README.md).
2. **Execute Deployment**: Run `ghpc deploy` command groups sequentially, checking resource state transitions as you go.

---

## Approach 3: Terraform and Helm Directly (Hands-on)

For direct control over your infrastructure:

1. **Provision Infrastructure**: Use your organization's custom Terraform pipeline to provision the GCP resources:
   - VPC network and subnetworks.
   - GKE cluster with Workload Identity enabled.
   - Cloud Spanner database instance.
   - Memorystore Redis cache.
2. **Deploy Helm Release**: Point your local kubeconfig context to the cluster and deploy the application chart:
   ```bash
   helm upgrade --install xmc ./deploy/helm -n xmc --create-namespace -f my-values.yaml
   ```
   *For details on configuring values, refer to [deploy/helm/values.yaml](deploy/helm/values.yaml).*

---

## Configuration Reference

Supply these parameters in your Cluster Toolkit overrides file (`xmanager-cloud-deployment.yaml`) or your Helm overrides (`values.yaml`):

| Parameter Name | Required? | Default Value | Used By / Description |
|---|---|---|---|
| `project_id` | **Yes** | - | Target Google Cloud Project ID |
| `deployment_name` | **Yes** | - | Unique prefix for resource naming (e.g. `xmc-v2-run`) |
| `region` | **Yes** | - | Google Cloud region for resource provisioning (e.g. `us-west1`) |
| `domain_name` | **Yes** | - | Base DNS domain name for external endpoints (e.g. `example.com`) |
| `environment` | **Yes** | - | Sub-environment descriptor for Spanner database keys (e.g. `dev`) |
| `iap_client_id` | **Yes** | - | Google OAuth Client ID for identity verification |
| `iap_client_secret`| **Yes** | - | Google OAuth Client Secret for identity verification |
| `jwt_secret` | **Yes** | - | Secret key used by ESS backend to sign session cookies |
| `allowed_emails` | Conditional | - | Comma-separated list of user emails authorized to log in - required when `authorized_iam_role` is not provided |
| `authorized_iam_role`| Conditional | - | GCP IAM role granted to GKE workload identities - required when `allowed_emails` are not provided |
| `certs_email` | **Yes** | - | Owner email registered with Google Trust Services (GTS) |
| `gts_key_id` | **Yes** | - | GTS External Account Binding key ID for certificates |
| `gts_hmac_key` | **Yes** | - | GTS External Account Binding HMAC secret for certificates |
| `xid_image` | **Yes** | - | Container image path for the XID service |
| `actuator_image` | **Yes** | - | Container image path for the Actuator daemon |
| `ess_image` | **Yes** | - | Container image path for the Experiment State Server (ESS) |
| `ui_image` | **Yes** | - | Container image path for the Frontend UI portal |
| `reconciler_image` | **Yes** | - | Container image path for the Kueue Reconciler service |
| `log_monitor_image`| **Yes** | - | Container image path for the Pub/Sub Log Monitor |
| `namespace` | No | `"xmc"` | Target Kubernetes namespace for services |
| `gcp_public_cidrs_access_enabled` | No | `false` | Toggles public endpoints on the GKE Control Plane |
| `authorized_cidr` | No | `"0.0.0.0/0"`| Authorized IP CIDR allowed to connect to GKE master |
| `task_signature_ttl_seconds` | No | `86400` | Validity duration of task signature tokens |
| `use_redis_queue` | No | `true` | Uses Memorystore Redis for internal queues when true |
| `deploy_redis` | No | `true` | Provisions Memorystore Redis instance when true |
| `redis_region` | No | - | Memorystore Redis region (defaults to region above) |
