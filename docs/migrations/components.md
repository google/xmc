<!--
Copyright 2026 Google LLC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
-->

# Migrating Legacy Ingestion Gateway Deployments to XMC Components

This runbook guides operators through the one-time, zero-downtime migration of an existing umbrella-managed `ingestion-gateway` workload to the XMC Components architecture (`stable-2026a` profile + [`xmc-bridge`](../../cmd/xmc_bridge/README.md)).

> [!NOTE]
> `enable_components` remains `false` by default in this release so operators can complete a standard XMC upgrade first and perform the gateway component migration in a separate, controlled maintenance window. Once migrated, future upgrades manage the gateway via the [`stable-2026a` profile](../../deploy/profiles/stable-2026a/README.md).

---

## How Zero-Downtime Adoption Works

```mermaid
sequenceDiagram
  participant Op as Operator
  participant K as Kubernetes Cluster
  participant B as xmc-bridge
  Op->>Op: 1. prepare: generate & lint profile-values.yaml from live cluster
  Op->>K: 2. handoff: annotate Deployment/Service/HPA (keep, owner=xmc-cmp-ingestion-gateway)
  Op->>K: 3. upgrade XMC with enable_components=true (Spanner migrations + umbrella apply)
  K-->>K: Umbrella omits legacy gateway templates; delete skipped (keep); pods keep serving
  Op->>K: 4. helm upgrade --install xmc-profile -f profile-values.yaml
  K->>B: Reconcile Component CRs (redpanda, clickhouse, ingestion-gateway)
  B->>K: helm install xmc-cmp-ingestion-gateway adopts kept objects & rolls out pods
  Op->>K: 5. finalize: wait Ready & rollout, verify zero drift, strip keep, probe metrics
```

Because the `ingestion-gateway` component chart uses the **same Kubernetes object names, Pod label selector, and Service** (`ingestion-gateway-deployment`, `ingestion-gateway-service`, and `ingestion-gateway-hpa`) as the legacy umbrella chart:

1. `helm.sh/resource-policy=keep` prevents Helm from deleting the live gateway objects when the umbrella chart is upgraded with `componentsDeployment.enabled=true` (`enable_components: true`).
2. `meta.helm.sh/release-name=xmc-cmp-ingestion-gateway` allows `xmc-bridge`'s standard `helm install` path to adopt the existing objects in-place via a normal rolling update—preserving the `Service` ClusterIP, GKE Gateway BackendPolicy/HTTPRoute bindings, and Network Endpoint Groups (NEGs).

---

## Prerequisites

Verify the following on your operator workstation before starting:

- **CLI Versions**:
  - `kubectl` authenticated against the target GKE cluster.
  - `helm` **>= `3.14.0`** (required because `deploy/helm` renders a Kubernetes `ValidatingAdmissionPolicy` for component deletion protection; older Helm 3.10–3.13 clients fail OpenAPI discovery).
  - `python3` (`>= 3.11`) or `bazel` to run [`tools/migrations/components/migrate.py`](../../tools/migrations/components/migrate.py) and `component-linter`.
- **Container Images Pushed**: Both updated `ingestion-gateway` and **`xmc-bridge`** OCI images must be built and pushed to your Artifact Registry repository for your target `<IMAGE_TAG>` (see [INSTALL.md](../../INSTALL.md#step-2-build-and-push-updated-container-images)).
- **Spanner Migrations Applied**: Cloud Spanner `ess` (including Component Management Service tables) and `xid` migrations must be applied during Step 3 (see [INSTALL.md](../../INSTALL.md#step-3-run-database-schema-migrations)).

Set shell variables for your environment:

```bash
export XMC_RELEASE="xmc"        # Umbrella Helm release name
export XMC_NAMESPACE="xmc"      # Target Kubernetes namespace
```

---

## Migration Sequence

### Step 1: Prepare and Validate `profile-values.yaml`

Run `migrate.py prepare` against the live legacy cluster before changing any cluster state:

```bash
python3 ./tools/migrations/components/migrate.py prepare \
  --release "${XMC_RELEASE}" \
  --namespace "${XMC_NAMESPACE}" \
  --output profile-values.yaml
```

What `prepare` does:
1. Verifies `helm >= 3.14.0` and reads `helm get values <release> -a` plus the live `ingestion-gateway-deployment` and optional `ingestion-gateway-hpa`.
2. **Fails fast** if the live gateway contains unmapped customizations (`initContainers`, sidecars, custom `env`/`envFrom` such as `MESSAGE_BUS=pubsub`, extra volumes, or unsupported CLI flags).
3. Maps image, replicas/HPA bounds, CPU/memory requests & limits, `serviceAccountName`, `projectId`, `redpandaBrokers`, `topicName`, SASL/TLS settings, and ClickHouse settings into `profile-values.yaml`, and verifies referenced SASL/TLS/ClickHouse `Secret`s exist.
4. Validates `profile-values.yaml` offline via `helm template` (JSON Schema) and `component-linter`.

> [!IMPORTANT]
> Inspect `profile-values.yaml` before proceeding. Confirm `components.redpanda.parameters.brokerEndpoints`, `components.clickhouse.parameters` (`host`, `nativePort`, `user`, `defaultDatabase`, `secretName`), and `components.ingestion-gateway.parameters.image` match your cluster topology. If `ingestionGatewayDeployment.enabled` was `false` on your cluster, all three components are generated with `enabled: false` and Steps 2 and 5 can be skipped.

### Step 2: Hand Off Helm Resource Ownership

Annotate the live `ingestion-gateway` resources with `helm.sh/resource-policy=keep` and transfer Helm ownership metadata to `xmc-cmp-ingestion-gateway`:

```bash
python3 ./tools/migrations/components/migrate.py handoff \
  --release "${XMC_RELEASE}" \
  --namespace "${XMC_NAMESPACE}"
```

`handoff` is idempotent and updates:
- `deployment/ingestion-gateway-deployment`
- `service/ingestion-gateway-service`
- `horizontalpodautoscaler/ingestion-gateway-hpa` (if present)
- `networkpolicy/<release>-ingestion-gateway-network-policy` (`helm.sh/resource-policy=keep` only, preventing an unprotected ingress window between Steps 3 and 4)

**Need to abort before Step 3?** Run `--undo` to restore umbrella ownership and strip `keep`:

```bash
python3 ./tools/migrations/components/migrate.py handoff \
  --release "${XMC_RELEASE}" \
  --namespace "${XMC_NAMESPACE}" \
  --undo
```

### Step 3: Upgrade XMC with `enable_components: true`

Enable components in your deployment configuration and run the normal XMC upgrade (including Spanner `ess` and `xid` migrations). During this step, Helm omits the legacy gateway templates from the umbrella chart, skips deleting the live gateway objects (`keep`), and starts `xmc-bridge` while legacy gateway pods continue serving traffic.

- **Option A — Development GKE Upgrade (`dev_env.py gke-upgrade`)**:
  Set `enable_components: true` in your `xmanager-cloud-deployment.yaml` and run:
  ```bash
  python3 ./tools/dev_env/dev_env.py gke-upgrade \
    --config deploy/ctk_v2/xmanager-cloud-deployment.yaml \
    --run-migrations \
    --profile-values profile-values.yaml
  ```
  *(Note: `gke-upgrade --profile-values` automatically executes Step 4 for you; proceed directly to Step 5.)*

- **Option B — Cluster Toolkit (`gcluster`)**:
  1. Ensure `xmc_bridge` and all updated service images are pushed (`bazel run //cmd/xmc_bridge:xmc_bridge_image_push`) and Spanner `ess`/`xid` migrations are applied (`up`).
  2. Set `enable_components: true` (and updated `image_tag`) in `xmanager-cloud-deployment.yaml`, recompile the blueprint, and deploy `xmc_app`:
     ```bash
     ./cluster-toolkit/gcluster create deploy/ctk_v2/xmanager-cloud.yaml \
       -d deploy/ctk_v2/xmanager-cloud-deployment.yaml -o ctk-out -l WARNING -w --force
     ./cluster-toolkit/gcluster deploy ctk-out/<deployment_name> --only xmc_app --auto-approve
     ```

- **Option C — Direct `helm upgrade`**:
  Apply Spanner `ess`/`xid` migrations first, then upgrade the umbrella release with `componentsDeployment.enabled=true` (and `componentsDeployment.xmcBridge.image` set to your pushed `xmc-bridge` image):
  ```bash
  helm upgrade "${XMC_RELEASE}" ./deploy/helm \
    -n "${XMC_NAMESPACE}" \
    -f my-values.yaml \
    --set componentsDeployment.enabled=true
  ```

### Step 4: Install the `stable-2026a` Component Profile

Install (or upgrade) the `xmc-profile` release using the validated `profile-values.yaml`:

```bash
helm upgrade --install xmc-profile ./deploy/profiles/stable-2026a \
  -n "${XMC_NAMESPACE}" \
  -f profile-values.yaml \
  --wait
```

`xmc-bridge` reconciles the `redpanda`, `clickhouse`, and `ingestion-gateway` `Component` CRs in dependency order, adopts the kept `ingestion-gateway` objects into release `xmc-cmp-ingestion-gateway`, and triggers a zero-downtime rolling update with `-enable_components=true`.

### Step 5: Finalize Adoption and Strip `keep`

Port-forward `service/ingestion-gateway-service` (or pass your cluster's external ingestion URL) and run `migrate.py finalize`:

```bash
kubectl port-forward -n "${XMC_NAMESPACE}" svc/ingestion-gateway-service 18080:8080 &
PF_PID=$!
trap 'kill "${PF_PID}" 2>/dev/null || true' EXIT

python3 ./tools/migrations/components/migrate.py finalize \
  --release "${XMC_RELEASE}" \
  --namespace "${XMC_NAMESPACE}" \
  --metrics-url "http://127.0.0.1:18080/api/v1/metrics"
```

What `finalize` verifies before stripping `keep`:
1. `Component/ingestion-gateway` reaches `Ready=True` and `Synced=True` at its current `metadata.generation`.
2. `helm status xmc-cmp-ingestion-gateway` is `deployed` and live `Deployment`, `Service`, and `HPA` carry `xmc-cmp-ingestion-gateway` ownership with zero spec drift against the rendered component manifest (including `-enable_components=true`).
3. `kubectl rollout status deployment/ingestion-gateway-deployment` completes and active pods are `Running` and `Ready`.
4. Strips `helm.sh/resource-policy=keep` from the adopted objects, deletes the superseded `<release>-ingestion-gateway-network-policy` once `ingestion-gateway-network-policy` is live, and verifies `POST /api/v1/metrics` returns HTTP `202 Accepted`.

---

## Troubleshooting & Recovery

- **`Component/redpanda` or `Component/clickhouse` stays `Ready=False`**:
  The kept `ingestion-gateway` pods continue serving traffic throughout. Inspect `kubectl describe component -n "${XMC_NAMESPACE}"` and `kubectl logs -n "${XMC_NAMESPACE}" deploy/xmc-bridge`, correct the endpoint or `Secret` name in `profile-values.yaml`, and re-run Step 4 (`helm upgrade --install xmc-profile ...`).
- **Transient fence-declare warnings in `xmc-bridge` logs during Step 3**:
  While `experiment-state-server` restarts during the umbrella upgrade, `xmc-bridge` may log transient gRPC connection warnings for ~30–45 seconds before reconciling automatically.
- **Emergency Rollback to Legacy Umbrella Mode (Post-Step 4)**:
  If you must revert to `enable_components: false` after `xmc-bridge` has already adopted the gateway:
  1. Disable deletion protection and set `deletionPolicy: Orphan` so removing `xmc-profile` detaches `xmc-bridge` without deleting live workloads:
     ```bash
     helm upgrade xmc-profile ./deploy/profiles/stable-2026a -n "${XMC_NAMESPACE}" \
       -f profile-values.yaml \
       --set components.redpanda.deletionProtection=false \
       --set components.clickhouse.deletionProtection=false \
       --set components.ingestion-gateway.deletionProtection=false \
       --set components.ingestion-gateway.deletionPolicy=Orphan \
       --wait
     helm uninstall xmc-profile -n "${XMC_NAMESPACE}" --wait
     ```
  2. Stamp `keep`, uninstall the orphaned `xmc-cmp-ingestion-gateway` Helm release, and restore `<release>` ownership annotations:
     ```bash
     python3 ./tools/migrations/components/migrate.py handoff \
       --release "${XMC_RELEASE}" \
       --namespace "${XMC_NAMESPACE}" \
       --undo
     ```
  3. Re-apply your umbrella chart with `enable_components: false` (`componentsDeployment.enabled=false`) and remove the component `NetworkPolicy`:
     ```bash
     kubectl delete networkpolicy ingestion-gateway-network-policy \
       -n "${XMC_NAMESPACE}" --ignore-not-found=true
     ```
