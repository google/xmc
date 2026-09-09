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

### 3. Graceful Degradation & Resilient Startup
- **Explicit Flag Guard**: Custom component discovery is controlled by the `--enable_components` flag (environment variable: `enable_components`).
- **Resilient Startup on Store Initialization**: If `--enable_components` is enabled but `EssSpannerDatabase` is not configured or the Cloud Spanner client cannot be initialized, the daemon logs a warning and disables custom component discovery (`logging.Warn`) without crashing the daemon. The health checker then reports custom components as disabled.
- **Hermetic Dependency Injection**: Tests and mock environments explicitly provide their own store (e.g., `store.NewMemoryStore()`) via dependency injection into `NewStoreComponentsProvider(store)`. Production code does not contain hidden or implicit in-memory fallbacks, ensuring fail-fast observability.

---

## Core Interfaces & Data Models

### `CustomComponentsInfoProvider`
The primary interface exposed by the package:

```go
type CustomComponentsInfoProvider interface {
	GetComponentsHealth(ctx context.Context) ([]ComponentHealth, error)
	GetComponentsConfig(ctx context.Context) ([]ComponentConfig, error)
}
```

### Data Structures
- **`ComponentHealth`**: Captures operational health status:
  - `Name`: Unique component identifier.
  - `Type`: Component classification (`ComponentTypeCustom1PComponent`, `ComponentTypeCustom3PWrapper`, etc.).
  - `HealthState`: Derived health status (`HealthStateHealthy`, `HealthStateUnhealthy`, `HealthStateDisabled`, `HealthStateUnspecified`).
  - `StatusMessage`: Human-readable explanation of status or error condition.
- **`ComponentConfig`**: Captures runtime configuration:
  - `Name`: Component identifier.
  - `Type`: Component classification.
  - `ConfigParameters`: Deterministically sorted slice of `ConfigParameter` key-value pairs (`Key`, `Value`).

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

---

## Configuration Parameter Extraction

`GetComponentsConfig` extracts administrative configuration parameters for components, focusing cleanly on service configuration and access coordinates while avoiding reconciliation or internal lifecycle clutter:

1. **Service Configuration Parameters (`spec.parameters`)**:
   - Service-specific tuning parameters declared on `ComponentSpec.parameters` (e.g. `topicName`, `brokerEndpoints`, `port`, `bufferBytes`, `storageSize`) are surfaced directly as configuration keys.
   - **Collision Disambiguation**: If a custom parameter key collides with reserved metadata keys (`version` or `endpoint`), the primary metadata attribute is preserved and the custom parameter is disambiguated with `param.<key>`.
2. **Component Version**:
   - `version`: Component release version tag (e.g. `v2.1.0`), extracted from `spec.version` when specified.
3. **Resolved Runtime Endpoint**:
   - `endpoint`: Formatted scheme, host, and port (e.g. `http://service.namespace:8080`), extracted from `status.coordinates`.
   - **RFC 3986 Compliance**: IPv6 host literals without explicit ports are enclosed in square brackets (e.g. `http://[2001:db8::1]`).

> [!NOTE]
> Internal reconciliation bookkeeping (such as `generation`, `state`, `dependencies`, `lifecycle.*`, and `wrapper.*`) is deliberately omitted from `GetComponentsConfig` to keep the Admin Panel configuration view focused and clean. Operational status and component state are exposed via `GetComponentsHealth` instead.
