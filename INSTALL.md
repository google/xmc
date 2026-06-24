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

## Pre-Deployment Setup

Before executing any deployment pathway, follow these configuration steps to prepare your environment. The steps described in this section assume highly privileged access to a GCP project and limited constraints stemming from organization policies. Please check your organization policies regarding the resources you are allowed to create - most companies will have very specific requirements around domain registration and certificates management. 

### 1. Build and Push Container Images

The XMC services require OCI container images built from the codebase. You must build and push these images to Google Artifact Registry (or another registry of your choice) before deploying.

1. **Create an Artifact Registry repository** in your GCP project:
   ```bash
   gcloud artifacts repositories create xmc \
       --repository-format=docker \
       --location=<region> \
       --description="XMC Container Images"
   ```

2. **Configure Docker authentication** to access the repository:
   ```bash
   gcloud auth configure-docker <region>-docker.pkg.dev
   ```

3. **Set environmental variables** specifying your target registry path, environment name suffix, and tag:
   ```bash
   export XMC_ACR_TARGET="<region>-docker.pkg.dev/<project_id>/xmc"
   export XMC_ENV="dev"  # Match your deployment environment name
   export BUILD_TAG="latest"
   ```

4. **Build and push the images** using the generated Bazel targets:
   ```bash
   bazel run //cmd/xid_service:xid_service_image_push
   bazel run //cmd/actuator:actuator_image_push
   bazel run //cmd/experiment_state_server:experiment_state_server_image_push
   bazel run //cmd/ui:ui_image_push
   bazel run //cmd/reconciler:reconciler_image_push
   bazel run //cmd/log_monitor:log_monitor_image_push
   ```

5. **Update image variables** in your deployment configuration file (e.g. `xmanager-cloud-deployment.yaml`, copy of [xmc-deployment-template.yaml](deploy/ctk_v2/xmc-deployment-template.yaml)):
   ```yaml
   xid_image: "<region>-docker.pkg.dev/<project_id>/xmc/xid-service-dev:latest"
   actuator_image: "<region>-docker.pkg.dev/<project_id>/xmc/actuator-dev:latest"
   ess_image: "<region>-docker.pkg.dev/<project_id>/xmc/experiment-state-server-dev:latest"
   ui_image: "<region>-docker.pkg.dev/<project_id>/xmc/ui-dev:latest"
   reconciler_image: "<region>-docker.pkg.dev/<project_id>/xmc/reconciler-dev:latest"
   log_monitor_image: "<region>-docker.pkg.dev/<project_id>/xmc/log-monitor-dev:latest"
   ```

### 2. DNS and Domain Name Configuration

XMC relies on GKE Gateway API to expose HTTP, HTTPS, and gRPC endpoints externally on a single global external static IP address.

#### Why a domain is required
- **Gateway Routing**: The Gateway routes incoming traffic to the appropriate service (e.g. UI, ESS backend, or gRPC) using HTTP host headers.
- **TLS/SSL Provisioning**: Google Trust Services (GTS) or Let's Encrypt requires a valid, publicly resolvable DNS domain to verify domain ownership (via ACME HTTP/DNS challenges) before issuing certificates.

#### Using GKE Gateway Technical Domains in Non-Production Environments
If you are deploying to a development, staging, or testing environment and do not have a dedicated corporate/personal domain name, you have two options to provision DNS records:

##### Option A: Using a Shared Custom Domain (e.g. `example.com`)
If your organization maintains a delegated DNS zone (e.g. in Cloud DNS) for development clusters:
1. Set the `domain_name` variable in your configuration to the shared technical domain (e.g. `example.com`).
2. Segment your setup using a unique `environment` suffix (e.g. `my-dev`). The blueprints automatically route the endpoints to:
   - UI: `my-dev.example.com`
   - API: `api.my-dev.example.com`
   - gRPC: `grpc.my-dev.example.com`
3. Map the Gateway's static IP (retrieved during deployment) to these domain names by adding **A records** in the DNS managed zone of your shared domain.

##### Option B: Using Google Cloud-provided Endpoints Domains (`cloud.goog`)
If you do not have any custom domain or DNS zone, you can use Google Cloud Endpoints to register free public DNS records under Google's managed `cloud.goog` domain.

The domain names will have the following structure:
- UI: `<environment>.endpoints.<project_id>.cloud.goog`
- API: `api.<environment>.endpoints.<project_id>.cloud.goog`
- gRPC: `grpc.<environment>.endpoints.<project_id>.cloud.goog`

To configure this:
1. In your configuration file (e.g. `xmanager-cloud-deployment.yaml`), set the `domain_name` variable to:
   ```yaml
   domain_name: "endpoints.<project_id>.cloud.goog"
   ```
   *(Replace `<project_id>` with your Google Cloud Project ID)*

2. After compiling and deploying the core infrastructure to retrieve the Gateway's global static IP address (Step 6 of the deployment process), you must register the three subdomains. Create three YAML configuration files containing the Endpoint configurations:

   **`ui-dns.yaml`**:
   ```yaml
   swagger: "2.0"
   info:
     description: "UI DNS for XMC"
     title: "XMC UI DNS"
     version: "1.0.0"
   host: "<environment>.endpoints.<project_id>.cloud.goog"
   x-google-endpoints:
   - name: "<environment>.endpoints.<project_id>.cloud.goog"
     target: "<gateway_external_ip>"
   paths: {}
   ```

   **`api-dns.yaml`**:
   ```yaml
   swagger: "2.0"
   info:
     description: "API DNS for XMC"
     title: "XMC API DNS"
     version: "1.0.0"
   host: "api.<environment>.endpoints.<project_id>.cloud.goog"
   x-google-endpoints:
   - name: "api.<environment>.endpoints.<project_id>.cloud.goog"
     target: "<gateway_external_ip>"
   paths: {}
   ```

   **`grpc-dns.yaml`**:
   ```yaml
   swagger: "2.0"
   info:
     description: "gRPC DNS for XMC"
     title: "XMC gRPC DNS"
     version: "1.0.0"
   host: "grpc.<environment>.endpoints.<project_id>.cloud.goog"
   x-google-endpoints:
   - name: "grpc.<environment>.endpoints.<project_id>.cloud.goog"
     target: "<gateway_external_ip>"
   paths: {}
   ```

3. Deploy these service configurations to Google Cloud Endpoints:
   ```bash
   gcloud endpoints services deploy ui-dns.yaml
   gcloud endpoints services deploy api-dns.yaml
   gcloud endpoints services deploy grpc-dns.yaml
   ```
   *Note: Creating these Endpoints automatically registers public DNS A records pointing to `<gateway_external_ip>` under the `cloud.goog` zone.*

### 3. OAuth2 Client (IAP Credentials) Setup

XMC uses an application-level [Google OAuth2 flow](https://developers.google.com/identity/protocols/oauth2). This requires Google OAuth 2.0 Web Client credentials (configured via `iap_client_id` and `iap_client_secret`).

#### Step-by-Step Creation Guide
1. Navigate to the **[APIs & Services > Credentials](https://console.cloud.google.com/apis/credentials)** console page.
2. Select your Google Cloud project.
3. Configure the **OAuth consent screen** if not done previously (choose **Internal** for restricted/private access, fill in App Name, User support email, and developer contact).
4. On the Credentials page, click **+ Create Credentials** > **OAuth client ID**.
5. Select **Web application** as the application type.
6. Name your client (e.g., `XMC Web Application`).
7. Under **Authorized redirect URIs**, add:
   - `https://api.<environment>.<domain>/api/callback`
   *(Substitute `<environment>` and `<domain>` with your configured suffix and domain, e.g. `https://api.xmc.example.com/api/callback`)*.
8. Click **Create** and copy the resulting **Client ID** and **Client Secret** into your `xmanager-cloud-deployment.yaml`:
   ```yaml
   iap_client_id: "YOUR_OAUTH_CLIENT_ID"
   iap_client_secret: "YOUR_OAUTH_CLIENT_SECRET"
   ```

For detailed guidance, refer to the [OAuth 2.0 APIs Credentials Documentation](https://support.google.com/cloud/answer/6158849).

### 4. cert-manager and Google Trust Services (GTS) Setup

To configure SSL, XMC needs Google Trust Services (GTS) External Account Binding (EAB) credentials.

#### How to Obtain GTS EAB Keys
1. Enable the Public CA API in your Google Cloud Project:
   ```bash
   gcloud services enable publicca.googleapis.com
   ```
2. Request a new External Account Key:
   ```bash
   gcloud publicca external-account-keys create
   ```
3. The command will output a `keyId` and `b64MacKey` (HMAC key). Map these to:
   - **`gts_key_id`** / **`certs.gts.keyId`**: Alphanumeric ID string (e.g. `a1b2c3d4e5f6g7h8i9j0`).
   - **`gts_hmac_key`** / **`certs.gts.hmacKey`**: Base64-encoded HMAC key secret (e.g. `A1b2C3d4E5f6G7h8I9j0K1l2==`).
   - **`certs_email`**: A valid email address registered with GTS to receive certificate expiration alerts (e.g., `admin@example.com`).

Configure these in your `xmanager-cloud-deployment.yaml`:
```yaml
certs_email: "your-email@example.com"
gts_key_id: "your-gts-key-id"
gts_hmac_key: "your-gts-hmac-key"
```

---

## Approach 1: Automated Agent Deployment (deploy_xmc)

If you are running an AI-assisted agent session, the agent can automate the entire deployment of the Cluster Toolkit v2 blueprint. 

### How to Trigger
Instruct the agent:
> Deploy XMC using the blueprint configuration at `deploy/ctk_v2/xmanager-cloud-deployment.yaml`

### How the Agent Deploys under the Hood:
1. **Pre-flight Check**: Validates the presence of `gcloud`, `kubectl`, `helm`, `terraform`, and `ghpc`.
2. **Copy Protobuf Descriptors**: Staged proto structures required by the application:
   ```bash
   cp ./internal/storage/spanner/emulator/ess_messages.pb ./deploy/
   cp ./internal/storage/spanner/emulator/xid_messages.pb ./deploy/
   ```
3. **Blueprint Compilation**:
   ```bash
   ./cluster-toolkit/ghpc create deploy/ctk_v2/xmanager-cloud.yaml -d <deployment_config_path> -o ctk-out -l WARNING -w --force
   ```
4. **Deploy APIs (`apis` group)**:
   ```bash
   ./cluster-toolkit/ghpc deploy ctk-out/xmc-v2-run --only apis --auto-approve
   ```
5. **Deploy Core Infrastructure (`primary` group)**:
   ```bash
   ./cluster-toolkit/ghpc deploy ctk-out/xmc-v2-run --only primary --auto-approve
   ```
6. **Extract Static IP & Pause for DNS / OAuth Settings**:
   The agent retrieves the global external static IP:
   ```bash
   gcloud compute addresses describe xmc-gateway-static-ip-<environment> --global --project=<project_id> --format='value(address)'
   ```
   > [!IMPORTANT]
   > The agent will pause here and ask you to:
   > - Configure **A records** in your authoritative DNS for the external IP (e.g. `<environment>.example.com`, `api.<environment>.example.com`, `grpc.<environment>.example.com`).
   > - Add the OAuth redirect URI in your GCP Console: `https://api.<environment>.example.com/api/callback`.
   >
   > Once done, reply to the agent to resume execution.
7. **Deploy Compute Cluster (`compute` group)**:
   ```bash
   ./cluster-toolkit/ghpc deploy ctk-out/xmc-v2-run --only compute --auto-approve
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
