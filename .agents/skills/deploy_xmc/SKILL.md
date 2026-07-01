---
name: Deploy XMC
description: Automates compiling and step-by-step deployment of the XMC v2 Cluster Toolkit blueprint, including pre-flight checks, DNS updates, and verification.
---

# Deploy XMC Workflow

This skill guides the agent through deploying the XManager on Cloud (XMC) infrastructure using the automated `deploy_xmc.sh` script. The script handles compilation, GCP resource provisioning, DNS setup (either automated or manual), and container builds.

---

## 1. Pre-flight CLI Checks

Before running the deployment, verify that all necessary CLI tools are installed and operational:

| CLI Tool | Verification Command | Expected Successful Outcome |
| :--- | :--- | :--- |
| **`gcloud`** | `gcloud --version` | Prints Google Cloud SDK version info |
| **`kubectl`** | `kubectl version --client` | Prints Kubernetes client version |
| **`helm`** | `helm version` | Prints Helm client version |
| **`terraform`** | `terraform --version` | Prints Terraform version (minimum 1.12.2) |
| **`bazel`** | `bazel --version` | Prints Bazel version |
| **`ghpc`** | `./cluster-toolkit/ghpc --version` | Prints GHPC tool version |

> [!CAUTION]
> **Check Action**: If any check fails (command not found or returns error):
> 1. **Stop execution immediately**.
> 2. Alert the user about the missing/broken CLI dependency.
> 3. Provide installation instructions and do not proceed.

---

## 2. Pre-flight Verification

Always run the deployment script in check-only mode first to validate your active GCP project alignment, configurations, and `.env` file synchronization.

```bash
./deploy/ctk_v2/deploy_xmc.sh \
  --config <deployment_config_path> \
  --check-only
```

*Note: If you plan to use automated DNS, add `--dns-project <project>` and `--dns-zone <zone>` to the check command to verify Cloud DNS access.*

---

## 3. Deploying XMC

Run the script to execute the end-to-end deployment. Choose one of the two modes below based on the DNS setup.

### Mode A: Automated DNS (Google Cloud DNS)
Use this if the target domain's authoritative DNS zone is hosted on Google Cloud DNS and you have permissions to modify it.

```bash
./deploy/ctk_v2/deploy_xmc.sh \
  --config <deployment_config_path> \
  --dns-project <dns_project_id> \
  --dns-zone <dns_managed_zone_name>
```

### Mode B: Manual DNS (Custom DNS Providers)
Use this if your DNS is managed externally (e.g., Route53, GoDaddy) or you do not have write access to the GCP DNS zone.

1.  **Run the script without DNS arguments:**
    ```bash
    ./deploy/ctk_v2/deploy_xmc.sh --config <deployment_config_path>
    ```
2.  **Wait for the pause:** The script will deploy the core network, extract the Gateway IP, print the DNS/OAuth instructions, and **pause**.
3.  **Perform manual setup:** Configure your external DNS provider with the A records pointing to the printed IP, and add the Redirect URI in the Google Cloud Console.
4.  **Resume:** Press `Enter` in the terminal to continue the GKE and application deployment.

---

## 4. Post-Deployment OAuth Configuration

Once the deployment completes successfully:

1.  Instruct the user to register the Redirect URI in the Google Cloud Console OAuth Web Client ID config:
    *   **`https://<environment>.<domain_name>/api/callback`**
2.  Provide them with the environment URL:
    *   **`https://<environment>.<domain_name>`**

---

## 5. Error Handling & Resumption

If the script fails at any step, identify the failing step from the logs, fix the underlying issue, and resume from the failed step:

```bash
./deploy/ctk_v2/deploy_xmc.sh \
  --config <deployment_config_path> \
  --start-from <failed_step>
```

*Note: Add DNS arguments to the resume command if using Mode A.*

*Valid steps: `compile`, `apis`, `primary`, `compute`, `gateway-api`, `prereqs`, `images`, `app`, `verify`*
