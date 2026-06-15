# DEPRECATED - XManager on Cloud (XMC) - Cluster Toolkit Deployment Guide

Warning - this blueprint is Deprecated - it should not longer be used to create new instances. It is referencing configuration parameters that are no longer valid. Added here for reference only. 

This directory contains the official Google Cloud HPC Cluster Toolkit blueprints and configurations to automate the provisioning of the complete XManager on Cloud (XMC) infrastructure stack.

## Introduction

This blueprint is created to help deploy XMC leveraging the initial topology which uses:
1. GKE deployments for experiment state server, actuator and reconciler
2. Redis queues or Cloud Tasks
3. Spanner for state storage
4. Cloud Run for web UI + Log Monitor
5. Pub-Sub for log streams
6. Public IPs and Managed DNS zones

## Purpose

This solution provides a fully declarative, production-grade infrastructure-as-code pipeline. By leveraging the Cluster Toolkit, teams can automate the setup of VPC networks, Spanner databases, GKE clusters, and containerized application layers, replacing complex custom setup shell scripts with robust, repeatable, and auditable blueprints.

---

## Phase 1: Prerequisites

The blueprint relies on local Helm charts, SQL schemas, and protocol buffer descriptor files (`.pb`). Because descriptor files are not stored statically in the repository, they must be compiled prior to running the deployment.

### Step 1: Generate Proto Descriptors
Run the following commands from the root of your workspace to compile the protobuf descriptors and copy them to the staging root:

```bash
# Verify that you have copy of proto descriptors:
# - internal/storage/spanner/emulator/ess_messages.pb
# - internal/storage/spanner/emulator/xid_messages.pb

cp ./internal/storage/spanner/emulator/ess_messages.pb ./deploy/
cp ./internal/storage/spanner/emulator/xid_messages.pb ./deploy/
```

---

## Phase 2: Configuration Variables

Open `xmanager-cloud-deployment.yaml` and configure the deployment variables to match your target environment.

### Core Variables
*   **`deployment_name`**: A unique string identifier appended to all provisioned resource names (e.g., `xmc-dev-setup`).
*   **`project_id`**: The target Google Cloud Project ID where the XMC infrastructure will be deployed.
*   **`project_number`**: The numeric project number of your target GCP project. This is required for IAP JWT verification. *(Locate in the Cloud Console via IAM & Admin > Settings > Project number)*.
*   **`region`**: The GCP region for GKE compute node pools, Spanner, and networking assets (e.g., `us-central1`).
*   **`environment`**: An environment suffix tag used in resource names (e.g., `dev`, `staging`, `prod`).
*   **`domain_name`**: The registered DNS domain name for the API endpoints (e.g., `xmc.example.com`).

### Identity-Aware Proxy (IAP) Configuration
*   **`iap_client_id`** & **`iap_client_secret`**: OAuth Web Client credentials used to authenticate and protect the backend services:
    1. Navigate to the Google Cloud Console > **APIs & Services** > **Credentials**.
    2. Click **Create Credentials** > **OAuth client ID** (Application type: *Web application*).
    3. Copy and paste the generated client ID and client secret strings.
*   **`iap_members`**: A list of users, groups, or service accounts allowed to access XManager through IAP (e.g., `["user:username@google.com"]`).

### Networking & Cache Configuration
*   **`authorized_cidr`**: The IP address range permitted to communicate with the GKE control plane. For security, this should be restricted to your local machine's IP address or your team's corporate network/VPN range.
    > [!WARNING]
    > **Security Risk**: Setting this to `0.0.0.0/0` permits public internet access to your GKE Kubernetes API endpoint. While acceptable in isolated sandboxes for temporary convenience, it is a severe security risk and must **never** be used for production environments.
*   **`use_redis_queue`**: Set to `true` to enable the background task queue.
*   **`deploy_redis`**: Set to `true` to provision a managed Memorystore for Redis instance.

### Cluster Optimization
*   **`enable_vertical_pod_autoscaling`**: Set to `true` to enable the native GKE Vertical Pod Autoscaler (VPA) engine for automatic microservice resource sizing.

---

## ⚠️ Crucial Gotchas & Pre-Deployment Awareness

Because XMC provisions a multi-product cloud stack (VPC, Spanner, Redis, GKE, Load Balancers, Cloud Run), resolving configuration issues mid-deployment can be highly time-consuming. 

Please review these gotchas before triggering the deployment:

### 1. Container Image Typos & Registry Accessibility
A simple typo in your `xmanager-cloud-deployment.yaml` image paths (e.g., `vars.xid_image`, `vars.ui_image`) will succeed at compile-time but cause an `ImagePullBackOff` failure 40 minutes later once GKE attempts to start the pods.
*   **Action**: Verify that all image tags are fully built and present in your container registry.
*   **Quick Check**: You can assert accessibility in seconds with: `gcloud artifacts docker images describe <IMAGE_URI> --quiet`

### 2. Cross-Project Registry Access Permissions
*   **Same-Project (Default)**: The GKE cluster and the Artifact Registry are assumed to reside in the same GCP project.
*   **Cross-Project**: If your container images are stored in a **different project** than `vars.project_id`, the GKE Node Service Account (`xmc-gke-sa-<env>@<project_id>.iam.gserviceaccount.com`) must be explicitly granted the **`roles/artifactregistry.reader`** role on the registry project *prior* to deployment. Without this permission, GKE will fail to pull the microservice containers.

### 3. Terminal Session Protection (Resilient Deployments)
The end-to-end provisioning sequence takes between **30 to 45 minutes**. If your terminal session is interrupted (due to a workstation going to sleep, undocking, or a VPN drop), the GCluster process will terminate mid-execution, corrupting the Terraform state and leaving orphaned load balancers, VPC subnets, or GKE nodes.
*   **Action**: It is highly recommended to execute GCluster inside a persistent session (e.g., **`tmux`**) or to run the deploy command detached in the background using **`nohup`**.

---

## 🏁 Phase 3: How to Deploy

Provisioning with the Cluster Toolkit is split into two phases: **compilation** (generating deployment state files) and **provisioning** (executing the infrastructure setup).

### Step 1: Install GCluster tool

See latest docs on [https://docs.cloud.google.com/cluster-toolkit/docs/overview](https://docs.cloud.google.com/cluster-toolkit/docs/overview)

### Step 2: Compile the Blueprint
Navigate into the toolkit directory and compile the blueprints to generate the deployment state:
```bash
cd xmc_ctk/
./gcluster create xmanager-cloud.yaml -d xmanager-cloud-deployment.yaml -l WARNING -w --force
```
*(This will compile the modules and create a dedicated workspace directory named after your `blueprint_name`)*.

### Step 3: Provision the Infrastructure
Execute the deployment to begin provisioning:
```bash
./gcluster deploy xmanager-on-cloud
```

> [!NOTE]
> **Interactive Approval**: The Cluster Toolkit provisions resources in sequential groups (`apis`, `primary`, `compute`, `kubernetes_prereqs`, `xmc_ingress`). As each phase generates its plan, the CLI will pause. **Type `a` and press Enter** to approve each group's rollout.

> [!TIP]
> **Session Safety Tip (Background Execution)**: To run GCluster completely unattended and protect against terminal drops, you can run the deployment in the background:
> ```bash
> nohup ./gcluster deploy xmanager-on-cloud --auto-approve > deploy.log 2>&1 &
> ```
> You can safely close your terminal and monitor progress at any time by running `tail -f deploy.log`.

---

## 🧹 Phase 4: How to Destroy

To safely tear down all provisioned infrastructure and clean up cloud resources, run the destroy command from within the toolkit folder:

```bash
./gcluster destroy xmanager-on-cloud --robust
```
*   Just like during deployment, approve the deletion of each group by typing `a` and pressing Enter when prompted.
*   Alternatively, force unattended cleanup by appending the `--auto-approve` flag.

> [!TIP]
> **Session Safety Tip (Background Teardown)**: Like deployment, resource teardown can take 15 to 25 minutes. To prevent terminal drops from interrupting teardown, you can run the destroy command in the background:
> ```bash
> nohup ./gcluster destroy xmanager-on-cloud --robust --auto-approve > destroy.log 2>&1 &
> ```
> Monitor the teardown logs live anytime via `tail -f destroy.log`.

---

## 🔍 Post-Deployment Verification

Once deployment completes successfully, execute the post-deployment verification script to assert functional health:

```bash
./verify_xmc.sh
```
This script validates pod readiness, external load balancer IP bindings, and curl endpoints to verify end-to-end system connectivity and health.
