# XManager on Cloud (XMC) v2 Installation Guide (Cluster Toolkit)

This guide provides step-by-step instructions for deploying the XManager on Cloud (XMC) infrastructure using the HPC Cluster Toolkit (`gcluster`/`ghpc`). It is based on the actual manual steps, troubleshooting actions, and workarounds performed during deployment.

---

## Prerequisites

Before starting the deployment, ensure the following CLI tools are installed on your local workstation and authenticated:

| CLI Tool | Required Version | Verification Command |
| :--- | :--- | :--- |
| **`gcloud`** | Latest | `gcloud --version` |
| **`kubectl`** | Latest | `kubectl version --client` |
| **`bazel`** | Latest | `bazel --version` |
| **`gcluster`** | v1.94+ | `./cluster-toolkit/gcluster --version` |

Make sure you are authenticated to the target GCP project:
```bash
gcloud auth login
gcloud config set project <TARGET_PROJECT_ID>
```

---

## Step 1: Configure Deployment Overrides

Create an overrides file named `deploy/ctk_v2/xmanager-cloud-deployment.yaml` (you can copy the template at `deploy/ctk_v2/xmc-deployment-template.yaml` as a starting point).

### Image Paths Override
Verify that the Docker image names/paths in your deployment config match the names in your Artifact Registry (typically using `-dev` suffixes for development environments):
```yaml
vars:
  project_id: <TARGET_PROJECT_ID>
  region: us-central1
  environment: dev
  domain_name: fatoshoti.com

  # Image paths
  xid_image: "us-central1-docker.pkg.dev/<PROJECT_ID>/xmc/xid-service-dev:latest"
  actuator_image: "us-central1-docker.pkg.dev/<PROJECT_ID>/xmc/actuator-dev:latest"
  ess_image: "us-central1-docker.pkg.dev/<PROJECT_ID>/xmc/experiment-state-server-dev:latest"
  ui_image: "us-central1-docker.pkg.dev/<PROJECT_ID>/xmc/ui-dev:latest"
  reconciler_image: "us-central1-docker.pkg.dev/<PROJECT_ID>/xmc/reconciler-dev:latest"
  log_monitor_image: "us-central1-docker.pkg.dev/<PROJECT_ID>/xmc/log-monitor-dev:latest"
```

---

## Step 2: Build and Stage Protobuf Descriptors

XMC requires protobuf descriptors to compile and run database schemas:

1. Build the proto files using Bazel:
   ```bash
   bazel build //internal/storage/spanner/emulator:copy_ess_messages //internal/storage/spanner/emulator:copy_xid_messages
   ```

2. Copy the generated files to the `deploy/` directory:
   ```bash
   cp bazel-bin/internal/storage/spanner/emulator/ess_messages.pb ./deploy/
   cp bazel-bin/internal/storage/spanner/emulator/xid_messages.pb ./deploy/
   ```

---

## Step 3: Handle Organization Policy Constraints (Internal Load Balancer)

If your Google Cloud project enforces the organization policy constraint **`constraints/compute.restrictLoadBalancerCreationForTypes`** restricting external load balancers, you must deploy GKE Gateway using an internal class (`gke-l7-rilb`).

1. **Create a regional proxy-only subnet** in the VPC network (required by GKE internal Gateway load balancers):
   ```bash
   gcloud compute networks subnets create xmc-proxy-only-subnet \
       --purpose=REGIONAL_MANAGED_PROXY \
       --role=ACTIVE \
       --region=<REGION> \
       --network=<NETWORK_NAME> \
       --range=10.129.0.0/23 \
       --project=<PROJECT_ID>
   ```

2. **Modify the Gateway configuration** in `deploy/ctk_v2/xmanager-cloud.yaml` to switch to internal routing and disable public GTS SSL cert maps:
   ```yaml
         - name: certs.type
           value: "self-signed"  # Use self-signed certs (GTS maps do not support private IPs)
         - name: gateway.className
           value: "gke-l7-rilb"  # Switch Gateway class to Regional Internal
         - name: gateway.staticIp
           value: ""             # Let GKE allocate an ephemeral internal IP
         - name: gateway.certMap
           value: ""             # Disable cert map
   ```

---

## Step 4: Compile the Blueprint

Compile the Cluster Toolkit blueprint to generate Terraform workspaces and Helm values:
```bash
./cluster-toolkit/gcluster create deploy/ctk_v2/xmanager-cloud.yaml \
    -d deploy/ctk_v2/xmanager-cloud-deployment.yaml \
    -o ctk-out \
    -l WARNING -w --force
```

---

## Step 5: Step-by-Step Deployment

Run the deployment groups in sequence to monitor resources:

### 1. Deploy APIs (`apis` group)
```bash
./cluster-toolkit/gcluster deploy ctk-out/my-xmc-ctk --only apis --auto-approve
```

### 2. Deploy Core Infrastructure (`primary` group)
```bash
./cluster-toolkit/gcluster deploy ctk-out/my-xmc-ctk --only primary --auto-approve
```

### 3. DNS Configuration & Pause
1. Retrieve the static/internal Gateway IP address:
   ```bash
   gcloud compute forwarding-rules list --project=<PROJECT_ID>
   ```
   *(Note down the IP of the newly created forwarding rule, e.g. `10.0.0.11`)*

2. Map DNS hostnames in your DNS server or local workstation `/etc/hosts` file to resolve to the Gateway IP:
   ```text
   <GATEWAY_IP> dev.fatoshoti.com api.dev.fatoshoti.com grpc.dev.fatoshoti.com
   ```

3. Update the authorized Redirect URI in the Google Cloud Console OAuth 2.0 Web Client Credentials:
   * **`https://api.dev.fatoshoti.com/api/callback`**

---

### 4. Deploy Compute Cluster (`compute` group)

If GKE already contains preexisting CRDs (e.g. `inferencepools.inference.networking.k8s.io`), Helm may fail with an ownership conflict. To resolve this:

1. Get the credentials for the newly created GKE cluster:
   ```bash
   gcloud container clusters get-credentials xmc-my-xmc-ctk-dev --region <REGION> --project <PROJECT_ID>
   ```

2. Annotate and label the CRD to let Helm adopt it:
   ```bash
   kubectl label crd inferencepools.inference.networking.k8s.io app.kubernetes.io/managed-by=Helm --overwrite
   kubectl annotate crd inferencepools.inference.networking.k8s.io meta.helm.sh/release-name=manifest-manifests-8f14584 --overwrite
   kubectl annotate crd inferencepools.inference.networking.k8s.io meta.helm.sh/release-namespace=default --overwrite
   ```

3. Run the compute group deployment:
   ```bash
   ./cluster-toolkit/gcluster deploy ctk-out/my-xmc-ctk --only compute --auto-approve
   ```

---

### 5. Install GKE System Helm Charts (`kubernetes_prereqs` group)
```bash
./cluster-toolkit/gcluster deploy ctk-out/my-xmc-ctk --only kubernetes_prereqs --auto-approve
```

---

### 6. Workload Identity configuration for `xid-service`
The `xid-service` pod needs authorization to use GKE Workload Identity to connect to Cloud Spanner:

1. Add the IAM policy binding to link the Kubernetes service account to the GCP Workload Identity IAM service account:
   ```bash
   gcloud iam service-accounts add-iam-policy-binding <GCP_WI_SERVICE_ACCOUNT_EMAIL> \
       --role="roles/iam.workloadIdentityUser" \
       --member="serviceAccount:<PROJECT_ID>.svc.id.goog[xmc/xid-service]" \
       --project=<PROJECT_ID>
   ```

2. Restart the deployment pods so they fetch the token:
   ```bash
   kubectl rollout restart deployment xid-service-deployment -n xmc
   ```

---

### 7. Deploy XMC Application (`xmc_app` group)
```bash
./cluster-toolkit/gcluster deploy ctk-out/my-xmc-ctk --only xmc_app --auto-approve
```

---

## Step 6: Post-Deployment Verification

Verify the health, certificate readiness, and routing of the environment:

1. Run the verification script:
   ```bash
   ./deploy/ctk_v2/verify_xmc.sh
   ```

2. Verify that the Gateway status has successfully bound the IP address and is programmed:
   ```bash
   kubectl get gateway xmc-gateway -n xmc
   ```

3. Check if all pods in the `xmc` namespace are in `Running` and `1/1` ready state:
   ```bash
   kubectl get pods -n xmc
   ```

---

## Step 7: How to Access the UI

Since the gateway is internal, you can access the UI by mapping the domains to your local `/etc/hosts` file (refer to Step 5.3). 

* **Direct access**: Open **`https://dev.fatoshoti.com`** in your browser. (Accept the self-signed SSL certificate warning to proceed).
* **Port Forwarding (for debugging without DNS/VPC access)**:
  ```bash
  kubectl port-forward svc/ui-service -n xmc 8080:80
  ```
  And open **`http://localhost:8080`**.
