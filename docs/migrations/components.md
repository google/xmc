# Migrating Legacy Metrics Workloads to XMC Components (`stable-2026a`)

This runbook guides platform operators through the one-time, zero-downtime migration of an existing XMC installation from legacy umbrella-managed metrics workloads (`enable_components: false` / `componentsDeployment.enabled=false`) to the `stable-2026a` Component profile (`enable_components: true` / `componentsDeployment.enabled=true`).

> [!IMPORTANT]
> Complete this migration in Release `N` before upgrading to Release `N+1`, where legacy non-component metrics templates are slated for removal.

---

## Prerequisites

- **Helm `>= 3.14.0`** (required for `--reset-then-reuse-values`), `kubectl`, and `python3` with cluster admin access to the target namespace (default: `xmc`).
- Updated XMC container images (including `xmc-bridge` and `ingestion-gateway`) built and pushed to your registry (see [INSTALL.md](../../INSTALL.md#step-2-build-and-push-updated-container-images)).

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
- **Standalone Cluster Toolkit (`gcluster`)**: Set `enable_components: true` in your deployment YAML, re-render and run `./cluster-toolkit/gcluster deploy ... --only xmc_app`, then execute Step 4 manually.
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
