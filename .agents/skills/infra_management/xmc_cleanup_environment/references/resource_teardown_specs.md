# Teardown Failure Mitigations & Resource Specs

## Failure Mitigations
- **Helm Timeout in `xmc_app`**: If destroy gets stuck deleting Helm releases, run:
  `./cluster-toolkit/gcluster destroy ctk-out/<deployment_name> --auto-approve --skip xmc_app`
- **GKE Node Pool Stuck in STOPPING**: Force delete GKE cluster via gcloud, then retry:
  `gcloud container clusters delete xmc-<deployment_name>-<env> --location=<loc> --project=<proj>`
  `./cluster-toolkit/gcluster destroy ctk-out/<deployment_name> --auto-approve --skip xmc_app,kubernetes_prereqs`
