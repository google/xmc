# Deployment Topologies

XMC supports two deployment topologies using automated HPC Cluster Toolkit blueprints.

---

## 1. Cluster Toolkit Blueprint v1

This is the legacy/initial topology which uses:
- **GKE Deployments**: Runs the backend services (Reconciler, Actuator, and Experiment State Server) on GKE.
- **Web UI on Cloud Run**: The frontend UI is hosted separately on Google Cloud Run.
- **Identity-Aware Proxy (IAP)**: Protects backend services and manages identity/access control at the infrastructure load balancer.
- **State & Messaging**: Uses Cloud Spanner for state storage, Memorystore Redis or Cloud Tasks for background queues, and Pub/Sub for log streams.
- **DNS/IP**: Public IPs and Managed DNS zones.

For more details, see the [deploy/ctk_v1/README.md](../../deploy/ctk_v1/README.md).

---

## 2. Cluster Toolkit Blueprint v2

This is the modern/current topology which consolidates components:
- **Unified GKE Hosting**: The frontend UI runs inside the GKE cluster itself, instead of Cloud Run, hosting both the core experiment management views and the **Resource Observability Dashboard (ROD)**.
- **Microservices Architecture**: Runs core daemons on GKE including Experiment State Server (ESS), Reconciler, Actuator, Log Monitor, and **ROD Data Provider (`rod-data-provider`)**.
- **GKE Gateway API**: Exposes the UI, HTTP API, gRPC, and ROD queue telemetry endpoints on a single Global External IP address using Gateway and HTTPRoute resources.
- **App-level OAuth2**: Users are authenticated using OAuth2 client credentials and session JWTs managed by the application itself, removing infrastructure-level IAP requirements.
- **cert-manager with Google Trust Services (GTS)**: Automates the provisioning of TLS certificates.
- **State & Messaging**: Cloud Spanner for state storage, WorkUnit status transitions, and Memorystore Redis for internal queues.

For more details, see the [deploy/ctk_v2/README.md](../../deploy/ctk_v2/README.md) and [Resource Observability Dashboard Design](resource_observability_dashboard.md).
