# Upgrade Script Step Options & Troubleshooting

## Valid Step Names for `--start-from` or `--only`
- `check`: Validates configuration, GCP credentials, and K8s namespace.
- `migrate`: Runs Spanner schema migrations.
- `images`: Rebuilds OCI container images with Bazel and pushes to Artifact Registry.
- `app`: Re-renders and applies Helm release templates.
- `restart`: Executes `kubectl rollout restart` across XMC deployments.
- `verify`: Executes environment health check verification script (`verify_xmc.py`).
