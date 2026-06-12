---
name: Deploy XMC
description: Automates compiling and step-by-step deployment of the XMC v2 Cluster Toolkit blueprint, including pre-flight checks, DNS pause prompts, and verification.
---

# Deploy XMC Workflow

This skill guides an agent through deploying the XManager on Cloud (XMC) infrastructure using the Cluster Toolkit v2 blueprint. 

It handles validation of local CLI dependencies (pre-flight checks), sequential deployment of resource groups, pausing for manual DNS updates, and automated verification of kubernetes readiness.

---

## 1. Pre-flight CLI Checks

Before running any compilation or deployment steps, verify that all necessary CLI tools are installed and operational:

| CLI Tool | Verification Command | Expected Successful Outcome |
| :--- | :--- | :--- |
| **`gcloud`** | `gcloud --version` | Prints Google Cloud SDK version info |
| **`kubectl`** | `kubectl version --client` | Prints Kubernetes client version |
| **`helm`** | `helm version` | Prints Helm client version |
| **`terraform`** | `terraform --version` | Prints Terraform version (minimum 1.12.2) |
| **`ghpc`** | `./cluster-toolkit/ghpc version` | Prints gcluster/ghpc tool version |

If any check fails (command not found or returns error):
1. **Stop execution immediately**.
2. Alert the user about the missing/broken CLI dependency.
3. Suggest instructions for installation.

---

## 2. Blueprint Compilation

The blueprint relies on local Helm charts, database migrations, and proto descriptors.

### Step 1: Copy Protobuf Descriptors
Ensure that `ess_messages.pb` and `xid_messages.pb` are copied to the `deploy/` staging directory:

```bash
cp ./internal/storage/spanner/emulator/ess_messages.pb ./deploy/
cp ./internal/storage/spanner/emulator/xid_messages.pb ./deploy/
```

### Step 2: Compile the Blueprint
Compile the blueprint, specifying the overrides config file (e.g. `deploy/ctk_v2/xmanager-cloud-deployment.yaml`) using the `-d` flag:

```bash
./cluster-toolkit/ghpc create deploy/ctk_v2/xmanager-cloud.yaml -d <deployment_config_path> -l WARNING -w --force
```
Confirm that compilation completes successfully and generates the deployment folder `xmc-v2-run` containing the Terraform workspaces.

---

## 3. Step-by-Step Deployment

To allow monitoring and manual intervention, deploy the blueprint groups sequentially:

### Step 1: Enable APIs (`apis` group)
```bash
./cluster-toolkit/ghpc deploy xmc-v2-run --only apis --auto-approve
```

### Step 2: Deploy Core Infrastructure & IP (`primary` group)
```bash
./cluster-toolkit/ghpc deploy xmc-v2-run --only primary --auto-approve
```

### Step 3: DNS Pause & IP Mapping
1. Extract the allocated Gateway global static IP from GCP:
   - Identify the environment name (e.g. `xmcv2`) and GCP project ID from the deployment config.
   - Run the command to retrieve the IP address:
     ```bash
     gcloud compute addresses describe xmc-gateway-static-ip-<environment> --global --project=<project_id> --format='value(address)'
     ```
2. Prompt the user for manual DNS mapping and OAuth configuration:
   - Inform the user that the IP has been provisioned.
   - Present a clear markdown warning asking them to update both their DNS records and Google OAuth client settings:
     > [!IMPORTANT]
     > **Manual Configuration Required**
     >
     > 1. **DNS Routing Configuration**:
     >    Please configure the following A records in your authoritative DNS project for the Gateway IP **`<GATEWAY_IP>`**:
     >
     >    | Record Type | Hostname | Target IP |
     >    |:---:|---|---|
     >    | **A** | `<environment>.example.com` | `<GATEWAY_IP>` |
     >    | **A** | `api.<environment>.example.com` | `<GATEWAY_IP>` |
     >    | **A** | `grpc.<environment>.example.com` | `<GATEWAY_IP>` |
     >
     > 2. **OAuth Authorized Redirect URI**:
     >    Please ensure you add the following Redirect URI to your Google Cloud Console OAuth Web Client ID (`<client_id>`) configuration:
     >    * **`https://api.<environment>.example.com/api/callback`**
     >
     > Once these DNS records are created and the redirect URI is registered in the GCP console, reply to this conversation to proceed with the remaining deployment steps.
3. **Stop executing tools** and wait for the user to confirm.

### Step 4: Deploy GKE Cluster & Node Pools (`compute` group)
```bash
./cluster-toolkit/ghpc deploy xmc-v2-run --only compute --auto-approve
```

### Step 5: Install GKE System Helm Charts (`kubernetes_prereqs` group)
```bash
./cluster-toolkit/ghpc deploy xmc-v2-run --only kubernetes_prereqs --auto-approve
```

### Step 6: Deploy XMC Application (`xmc_app` group)
```bash
./cluster-toolkit/ghpc deploy xmc-v2-run --only xmc_app --auto-approve
```

---

## 4. Verification and Validation

Verify the health and routing of the environment:

### Step 1: Run Verification Script
```bash
./deploy/ctk_v2/verify_xmc.sh
```

### Step 2: Gateway Status Check
Poll until the GKE Gateway status reports `Programmed: True`:
```bash
kubectl get gateway xmc-gateway -n xmc -o jsonpath='{.status.conditions[?(@.type=="Programmed")].status}'
```

### Step 3: GTS Certificate Check
Poll until the Google-managed certificate state reports `ACTIVE`:
```bash
gcloud certificate-manager certificates describe xmc-cert-<environment> --project=<project_id> --format='value(managed.state)'
```

Once all checks pass, report the finalized deployment URL (e.g. `https://<environment>.example.com`) to the user.
