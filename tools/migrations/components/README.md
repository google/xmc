# Legacy Ingestion Gateway to XMC Components Migration Tool (`tools/migrations/components/`)

Deterministic one-time CLI tooling for migrating legacy umbrella `ingestion-gateway` Helm deployments to XMC Components (`stable-2026a`).

## Modules

- [`cluster_inspector.py`](cluster_inspector.py): Read-only Helm and Kubernetes resource, Secret, and release-status queries.
- [`profile_preparer.py`](profile_preparer.py): Audits live `ingestion-gateway-deployment` pods for unmapped customizations, maps legacy umbrella values and live workload/HPA/SASL/TLS state to flat `stable-2026a` component parameters, verifies referenced Secrets, and validates output with `helm template` + `component-linter`.
- [`resource_handoff.py`](resource_handoff.py): Idempotently annotates `ingestion-gateway-deployment`, `ingestion-gateway-service`, optional `ingestion-gateway-hpa`, and optional legacy `NetworkPolicy` with `helm.sh/resource-policy=keep` and `xmc-cmp-ingestion-gateway` ownership (plus reversible `--undo` before upgrade).
- [`migrate.py`](migrate.py): Structured JSON CLI entrypoint (`prepare`, `handoff`).

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
```

## Testing

```bash
bazel test //tools/migrations/components/...
```
