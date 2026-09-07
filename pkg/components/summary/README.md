# Custom Components Summary & Admin Inspection (`pkg/components/summary`)

The `pkg/components/summary` package provides administrative discovery, health inspection, and configuration introspection for registered custom components in XManager on Cloud (XMC). It bridges the underlying component persistence layer (`pkg/components/store`) with administrative services such as the Health Service (`cmd/health_service`) and the Admin Panel UI.

---

## Architectural Principles

### 1. Zero External RPCs on the Hot Path
A core architectural invariant of the Components Subsystem is that **health reporting and administrative discovery must never block on or depend upon live external endpoints on the hot path**. 

- **State Purely Derived from Spanner**: The `StoreComponentsProvider` reads component state exclusively from the Cloud Spanner store via `store.Store.ListComponents(ctx)`.
- **Fault Isolation**: External service probes, Kubernetes API calls, or third-party webhooks are strictly prohibited on the summary path. If an external component cluster is down, slow, or partitioned, the health service responds immediately based on the recorded observed state in Cloud Spanner without incurring network timeouts or cascade failures.

### 2. Defensive Timeout Guardrails
When querying the backing store from `cmd/health_service/health_checker.go`, the health checker wraps the provider call in a dedicated 3-second timeout context:
```go
compCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
defer cancel()
customHealths, customErr = c.componentsProvider.GetComponentsHealth(compCtx)
```
This protects health check latency and prevents slow database read locks from impacting overall health check throughput.

### 3. Graceful Degradation & Fail-Fast Initialization
- **Explicit Flag Guard**: Custom component discovery is controlled by the `--enable_components` flag (environment variable: `enable_components`).
- **Fail-Fast on Database Misconfiguration**: If `--enable_components` is explicitly set to `true`, failure to initialize the Cloud Spanner client or create the store provider triggers `logging.Fatal`, halting daemon startup to prevent a false sense of operational security.
- **In-Memory Fallback for Tests & Local Dev**: When `EssSpannerDatabase` is empty (e.g., local development or hermetic unit tests), the provider falls back to a thread-safe `store.NewMemoryStore()`.

---

## Core Interfaces & Data Models

### `CustomComponentsInfoProvider`
The primary interface exposed by the package:

```go
type CustomComponentsInfoProvider interface {
	GetComponentsHealth(ctx context.Context) ([]ComponentHealth, error)
}
```

### Data Structures
- **`ComponentHealth`**: Captures operational health status:
  - `Name`: Unique component identifier.
  - `Type`: Component classification (`ComponentTypeCustom1PComponent`, `ComponentTypeCustom3PWrapper`, etc.).
  - `HealthState`: Derived health status (`HealthStateHealthy`, `HealthStateUnhealthy`, `HealthStateDisabled`, `HealthStateUnspecified`).
  - `StatusMessage`: Human-readable explanation of status or error condition.

---

## Health State Mapping Contract

The provider evaluates component health deterministically using the component's desired intent (`c.State`) and observed generation status (`c.Status`):

| Component State | Access Status | Derived `HealthState` | Platform Health Impact |
| :--- | :--- | :--- | :--- |
| `COMPONENT_STATE_ENABLED` | `Ready == true` | `HealthStateHealthy` | Preserves `HEALTHY` platform status |
| `COMPONENT_STATE_ENABLED` | `Ready == false` | `HealthStateUnhealthy` | Degrades platform to `UNHEALTHY` |
| `COMPONENT_STATE_DISABLING` | Any | `HealthStateUnhealthy` | Degrades platform to `UNHEALTHY` ("Component draining") |
| `COMPONENT_STATE_DISABLED` | Any | `HealthStateUnspecified` | **Ignored** (does not degrade platform health) |
| `COMPONENT_STATE_DELETED` | Any | (Filtered / Omitted) | N/A |

### Platform Health Isolation Invariant
In `cmd/health_service/server.go`, core platform services returning `HEALTH_STATE_UNSPECIFIED` (e.g., unconfigured Log Monitor or database client initialization failures) trigger an `UNHEALTHY` overall platform state. In contrast, custom components with `HEALTH_STATE_UNSPECIFIED` (representing disabled optional components) are explicitly ignored during overall platform health calculation.
