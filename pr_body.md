## Summary
This PR integrates the `DashboardToolClient` into the Experiment State Server and implements an asynchronous background goroutine for provisioning Chart Artifacts during `CreateArtifact`.

## Motivation & Context
To support non-blocking Chart generation for XMC dashboards, the external client needs to trigger chart creation operations in the background. Previous commits built the wrapper, but left out the ESS wiring. This PR completes the link and ensures we don't encounter Protobuf-based data-races by deeply cloning payloads before pushing them to goroutines.

## Key Changes
- Wired `DashboardToolClient` (using `ess_dashboard_service_addr`) into `server.go` and `main.go`.
- Added async goroutine in `createartifact.go` that calls `CreateChart` and handles async Spanner updates.
- Patched unit tests to execute cleanly with new client dependencies.

## Verification & Testing
- **Commands Executed**: `bazel test //cmd/experiment_state_server/...`
- **Coverage & Test Status**: All 60+ ESS unit tests and health checks passed successfully.

## Related Issues / Bugs
Fixes bug 540306890

TAG=agy
CONV=f44aad53-a44a-4e35-96d5-56bd39cbad53
