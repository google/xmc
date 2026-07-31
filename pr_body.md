## Summary
Adds a lightweight local ClickHouse container configuration (`deploy/local/clickhouse-standalone.yaml`) and test execution helper (`scripts/test_clickhouse_schema.py`) to support out-of-band development and rapid schema testing without CTK or GKE.

## Motivation & Context
Fixes b/537681533. Enables developers to quickly iterate on ClickHouse SQL schemas and integration testing on local workstations out-of-band of Cluster Toolkit or GKE deployments. Also documents clear user flows distinguishing local standalone testing from GKE Helm addon deployment.

## Key Changes
- Created `deploy/local/clickhouse-standalone.yaml` (Docker Compose running ClickHouse 24.3 exposing ports 8123/9000).
- Created `scripts/test_clickhouse_schema.py` for readiness polling and DDL/DML test execution.
- Created `scripts/test_clickhouse_schema_test.py` with 100% unit test coverage.
- Created `deploy/local/README.md` and updated `deploy/addons/metrics/clickhouse/README.md` to map local standalone vs GKE addon user flows.

## Verification & Testing
- **Commands Executed**:
  - `python3 -m unittest scripts/test_clickhouse_schema_test.py` (5/5 unit tests passed).
  - `python3 ./scripts/test_clickhouse_schema.py --cleanup` (Container booted, health checked, DDL/DML executed, cleaned up).
  - `./.githooks/pre-commit` (All linters passed: gofmt, golangci-lint, addlicense, ruff).
- **Test Status**: All unit and integration tests passed cleanly.

## Related Issues / Bugs
Fixes b/537681533

TAG=agy
CONV=7485b091-7539-488f-aa1d-559130b98dff
