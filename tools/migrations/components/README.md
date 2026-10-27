# Legacy Ingestion Gateway to XMC Components Migration Tool (`tools/migrations/components/`)

Deterministic one-time CLI tooling for migrating legacy umbrella `ingestion-gateway` Helm deployments to XMC Components (`stable-2026a`). See the full operator runbook in [`docs/migrations/components.md`](../../../docs/migrations/components.md).

## Modules

- [`cluster_inspector.py`](cluster_inspector.py): Helm and Kubernetes resource, Secret, manifest, and release-status queries plus shared namespace/annotation helpers.
- [`profile_preparer.py`](profile_preparer.py): Audits live `ingestion-gateway-deployment` pods for unmapped customizations, maps legacy umbrella values and live workload/HPA/SASL/TLS state to flat `stable-2026a` component parameters, verifies referenced Secrets, and validates output with `helm template` + `component-linter`.
- [`resource_handoff.py`](resource_handoff.py): Verifies `app.kubernetes.io/managed-by=Helm` and idempotently annotates `ingestion-gateway-deployment`, `ingestion-gateway-service`, optional `ingestion-gateway-hpa`, and optional legacy `NetworkPolicy` with `helm.sh/resource-policy=keep` and `xmc-cmp-ingestion-gateway` ownership (plus guarded `--undo` rollback).
- [`adoption_finalizer.py`](adoption_finalizer.py): Polls `Component/ingestion-gateway` `Ready`/`Synced`, verifies `helm status xmc-cmp-ingestion-gateway` and zero spec drift against rendered manifests, waits on `kubectl rollout status` and active pod readiness, strips `keep`, deletes superseded legacy `NetworkPolicy`, and probes `POST /api/v1/metrics`.
- [`migrate.py`](migrate.py): Structured JSON CLI entrypoint (`prepare`, `handoff`, `finalize`).

## Usage

```bash
# Step 1: Generate and lint profile-values.yaml
python3 tools/migrations/components/migrate.py prepare \
  --release xmc \
  --namespace xmc \
  --output profile-values.yaml

# Step 2: Annotate live gateway resources for zero-downtime Helm adoption
python3 tools/migrations/components/migrate.py handoff \
  --release xmc \
  --namespace xmc

# Step 5 (after upgrading with enable_components=true and installing xmc-profile):
python3 tools/migrations/components/migrate.py finalize \
  --release xmc \
  --namespace xmc \
  --metrics-url http://127.0.0.1:18080/api/v1/metrics
```

## Testing

```bash
bazel test //tools/migrations/components/...
```
