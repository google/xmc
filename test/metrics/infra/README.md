# Hermetic Metrics Testing Infrastructure (`test/metrics/infra`)

This package provides a Go programmatic hermetic testing harness (`setup.go`) built on top of [`testcontainers-go`](https://github.com/testcontainers/testcontainers-go) for Redpanda and ClickHouse integration testing.

---

## 1. Overview

The testing harness dynamically provisions isolated Docker containers during Go unit/integration test runs (`go test`):
- **Redpanda Container**: Runs `docker.redpanda.com/redpandadata/redpanda:v24.1.1` and exposes Kafka seed broker endpoints.
- **ClickHouse Container**: Runs `clickhouse/clickhouse-server:24.3-alpine`, applies SQL DDL initialization scripts (e.g. [`deploy/clickhouse/schema.sql`](../../../deploy/clickhouse/schema.sql)), and returns a native [`clickhouse-go`](https://github.com/ClickHouse/clickhouse-go) database driver connection (`driver.Conn`).

---

## 2. Quickstart & Usage

### Basic Usage Example

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

	// Optionally specify production SQL DDL schema path
	schemaPath := filepath.Join("..", "..", "..", "deploy", "clickhouse", "schema.sql")

	// Start hermetic Redpanda and ClickHouse containers
	harness, err := infra.StartHarness(ctx, schemaPath)
	if err != nil {
		t.Fatalf("Failed starting hermetic harness: %v", err)
	}
	defer harness.Teardown(ctx)

	// Access seed brokers and database driver
	brokers := harness.RedpandaBrokers
	conn := harness.ClickHouseConn

	// Run integration assertions...
}
```

---

## 3. Running Tests

### Prerequisites
- Active local **Docker daemon** listening on `/var/run/docker.sock`.

### Execution Commands

- **Using Go Tooling**:
  ```bash
  go test -v ./test/metrics/infra/...
  ```

- **Using Bazel**:
  ```bash
  bazel test //test/metrics/infra:infra_test --tags=requires-network
  ```
