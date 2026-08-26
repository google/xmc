# XMC AI Components Subsystem

The **XMC AI Components Subsystem** provides lifecycle management, dependency resolution, versioning, and validation for reusable AI software modules and configurations in XManager on Cloud (XMC).

---

## Architecture Overview

The Components subsystem coordinates component definitions, versioning, and runtime lifecycle across database storage, control planes, and cluster nodes:

- **Source of Truth (SOT)**: Google Cloud Spanner acts as the authoritative relational database storing the global component catalog, versions, dependencies, and rollout states.
- **Component Manager Service (CMS)**: A gRPC service hosted within the Experiment Service Server (ESS) providing control plane APIs for component registration, dependency validation, and lifecycle operations.
- **GitOps Bridge (`cmd/xmc-bridge`)**: A bidirectional synchronizer reconciling Kubernetes Custom Resources (`Component.xmc.dev`) with the authoritative Spanner catalog.
- **In-Memory Store (`pkg/components/store`)**: A thread-safe in-memory cache and graph validation engine with prospective DAG cycle detection, fast lookups, and snapshot resolution.

```
┌─────────────────────────────────────────────────────────┐
│              Experiment Service Server (ESS)            │
│  ┌───────────────────────────────────────────────────┐  │
│  │         Component Manager Service (CMS)           │  │
│  └─────────────────────────┬─────────────────────────┘  │
│                            │                            │
│  ┌─────────────────────────▼─────────────────────────┐  │
│  │             Storage Engine / MemoryStore          │  │
│  │   ┌─────────────────────┬─────────────────────┐   │  │
│  │   │ Dependency Parser   │ Graph Validator     │   │  │
│  │   │ & Normalization     │ (3-Color DFS)       │   │  │
│  │   └─────────────────────┴─────────────────────┘   │  │
│  └───────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────┘
```

---

## Subsystem Modules

### 1. Dependency Extraction & Normalization (`pkg/components/store/dependency_parser.go`)

Components specify explicit dependencies on other components via:
- **Spec Descriptions**: Annotated tags such as `@depends-on: comp-a, comp-b`
- **Labels**: Structured labels like `xmc.dev/dependency.<name>=<version>`

Key functions:
- `ExtractDependencies(spec *componentspb.ComponentSpec, labels map[string]string) []string`: Extracts unique canonical dependency names.
- `NormalizeDependencies(deps []string) []string`: Strips `components/` prefixes, deduplicates, and sorts lexicographically.

---

### 2. Dependency Cycle Detection (`pkg/components/store/graph_validator.go`)

To prevent circular deadlocks during runtime activation and initialization, XMC validates the component dependency DAG on every write operation (`SetComponentIntent`) before mutating store state.

#### 3-Color DFS Cycle Detection Algorithm
The validator utilizes a canonical 3-color depth-first search (DFS):
- **White (`0`)**: Unvisited node.
- **Gray (`1`)**: Currently exploring in active recursion stack (ancestor).
- **Black (`2`)**: Fully explored node and all reachable descendants.

When a traversal encounters a **Gray** node, a back-edge indicating a dependency cycle has been detected. The DFS reconstructs the exact cycle path (e.g., `comp-a -> comp-b -> comp-c -> comp-a`).

```
  [comp-a (Gray)] ──► [comp-b (Gray)] ──► [comp-c (Gray)]
        ▲                                       │
        └───────────────────────────────────────┘ (Back-edge detected!)
```

#### Prospective Cycle Validation
- `ValidateProspectiveDependencies(existing []*ComponentRecord, targetName string, newDeps []string) error`
- `ValidateProspectiveDependenciesFromMap(components map[string]*ComponentRecord, targetName string, newDeps []string) error`

If a prospective update would introduce a self-cycle or transitive cycle, the operation is rejected immediately with gRPC `codes.FailedPrecondition` and `ErrDependencyCycle`.
