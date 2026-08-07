# Pathways Multi-Component JobSet Example

This example demonstrates how to configure and launch a multi-component Pathways (`pathways-head` + `worker`) JobSet using XManager Cloud (`xm_cloud`), taking full advantage of custom container network port mappings (`ContainerPort`).

## Architecture

In a Pathways RL post-training or distributed execution topology on JAX-on-Pathways, the control plane and multi-slice TPU workers coordinate over gRPC ports across two distinct `KubernetesJob` (`ReplicatedJob`) roles inside a single `JobSet`:

### 1. `pathways-head` ReplicatedJob (1 pod on CPU/control-plane nodes)
- **`pathways-rm` (Init Container)**: Exposes Resource Manager gRPC ports **`29001`** and **`29002`**.
- **`pathways-proxy` (Init Container)**: Exposes Pathways proxy server port **`29000`**.
- **`jax-tpu` (Primary Container)**: Connects to the proxy via `JAX_BACKEND_TARGET=grpc://$(PATHWAYS_HEAD):29000`.

### 2. `worker` ReplicatedJob (Multi-pod TPU worker slices)
- **`pathways-worker` (Primary Container on TPU nodes)**: Exposes worker gRPC port **`29005`**, secondary data port **`29006`**, TPU Inter-Chip Interconnect (ICI) port **`8471`**, and health/metrics port **`8080`**. Uses `dependency_order` to depend on `pathways-head` being `READY` before starting worker pods.

## Running the Example

Make sure your environment is configured with `PROJECT_ID`, `QUEUE_NAME`, and `XMC_DOMAIN` inside your `.env` file, and that your Python environment (`./deploy/cli/venv`) is active:

```bash
python3 examples/pathways_job/launch.py
```
