# Bazel Testlog Parsing & Error Isolation Guide

## Bazel Testlog Paths
Failed Bazel test logs are written to:
`bazel-testlogs/<package_path>/<target_name>/test.log`

## Targeted Parsing Commands
```bash
# Extract failure section from test log
grep -A 20 -B 5 "FAIL" bazel-testlogs/pkg/auth/oauth2_test/test.log

# Extract panic or exception stack traces
grep -A 30 "panic:" bazel-testlogs/cmd/xid_service/xid_service_test/test.log
```
