# Language-Specific Test Coverage Commands

## Go (Golang)
```bash
# Run tests with lcov report
bazel coverage --combined_report=lcov //pkg/auth/...
# Or via go tool directly
go test -coverprofile=coverage.out ./pkg/auth/...
go tool cover -func=coverage.out
```

## Python
```bash
# Execute affected unit tests via repository runner
python3 ./tools/run_affected_tests.py
# Or via Bazel coverage
bazel coverage --combined_report=lcov //sdks/python/xmc/...
```

## TypeScript / Angular
```bash
# Execute Angular unit test suite
npm test -- --coverage
```
