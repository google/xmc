# XManager on Cloud (XMC) - Cluster Toolkit v2 Deployment Guide

This directory contains the official Google Cloud HPC Cluster Toolkit v2 blueprints and configurations to automate the provisioning of the complete XManager on Cloud (XMC) infrastructure stack leveraging the modern GKE-hosted topology. This is intended for initial deployment of XMC. This tooling is not intended for upgrading existing instances to latest version of XMC. 

## Introduction

This blueprint is created to help deploy XMC leveraging the current topology which uses:
1. **GKE Deployment for UI**: The UI runs inside the GKE cluster itself, instead of Cloud Run.
2. **GKE Gateway API**: Exposes the UI, HTTP API, and gRPC endpoints on a single Global External IP address using Gateway and HTTPRoute resources.
3. **App-level OAuth2**: Users are authenticated using OAuth2 client credentials and session JWTs managed by the application itself.
4. **cert-manager with Google Trust Services (GTS)**: Automates the provisioning of TLS certificates.

## Phase 1: Configuration Variables

First, copy the template deployment configuration file and open it to configure deployment variables to match your target environment:
```bash
cp deploy/ctk_v2/xmc-deployment-template.yaml deploy/ctk_v2/xmanager-cloud-deployment.yaml
```
Open `deploy/ctk_v2/xmanager-cloud-deployment.yaml` and configure the deployment variables:

### Core Variables
*   **`deployment_name`**: A unique string identifier appended to all provisioned resource names (e.g., `xmc-v2-run`).
*   **`project_id`**: The target Google Cloud Project ID where the XMC infrastructure will be deployed.
*   **`region`**: The GCP region for GKE compute node pools, Spanner, and networking assets (e.g., `us-west1`).
*   **`environment`**: An environment suffix tag used in resource names (e.g., `xmcv2`).
*   **`domain_name`**: The base DNS domain name for the API endpoints (e.g., `example.com`). For details on why a domain is required and how to configure free Google-managed domains, see [Domain Configuration in INSTALL.md](../../INSTALL.md#3-dns-and-domain-name-configuration).

### App-level Authentication Configuration
*   **`iap_client_id`** & **`iap_client_secret`**: OAuth Web Client credentials used by the application backend for Google OAuth2 flow. For step-by-step instructions on creating these, see [OAuth2 Client Setup in INSTALL.md](../../INSTALL.md#4-oauth2-client-iap-credentials-setup).
*   **`jwt_secret`**: A unique JWT secret key used by the backend to sign and verify cookies.
*   **`allowed_emails`**: A comma-separated list of email addresses permitted to sign in to XManager.
*   **`authorized_iam_role`**: The GCP IAM Role for the workload identity binding (e.g., `projects/YOUR_GCP_PROJECT/roles/YOUR_IAM_ROLE`).

### cert-manager GTS Configuration
For step-by-step setup of Google Trust Services (GTS) External Account Binding (EAB) keys, see [GTS cert-manager Setup in INSTALL.md](../../INSTALL.md#5-cert-manager-and-google-trust-services-gts-setup).
*   **`certs_email`**: Email address to register with cert-manager.
*   **`gts_key_id`**: GTS External Account Binding key ID.
*   **`gts_hmac_key`**: GTS External Account Binding HMAC secret.

---

## 🏁 Phase 2: How to Deploy

### Step 1: Compile the Blueprint
Navigate into your master directory containing the `cluster-toolkit` folder and compile the blueprint:

```bash
./cluster-toolkit/gcluster create deploy/ctk_v2/xmanager-cloud.yaml -d deploy/ctk_v2/xmanager-cloud-deployment.yaml -o ctk-out -l WARNING -w --force
```
This command compiles the modules and generates the `xmc-v2-run` deployment folder inside `ctk-out/` containing the generated Terraform code.

### Step 2: Provision the Infrastructure
Execute the deployment to begin provisioning:

```bash
./cluster-toolkit/gcluster deploy ctk-out/xmc-v2-run
```
Type `a` and press Enter to approve the rollout of each group when prompted by the CLI.

---

## 🧹 Phase 3: How to Destroy

To safely tear down all provisioned infrastructure, run:

```bash
./cluster-toolkit/gcluster destroy ctk-out/xmc-v2-run --robust
```

---

## 🔍 Post-Deployment Verification

Once deployment completes successfully, run the post-deployment verification script:

```bash
./deploy/ctk_v2/verify_xmc.sh
```
This script checks the status of all pods, services, gateway routing, and certificates in the `xmc` namespace.
