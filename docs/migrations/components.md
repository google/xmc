# Migrating Legacy Metrics Workloads to XMC Components (`stable-2026a`)

This runbook guides platform operators through the one-time, zero-downtime migration of an existing XMC installation from legacy umbrella-managed metrics workloads (`enable_components: false` / `componentsDeployment.enabled=false`) to the `stable-2026a` Component profile (`enable_components: true` / `componentsDeployment.enabled=true`).

> [!IMPORTANT]
> **Supported XMC Versions (`from` -> `target`)**:
> - **Source (`from`)**: Any existing XMC installation running legacy umbrella mode (`enable_components: false` / `componentsDeployment.enabled=false`) — including `0.4.0`–`0.17.x` deployments with live `ingestion-gateway` workloads (and `0.18.x` clusters that have not yet enabled components). Pre-`0.4.0` clusters or installations where `ingestionGatewayDeployment.enabled=false` are also handled safely (`handoff` is a no-op).
> - **Target (`to`)**: XMC **`0.18.0` – `0.18.x`**. Targets `<= 0.17.0` are unsupported (`0.17.0` shipped prior to this migration CLI, and `<= 0.16.0` lacks `stable-2026a` `ingestion-gateway` parameter/schema parity, multi-broker resolution, and `--profile-values` support). Complete this cutover on `0.18.x` before upgrading to `0.19.0`, where legacy non-component metrics templates are slated for removal.

---

## Prerequisites

- **Helm `>= 3.14.0`** (required for `--reset-then-reuse-values`), `kubectl`, and `python3` in an **interactive terminal** with cluster admin access to the target namespace (default: `xmc`). Each `migrate.py` subcommand verifies the source cluster and local codebase match the `0.18.x` transition window and prompts for interactive `yes` confirmation on `stdin` (non-TTY invocations abort automatically).
- Updated XMC container images (including `xmc-bridge` and `ingestion-gateway`) built and pushed to your registry (see [INSTALL.md](../../INSTALL.md#step-2-build-and-push-updated-container-images)).
- **`xmc-bridge` Workload Identity binding (older Cluster Toolkit deployments)**: If your cluster was provisioned before `module.xmc_wi_binding_xmc_bridge` was added to the `compute` group (and you upgrade only `xmc_app`), ensure `<namespace>/xmc-bridge-sa` holds `roles/iam.workloadIdentityUser` on `<deployment_name>-xmc-wl-sa@<project_id>.iam.gserviceaccount.com` (for example via `terraform -chdir=ctk-out/<deployment_name>/compute apply -target=module.xmc_wi_binding_xmc_bridge` after reviewing `terraform plan`).

---

## Step-by-Step Cutover

### Step 1: Generate and Validate `profile-values.yaml`

Extract your live umbrella Helm values into a `stable-2026a` profile values file and validate it against [`deploy/profiles/stable-2026a/values.schema.json`](../../deploy/profiles/stable-2026a/values.schema.json) (replace `-r xmc` throughout with `xmc-<deployment_name>-<environment>` when your deployment was created via Cluster Toolkit):

```bash
./tools/migrations/components/migrate.py prepare \
  -n xmc \
  -r xmc \
  -o profile-values.yaml
```

Inspect `profile-values.yaml` and confirm image tags, broker/ClickHouse endpoints, HPA bounds, and secret names match your environment. Optionally run the static component linter against the rendered `Component` CRs:

```bash
mkdir -p /tmp/xmc-rendered-profile
helm template xmc-profile deploy/profiles/stable-2026a -n xmc -f profile-values.yaml > /tmp/xmc-rendered-profile/profile.yaml
bazel run //cmd/component-linter -- \
  --manifests-dir=/tmp/xmc-rendered-profile \
  --rules-config="$(pwd)/deploy/profiles/stable-2026a/rules.yaml"
```

### Step 2: Hand Off Live `ingestion-gateway` Helm Ownership

Before upgrading the umbrella release with `enable_components=true`, stamp `helm.sh/resource-policy=keep` and transfer `meta.helm.sh/release-name` to `xmc-cmp-ingestion-gateway` on the live `Deployment`, `Service`, and optional `HorizontalPodAutoscaler` (while keeping `<release>-ingestion-gateway-network-policy` in place until replacement):

```bash
./tools/migrations/components/migrate.py handoff -n xmc -r xmc
```

*(If you need to abort before Step 3, run `./tools/migrations/components/migrate.py handoff --undo -n xmc -r xmc`.)*

### Step 3: Upgrade the XMC Umbrella Release (`enable_components=true`)

Upgrade your XMC umbrella deployment so `xmc-bridge` starts and `ess`, `dashboard-service`, and `ingestion-gateway` enable component discovery without pruning your live gateway pods:

- **`dev_env.py gke-upgrade`**: Set `enable_components: true` in your deployment YAML, then pass `--profile-values "<absolute-path-to>/profile-values.yaml"` to `python3 ./tools/dev_env/dev_env.py gke-upgrade -c <deployment.yaml> --enable-components --profile-values "<absolute-path-to>/profile-values.yaml"` (this automatically executes Step 4 via `deploy_component_profile`).
- **Standalone Cluster Toolkit (`gcluster`)**: Set `enable_components: true` in your deployment YAML, re-render the blueprint and deploy the `xmc_app` group, then execute Step 4 manually:
  ```bash
  ./cluster-toolkit/gcluster create deploy/ctk_v2/xmanager-cloud.yaml -d <deployment_config_path> -o ctk-out -l WARNING -w --force
  ./cluster-toolkit/gcluster deploy ctk-out/<deployment_name> --only xmc_app --auto-approve
  ```
- **Direct Helm**:
  ```bash
  helm upgrade --install xmc ./deploy/helm \
    -n xmc \
    -f my-values.yaml \
    --set componentsDeployment.enabled=true \
    --wait
  ```

### Step 4: Install the `stable-2026a` Component Profile

If you upgraded via standalone `gcluster` or direct Helm in Step 3 (rather than `dev_env.py gke-upgrade --profile-values`), install or upgrade `xmc-profile` so `xmc-bridge` reconciles `redpanda`, `clickhouse`, and `ingestion-gateway` and adopts `xmc-cmp-ingestion-gateway`:

```bash
helm upgrade --install --reset-then-reuse-values xmc-profile ./deploy/profiles/stable-2026a \
  -n xmc \
  -f profile-values.yaml \
  --wait
```

### Step 5: Finalize Cutover and Verify Ingestion

Wait for `Component/ingestion-gateway` to report `Ready` and `Synced`, verify `xmc-cmp-ingestion-gateway` is `deployed`, strip the temporary `helm.sh/resource-policy=keep` annotations, and delete the superseded `<release>-ingestion-gateway-network-policy`:

```bash
./tools/migrations/components/migrate.py finalize -n xmc -r xmc
```

Optionally verify live gateway readiness via a local port-forward:

```bash
kubectl port-forward -n xmc svc/ingestion-gateway-service 18080:8080 &
PF_PID=$!
sleep 2
curl -fsS http://127.0.0.1:18080/readyz
kill "${PF_PID}"
```

---

## After Migration: Upgrading XMC

`xmc-profile` is a separate Helm release: `gcluster deploy` and umbrella `helm upgrade` do not upgrade it. `prepare` also pins the gateway image that was running at migration time (`components.ingestion-gateway.imageTag`, plus `components.ingestion-gateway.parameters.image` when your umbrella values set a custom gateway image), and these take precedence over the default image `xmc-bridge` would otherwise use. Keep `profile-values.yaml` alongside your deployment configuration and, on every later XMC upgrade:

1. Keep `enable_components: true` (`componentsDeployment.enabled=true`) and upgrade the umbrella release as usual (see [INSTALL.md](../../INSTALL.md#step-4-upgrade-application-workloads)).
2. In `profile-values.yaml`, update `components.ingestion-gateway.imageTag` and, if present, the tag in `components.ingestion-gateway.parameters.image` to match the new `<IMAGE_TAG>` (the same tag used for the umbrella upgrade).
3. Re-run the Step 4 `helm upgrade --install --reset-then-reuse-values xmc-profile ...` command from the new release checkout (`dev_env.py gke-upgrade --profile-values` does this automatically).

---

## Rollback Procedure

To revert back to legacy umbrella ownership without dropping live `ingestion-gateway` pods:

1. **Orphan workloads and remove `Component` CRs**:
   ```bash
   helm upgrade xmc-profile ./deploy/profiles/stable-2026a \
     -n xmc \
     -f profile-values.yaml \
     --set components.ingestion-gateway.deletionPolicy=Orphan \
     --set components.ingestion-gateway.deletionProtection=false \
     --set components.redpanda.deletionProtection=false \
     --set components.clickhouse.deletionProtection=false \
     --wait
   helm uninstall xmc-profile -n xmc --wait
   ```

2. **Restore umbrella Helm ownership annotations** (this keeps live pods running while removing the orphaned `xmc-cmp-ingestion-gateway` Helm release record):
   ```bash
   ./tools/migrations/components/migrate.py handoff --undo -n xmc -r xmc
   ```

3. **Re-enable legacy umbrella management and clean up component-only RBAC/NetworkPolicy**:
   ```bash
   helm upgrade xmc ./deploy/helm \
     -n xmc \
     -f my-values.yaml \
     --set componentsDeployment.enabled=false \
     --wait
   kubectl delete -n xmc --ignore-not-found \
     role/ingestion-gateway-components-reader \
     rolebinding/ingestion-gateway-components-reader-binding \
     networkpolicy/ingestion-gateway-network-policy
   ```
