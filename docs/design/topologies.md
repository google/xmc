# Deployment Topologies

XMC uses automated HPC Cluster Toolkit blueprints for deployment (Cluster Toolkit Blueprint v2; legacy Blueprint v1 has been removed).

---

## 1. Cluster Toolkit Blueprint v1

This is the legacy/initial topology which uses:
- **GKE Deployments**: Runs the backend services (Reconciler, Actuator, and Experiment State Server) on GKE.
- **Web UI on Cloud Run**: The frontend UI is hosted separately on Google Cloud Run.
- **Identity-Aware Proxy (IAP)**: Protects backend services and manages identity/access control at the infrastructure load balancer.
- **State & Messaging**: Uses Cloud Spanner for state storage, Memorystore Redis or Cloud Tasks for background queues, and Pub/Sub for log streams.
- **DNS/IP**: Public IPs and Managed DNS zones.

*(Cluster Toolkit Blueprint v1 has been deprecated and removed from this repository in favor of Blueprint v2 below.)*

---

## 2. Cluster Toolkit Blueprint v2

This is the modern/current topology which consolidates components:
- **Unified GKE Hosting**: The frontend UI runs inside the GKE cluster itself, instead of Cloud Run.
- **GKE Gateway API**: Exposes the UI, HTTP API, and gRPC endpoints on a single Global External IP address using Gateway and HTTPRoute resources.
- **App-level OAuth2**: Users are authenticated using OAuth2 client credentials and session JWTs managed by the application itself, removing infrastructure-level IAP requirements.
- **cert-manager with Google Trust Services (GTS)**: Automates the provisioning of TLS certificates.
- **State & Messaging**: Cloud Spanner for state storage and Memorystore Redis for internal queues.

For more details, see the [deploy/ctk_v2/README.md](../../deploy/ctk_v2/README.md).
