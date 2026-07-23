# Bug Fixing & Debugging Guidelines

Log isolation, Bazel testlog parsing, and subagent Root Cause Analysis (RCA) guidelines.

---

## 1. Log Isolation & Context Bounds
- **Isolate Log Dumps**: Never dump entire log files into chat or prompt context memory.
- **Targeted Log Parsing**: Extract failure lines using grep or dedicated log parsing scripts.
- **Bazel Testlog Locations**: Locate Bazel test failure logs under `bazel-testlogs/<package>/<target>/test.log`.

## 2. Subagent RCA Delegation
- **Offload Heavy Debugging**: Delegate context-heavy log parsing and error trace analysis to worker subagents.
- **RCA Execution Steps**:
  1. Reproduce failure using minimal test command.
  2. Parse exact error traceback and failing assertion.
  3. Formulate root cause hypothesis.
  4. Verify fix with targeted unit test before editing production code.
