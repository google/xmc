# XManager on Cloud (XMC)

> [!CAUTION]
> THIS BRANCH IS FOR GITHUB INTEGRATION TESTING ONLY.
> DO NOT MAKE ANY CHANGES HERE
> DO NOT MERGE


XManager on Cloud (XMC) is a high-performance backend application that exposes gRPC and HTTP APIs to orchestrate cloud-native resource scheduling, job reconciliation, and workloads. It leverages Google Cloud Platform services (such as Cloud Spanner, Memorystore Redis, and Pub/Sub) alongside Kubernetes (GKE Gateway API, JobSets, Workloads) to deliver a unified computing interface.

---

## Documentation Index

Explore the folders and guides below to set up, build, or deploy XMC:

* ### [Installation & Deployment Guide](INSTALL.md)
  Step-by-step setup guides covering automated agent skills, Cluster Toolkit v2 blueprints, and direct Terraform/Helm commands, alongside the global configuration reference table.

* ### [Developer Documentation](docs/developer.md)
  Technical guides on compiling protocol buffers, custom AIP-160 filter evaluation, testing private packages, running database health checks, pre-commit hooks, and static code analysis.

* ### [Deployment Topologies](docs/design/topologies.md)
  Deep dive into the structural layout differences between Cluster Toolkit Blueprint v1 (GKE + Cloud Run + infrastructure IAP) and Blueprint v2 (Unified GKE + Gateway API + app-level OAuth2).

* ### [Gateway & Traffic Routing Design](docs/design/gateway.md)
  Detailed system design for GKE Gateway API path-based HTTP routing, GRPC traffic routing rules, Workload Identity mappings, and GCP IAM integrations.

* ### [Agent Workflows](docs/agent_workflows.md)
  Overview of pre-configured agent-assisted skills (`deploy_xmc` and `pr_comment_workflow`) for automated cloud deployment and PR feedback resolution.

* ### [Contributing Guide](CONTRIBUTING.md)
  Guidelines on submitting patches, reporting issues, and repository branching policies.
