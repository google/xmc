# Python Language Guidelines

This document outlines coding standards, style guidelines, and best practices for Python development across the codebase.

---

## 1. Style & Conventions
- **Python Version Policy**: Target Python 3.13 for venv/CI tooling; code must remain compatible with Python 3.11 (Bazel hermetic `rules_python` toolchain default), which is also the `requires-python` minimum of the published SDKs (`sdks/python/*/pyproject.toml`). Ruff `target-version` in the root `pyproject.toml` encodes this minimum. Leverage modern features available in the applicable minimum (enhanced typing, `pathlib` APIs, `datetime.UTC`).
- **PEP 8 Style Guide**: Strictly follow standard Python style guidelines (PEP 8) and standard library best practices.
- **Standard Library Preference**: Prefer built-in Python standard library modules (`pathlib`, `argparse`, `dataclasses`, `enum`, `subprocess`, `typing`) over third-party dependencies or custom utility wrappers.
- **Prefer Python over Shell**: Write Python scripts instead of complex Shell scripts for automation, operational hooks, and CLI tools whenever procedural logic, JSON/YAML parsing, or conditional execution is required.
- **Client SDK Wrapper Encapsulation & Private Stub Access**: Encapsulate low-level RPC operations within high-level client wrapper classes. Do not leak internal transport details or bypass client abstractions by accessing private attributes (e.g. `stub._stub`). If a wrapper lacks a required method, prefer extending the wrapper class (e.g. `ExperimentStateGRpcApi`) with a first-class public method. When transitional code or example scripts temporarily mix high-level wrapper methods with raw RPC stub calls, keep the wrapper and the raw stub in distinct, explicitly typed variables (never `Any`) so both human readers and static analyzers can resolve which interface each call targets.

## 2. Type Annotations & Documentation
- **Public API Type Hints**: Enforce explicit type annotations (`typing`) on all public function signatures, class methods, module interfaces, and data structures.
- **Structured Docstrings**: Write clear, structured docstrings for all public modules, classes, and methods detailing operational intent, arguments, return values, and raised exceptions.

## 3. Unit Testing & Bazel Execution Protocol
- **Standard `unittest` Framework**: Standardize test suites using the Python standard library `unittest` module (with tests named via `_test.py` suffix alongside target source files).
- **Testing Scope & Cohesion**: Adhere to the 1-to-1 file test cohesion rules defined in [Core Engineering Standards](../core_engineering.md#5-strict-testing-coverage--pr-granularity), including exemptions for standalone example scripts under `examples/`.
- **Hermetic Testing**: Isolate tests from external environment side-effects (file system mutations, process invocations, network RPCs) using `unittest.mock.patch` to keep unit test suites fast, deterministic, and self-contained.
- **Mandatory Bazel Test Execution**: Because Python packages in this repository rely on Bazel-generated Protobuf bindings (`api.protos...`), tests MUST be run using Bazel. ALWAYS execute unit tests via `./tools/run_affected_tests.py` or `bazel test <target>`. Do NOT attempt direct system `python3 -m unittest` or `pip install` invocations outside of Bazel.

## 4. Virtual Environments & Execution Safety
- **Python Virtual Environment**: Before executing or developing CLI tools under `tools/` or `deploy/`, ensure the local Python virtual environment is initialized and activated. Virtual environments must be explicitly created using **Python 3.13.14** via the setup script, passing `--venv-dir ./tools/common/venv` to match the default directory that `tools/common/tool_utils.py` auto-bootstraps and re-executes into:
  ```bash
  python3.13 ./tools/common/setup_venv.py --venv-dir ./tools/common/venv --requirements ./tools/common/requirements.txt
  source ./tools/common/venv/bin/activate
  ```
- **Standalone Script Files Over `python3 -c`**: Always write Python code to dedicated `.py` files on disk using `write_to_file` rather than invoking inline commands via `python3 -c "..."` to prevent shell expansion and backtick command substitution errors.
