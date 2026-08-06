# Hermetic Metrics Testing Infrastructure (`test/metrics/infra`)

This package provides Go (`setup.go`) and Python (`setup.py`) hermetic testing harnesses for Redpanda (Kafka) and ClickHouse metrics pipeline integration testing.

---

## 1. What Is This & Why Is It Needed?

### What Is It?
The `test/metrics/infra` package contains programmatic testing utilities and environment mapping harnesses for both **Go** and **Python** test suites. It manages local Docker container endpoints (Redpanda seed brokers and ClickHouse database drivers) and dynamically injects network addresses into SDK and client test environments.

### Why Is It Needed?
1. **Hermetic Integration Testing**: Allows integration tests and Python SDK tests to run against live Redpanda and ClickHouse container instances without depending on shared or external staging environments.
2. **Dynamic Port & Environment Mapping**: Container ports exposed by Docker are often dynamically assigned (e.g. `127.0.0.1:32768`). The Python and Go harnesses resolve these dynamic endpoints and automatically map them into environment variables (`XMC_REDPANDA_BROKERS`, `XMC_CLICKHOUSE_ADDR`, `XMC_GATEWAY_URL`).
3. **Smoke Testing & Connection Verification**: Provides lightweight TCP connectivity checks to ensure test environments are ready before running complex integration assertions.
4. **Zero Third-Party Dependency Leaks**: The Python harness uses standard library primitives (`socket`, `subprocess`, `os`, `contextlib`) to prevent dependency contamination in test runner environments.

---

## 2. Infrastructure Components

### Go Testing Harness (`setup.go`)
- Built on top of [`testcontainers-go`](https://github.com/testcontainers/testcontainers-go).
- Dynamically provisions isolated Docker containers:
  - **Redpanda Container**: Runs `docker.redpanda.com/redpandadata/redpanda:v24.1.1` and exposes Kafka seed broker endpoints.
  - **ClickHouse Container**: Runs `clickhouse/clickhouse-server:24.3-alpine`, applies SQL DDL initialization scripts ([`deploy/clickhouse/schema.sql`](../../../deploy/clickhouse/schema.sql)), and returns a native [`clickhouse-go`](https://github.com/ClickHouse/clickhouse-go) database driver connection (`driver.Conn`).

### Python Testing Harness (`setup.py`)
- Standard library Python testing harness (`MetricsTestHarness`, `create_test_harness`).
- **Endpoint Resolution**: Resolves Docker container host ports via `docker port` or defaults to local fallbacks (`127.0.0.1:9092` for Redpanda, `127.0.0.1:9000` / `8123` for ClickHouse).
- **Environment Mapping**: Injects required environment variables into `os.environ` using a context manager (`env_context()`) for Python SDK integration tests.
- **TCP Smoke Testing**: Verifies socket connectivity (`smoke_test()`, `verify_all_endpoints()`) prior to test execution.

---

## 3. Quickstart & Usage Examples

### Go Usage Example

```go
package mytest

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"xmanager_cloud/test/metrics/infra"
)

func TestMyIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	schemaPath := filepath.Join("..", "..", "..", "deploy", "clickhouse", "schema.sql")

	// Start hermetic Redpanda and ClickHouse containers
	harness, err := infra.StartHarness(ctx, schemaPath)
	if err != nil {
		t.Fatalf("Failed starting hermetic harness: %v", err)
	}
	defer harness.Teardown(ctx)

	brokers := harness.RedpandaBrokers
	conn := harness.ClickHouseConn

	// Run integration assertions...
}
```

### Python Usage Example

```python
import unittest

from test.metrics.infra.setup import create_test_harness
from xmc import Client


class TestPythonMetricsIntegration(unittest.TestCase):
    def test_python_sdk_with_harness(self) -> None:
        harness = create_test_harness(auto_resolve_containers=True)

        with harness.env_context():
            # Injects XMC_REDPANDA_BROKERS, XMC_CLICKHOUSE_ADDR into os.environ
            harness.verify_all_endpoints(timeout=2.0)

            client = Client()
            self.assertFalse(client.disabled)
```

---

## 4. Running Tests

### Prerequisites
- Active local **Docker daemon** listening on `/var/run/docker.sock` for container integration tests.

### Execution Commands

- **Using Go Tooling**:
  ```bash
  go test -v ./test/metrics/infra/...
  ```

- **Using Bazel**:
  ```bash
  # Go container integration test
  bazel test //test/metrics/infra:infra_test --tags=requires-network

  # Python harness unit test
  bazel test //test/metrics/infra:setup_py_test
  ```
