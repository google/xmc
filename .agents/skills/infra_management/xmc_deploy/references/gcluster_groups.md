# Cluster Toolkit Groups & DNS Configuration Reference

## Pre-flight CLI Dependency Requirements
- `gcloud` (Google Cloud SDK)
- `kubectl` (Kubernetes client)
- `helm` (Helm client)
- `terraform` (v1.12.2+)
- `bazel`
- `./cluster-toolkit/gcluster`

## Deployment Group Sequence
1. `apis`: Enables GCP APIs.
2. `primary`: Core infrastructure & static IP. Extract IP via:
   `gcloud compute addresses describe xmc-gateway-static-ip-<env> --global --project=<project_id>`
   *Pause for user DNS & OAuth redirect URI setup.*
3. `compute`: GKE Cluster & Node pools. Enable Gateway API:
   `gcloud container clusters update <cluster_name> --gateway-api=standard --region=<region>`
4. `kubernetes_prereqs`: System Helm charts.
5. Build & push service Docker images:
   `bazel run //cmd/actuator:actuator_image_push`
   `bazel run //cmd/experiment_state_server:experiment_state_server_image_push`
   `bazel run //cmd/log_monitor:log_monitor_image_push`
   `bazel run //cmd/reconciler:reconciler_image_push`
   `bazel run //cmd/ui:ui_image_push`
   `bazel run //cmd/xid_service:xid_service_image_push`
6. `xmc_app`: XMC Application Helm deployment.
