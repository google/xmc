# Migrating Legacy Metrics Workloads to XMC Components (`stable-2026a`)

This runbook guides platform operators through the one-time, zero-downtime migration of a pre-`1.0.0` XMC installation from legacy umbrella-managed metrics workloads (`enable_components: false` / `componentsDeployment.enabled=false`) to the `stable-2026a` Component profile (`xmc-profile`).

> [!IMPORTANT]
> **Supported XMC Versions (`from` -> `target`)**:
> - **Source (`from`)**: Any pre-`1.0.0` XMC installation running legacy umbrella mode (`enable_components: false` / `componentsDeployment.enabled=false`, including `0.4.0`–`0.18.x` deployments with live `ingestion-gateway` workloads, plus pre-`0.4.0` or gateway-disabled clusters where `handoff` is a no-op).
> - **Target (`to`)**: XMC **`0.18.0` – `0.18.x` or `1.0.0+` (`1.x`)**. Targets `<= 0.17.0` are unsupported (`0.17.0` shipped prior to this migration CLI, and `<= 0.16.0` lacks `stable-2026a` `ingestion-gateway` parameter/schema parity, multi-broker resolution, and `--profile-values` support). In `1.0.0+`, XMC Components are mandatory and legacy non-component `ingestion-gateway` templates are removed (passing `componentsDeployment.enabled=false` or `ingestionGatewayDeployment.enabled=true` fails closed), so unmigrated `< 1.0.0` clusters with live `ingestion-gateway` workloads **must** run `prepare` -> `handoff` **before** upgrading the umbrella release to `1.0.0+`.

---

## Prerequisites

- **Helm `>= 3.14.0`** (required for `--reset-then-reuse-values`), `kubectl`, and `python3` in an **interactive terminal** with cluster admin access to the target namespace (default: `xmc`). Each `migrate.py` subcommand verifies the source cluster and local target codebase (`0.18.x` or `1.x`) and prompts for interactive `yes` confirmation on `stdin` (non-TTY invocations abort automatically).
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

Before upgrading the umbrella release, stamp `helm.sh/resource-policy=keep` and transfer `meta.helm.sh/release-name` to `xmc-cmp-ingestion-gateway` on the live `Deployment`, `Service`, and optional `HorizontalPodAutoscaler` (while keeping `<release>-ingestion-gateway-network-policy` in place until replacement):

```bash
./tools/migrations/components/migrate.py handoff -n xmc -r xmc
```

*(If you need to abort before Step 3, run `./tools/migrations/components/migrate.py handoff --undo -n xmc -r xmc`.)*

### Step 3: Upgrade the XMC Umbrella Release

On **`1.0.0+`**, XMC Components are enabled automatically (no `enable_components: true` or `--set componentsDeployment.enabled=true` flag is needed). Before upgrading, **remove** any legacy `enable_components: false`, `componentsDeployment.enabled: false`, or `ingestionGatewayDeployment` block (direct Helm upgrades fail closed on `componentsDeployment.enabled: false` and `ingestionGatewayDeployment.enabled: true`), as well as the retired `enable_metrics`, `ingestion_gateway_deployment_enabled`, `clickhouse_{host,port,database,user}` deployment-YAML keys (rejected by `tools/dev_env`; standalone `gcluster create` only warns about unused variables) and the retired `metrics.{enabled,clickhouse.{host,port,database,user}}` Helm values (retaining `clickhouse_password` / `metrics.clickhouse.{secretName,password}` for ClickHouse credentials) from your `xmanager-cloud-deployment.yaml` or `my-values.yaml`. Also verify two credential and profile migration requirements before running the upgrade:

- **Secret Manager CSI ClickHouse Password**: `secrets.csi.clickhousePasswordSecretName` now defaults to `""`. CSI releases (`secrets.csi.enabled: true`) that read the ClickHouse password from Secret Manager **must** set `secrets.csi.clickhousePasswordSecretName` explicitly (previously implied `"clickhouse-password"`) in `my-values.yaml` before upgrading; otherwise `dashboard-service` falls back to `Secret/clickhouse-secret`.
- **Partially Migrated `0.18.x` Releases (`Component/clickhouse` Still Disabled)**: Operators on `0.18.x` who already run XMC Components (`xmc-bridge` active, where `migrate.py prepare` refuses to run) but kept `Component/clickhouse` disabled while relying on custom umbrella `metrics.clickhouse.{host,port,database,user}` values must move those settings into `components.clickhouse.parameters` (`host`, `nativePort`, `defaultDatabase`, `user`, `secretName`) in `xmc-profile` and set `components.clickhouse.enabled: true` (waiting until `xmc-bridge` reconciles it and restarting once if `xmc-profile` is applied after `dashboard-service` starts: `kubectl wait component.xmc.dev/clickhouse -n xmc --for=condition=Ready --timeout=120s && kubectl rollout restart deployment/dashboard-service-deployment -n xmc`); otherwise `GetChartData` returns `FailedPrecondition: data engine is not configured` after upgrading.

Then upgrade the umbrella release so `xmc-bridge` starts and `ess`, `dashboard-service`, and `ingestion-gateway` use component discovery without pruning your live gateway pods:

- **`dev_env.py gke-upgrade`**: Remove any legacy `enable_components: false` from your deployment YAML, then run `python3 ./tools/dev_env/dev_env.py gke-upgrade -c <deployment.yaml> --profile-values "<absolute-path-to>/profile-values.yaml"` (this automatically executes Step 4 via `deploy_component_profile`; if targeting `0.18.x`, also set `enable_components: true`).
- **Standalone Cluster Toolkit (`gcluster`)**: Remove any legacy `enable_components: false` from your deployment YAML *(or set `enable_components: true` if targeting `0.18.x`)*, clear any stale staged templates, re-render the blueprint, and deploy the `xmc_app` group, then execute Step 4 manually:
  ```bash
  rm -rf ctk-out/<deployment_name>/.ghpc/staged
  ./cluster-toolkit/gcluster create deploy/ctk_v2/xmanager-cloud.yaml -d <deployment_config_path> -o ctk-out -l WARNING -w --force
  ./cluster-toolkit/gcluster deploy ctk-out/<deployment_name> --only xmc_app --auto-approve
  ```
- **Direct Helm**: Remove any legacy `componentsDeployment.enabled: false` or `ingestionGatewayDeployment` block from `my-values.yaml` *(or pass `--set componentsDeployment.enabled=true` if targeting `0.18.x`)*, then run:
  ```bash
  helm upgrade --install xmc ./deploy/helm \
    -n xmc \
    -f my-values.yaml \
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

1. Upgrade the umbrella release as usual (see [INSTALL.md](../../INSTALL.md#step-4-upgrade-application-workloads)).
2. In `profile-values.yaml`, update `components.ingestion-gateway.imageTag` and, if present, the tag in `components.ingestion-gateway.parameters.image` to match the new `<IMAGE_TAG>` (the same tag used for the umbrella upgrade).
3. If `xmc-profile` was previously installed on XMC `0.18.x` and you are upgrading directly with `helm` rather than `dev_env.py gke-upgrade --profile-values`:
   - Remove any `components.vizier` or `components.hpo` blocks from `profile-values.yaml` (if either experimental component was enabled on `0.18.x`, set `enabled: false` and run `helm upgrade xmc-profile` on `0.18.x` first so `xmc-bridge` uninstalls `xmc-cmp-hpo` cleanly).
   - Clear `deletionProtection` on any retired disabled `vizier` and `hpo` `Component` CRs so Helm can prune them cleanly:
     ```bash
     for cr in vizier hpo; do
       if [ "$(kubectl get components.xmc.dev "${cr}" -n xmc -o jsonpath='{.spec.enabled}' 2>/dev/null)" = "false" ]; then
         kubectl patch -n xmc --type=merge -p '{"spec":{"deletionProtection":false}}' -- components.xmc.dev "${cr}"
       fi
     done
     ```
   - If `helm get values xmc-profile -n xmc` previously recorded `vizier` or `hpo` keys, run the Step 4 `helm upgrade` once with `--reset-values -f profile-values.yaml` (including all active overrides in `profile-values.yaml`) instead of `--reset-then-reuse-values`.
4. Re-run the Step 4 `helm upgrade --install --reset-then-reuse-values xmc-profile ...` command from the new release checkout (`dev_env.py gke-upgrade --profile-values` unprotects retired disabled CRs and runs this upgrade automatically).

> [!NOTE]
> `--reset-then-reuse-values` keeps the values supplied to the previous `xmc-profile` release, so deleting an override from `profile-values.yaml` does not revert it on upgrade. To return a value to its chart default, set the default explicitly (for example `components.ingestion-gateway.parameters.topicName: "xmc-metrics"`).

---

## Rollback Procedure

> [!WARNING]
> Rolling back to legacy umbrella `ingestion-gateway` ownership is **only supported when downgrading to (or running on) XMC `<= 0.18.x`**, because XMC `1.0.0+` removes the legacy umbrella `ingestion-gateway` templates and fails closed when `componentsDeployment.enabled=false` or `ingestionGatewayDeployment.enabled=true` is passed.

To revert back to legacy umbrella ownership on XMC `<= 0.18.x` without dropping live `ingestion-gateway` pods:

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
