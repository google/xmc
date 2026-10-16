# XManager on Cloud (XMC)

XManager on Cloud (XMC) is a high-performance backend application that exposes gRPC and HTTP APIs to orchestrate cloud-native resource scheduling, job reconciliation, and workloads. It leverages Google Cloud Platform services (such as Cloud Spanner, Memorystore Redis, and Pub/Sub) alongside Kubernetes (GKE Gateway API, JobSets, Workloads) to deliver a unified computing interface.

---

## Documentation Index

Explore the folders and guides below to set up, build, or deploy XMC:

* ### [Architecture Overview](docs/architecture/overview.md)
  Start here. The four planes (control, execution, telemetry, components), how the twelve services interact, request routing at the edge, data stores, and cross-cutting concerns. Continues into the [Component Reference](docs/architecture/components.md), the [Execution Model](docs/architecture/execution_model.md), and [Use Cases](docs/architecture/use_cases.md) for ML researchers, platform engineers, and new contributors.

* ### [Main Flows](docs/flows/README.md)
  Sequence-level walkthroughs of the principal paths: [experiment lifecycle](docs/flows/experiment_lifecycle.md), [work unit lifecycle](docs/flows/work_unit_lifecycle.md) (the asynchronous actuation path), [reconciliation](docs/flows/reconciliation.md), and [metrics & observability](docs/flows/metrics_and_observability.md).

* ### [Installation & Deployment Guide](INSTALL.md)
  Step-by-step setup guides covering automated agent skills, Cluster Toolkit v2 blueprints, and direct Terraform/Helm commands, alongside the global configuration reference table.

* ### [Local & Hybrid Development Guide](tools/dev_env/README.md)
  Inner-loop local development using KinD (`dev_env up`), fast microservice reload (`dev_env reload`), and the hybrid execution model (`--remote-k8s`) pairing a local KinD control plane with a remote GKE data plane for real JobSet scheduling.

* ### [End-to-End & UI Feature Testing Guide](tools/nightly_tests/README.md)
  Out-of-the-box backend, telemetry, and Playwright Web UI verification modules and patterns for testing workloads and Angular views locally and in CI.

* ### [Developer Documentation](docs/developer.md)
  Technical guides on compiling protocol buffers, custom AIP-160 filter evaluation, testing private packages, running database health checks, pre-commit hooks, and static code analysis.

* ### [Deployment Topologies](docs/design/topologies.md)
  Deep dive into the structural layout differences between Cluster Toolkit Blueprint v1 (GKE + Cloud Run + infrastructure IAP) and Blueprint v2 (Unified GKE + Gateway API + app-level OAuth2).

* ### [Gateway & Traffic Routing Design](docs/design/gateway.md)
  Detailed system design for GKE Gateway API path-based HTTP routing, GRPC traffic routing rules, Workload Identity mappings, and GCP IAM integrations.

* ### [Agent Workflows](docs/agent_workflows.md)
  Overview of pre-configured agent-assisted skills (`xmc_deploy` and `xmc_address_pr_comment`) for automated cloud deployment and PR feedback resolution.

* ### [Contributing Guide](CONTRIBUTING.md)
  Guidelines on submitting patches, reporting issues, and repository branching policies.

---

## Tools & Scripts Virtual Environment

All automation tools and verification scripts under `./tools` and `./deploy` require a Python virtual environment with external dependencies (`tools/common/requirements.txt`). Virtual environments must be explicitly created using **Python 3.13.14**:

```bash
python3.13 -m venv ./tools/venv
source ./tools/venv/bin/activate
pip install --upgrade pip
pip install -r ./tools/common/requirements.txt
```

Alternatively, you can bootstrap the virtual environment directly:
```bash
python3 ./tools/common/setup_venv.py --venv-dir ./tools/venv --requirements ./tools/common/requirements.txt
```