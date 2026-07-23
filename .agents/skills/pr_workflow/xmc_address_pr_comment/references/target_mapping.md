# Bazel Binary & GKE Deployment Mapping Reference

| Bazel Target | Push Command Target | Helm Template File | GKE Deployment Name |
| :--- | :--- | :--- | :--- |
| `//cmd/xid_service:xid_service` | `//cmd/xid_service:xid_service_image_push` | `deploy/helm/templates/xid/deployment.yaml` | `xid-service-deployment` |
| `//cmd/experiment_state_server` | `//cmd/experiment_state_server:ess_image_push` | `deploy/helm/templates/ess/deployment.yaml` | `experiment-state-server-deployment` |
| `//cmd/actuator:actuator` | `//cmd/actuator:actuator_image_push` | `deploy/helm/templates/actuator/deployment.yaml` | `actuator-deployment` |
| `//cmd/reconciler:reconciler` | `//cmd/reconciler:reconciler_image_push` | `deploy/helm/templates/reconciler/deployment.yaml` | `reconciler-deployment` |
| `//cmd/log_monitor:log_monitor` | `//cmd/log_monitor:log_monitor_image_push` | N/A | N/A |
| `//cmd/ui` (Frontend TS/HTML) | `//cmd/ui:ui_image_push` | `deploy/helm/templates/ess/ui-deployment.yaml` | `ui-deployment` |
