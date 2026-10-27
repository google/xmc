# Legacy Ingestion Gateway to XMC Components Migration Tool (`tools/migrations/components/`)

Deterministic one-time CLI tooling for migrating legacy umbrella `ingestion-gateway` Helm deployments to XMC Components (`stable-2026a`).

## Modules

- [`cluster_inspector.py`](cluster_inspector.py): Read-only Helm (`helm get values -a -o json`) and Kubernetes (`kubectl get -o json`) cluster queries.
- [`profile_preparer.py`](profile_preparer.py): Maps legacy umbrella values and live workload/HPA state to flat `stable-2026a` component parameters and renders `profile-values.yaml`.
- [`migrate.py`](migrate.py): Structured JSON CLI entrypoint (`prepare`).

## Usage

```bash
python3 tools/migrations/components/migrate.py prepare \
  --release xmc \
  --namespace xmc \
  --output profile-values.yaml
```

## Testing

```bash
bazel test //tools/migrations/components/...
```
