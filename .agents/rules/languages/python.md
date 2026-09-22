# Python Language Guidelines

This document outlines coding standards, style guidelines, and best practices for Python development across the codebase.

---

## 1. Style & Conventions
- **Python 3.14+ Version Enforcement**: Target Python 3.14+ for all Python scripts, CLI tools, operational hooks, and inline rule command snippets. Leverage modern Python 3.14+ features (including full syntax support in f-strings `f"{c['id']}"`, enhanced typing, and `pathlib` APIs).
- **PEP 8 Style Guide**: Strictly follow standard Python style guidelines (PEP 8) and standard library best practices.
- **Standard Library Preference**: Prefer built-in Python standard library modules (`pathlib`, `argparse`, `dataclasses`, `enum`, `subprocess`, `typing`) over third-party dependencies or custom utility wrappers.
- **Prefer Python over Shell**: Write Python scripts instead of complex Shell scripts for automation, operational hooks, and CLI tools whenever procedural logic, JSON/YAML parsing, or conditional execution is required.

## 2. Type Annotations & Documentation
- **Public API Type Hints**: Enforce explicit type annotations (`typing`) on all public function signatures, class methods, module interfaces, and data structures.
- **Structured Docstrings**: Write clear, structured docstrings for all public modules, classes, and methods detailing operational intent, arguments, return values, and raised exceptions.

## 3. Unit Testing & Bazel Execution Protocol
- **Standard `unittest` Framework**: Standardize test suites using the Python standard library `unittest` module (with tests named via `_test.py` suffix alongside target source files).
- **Example Launch Script Exception (`examples/*/launch.py`)**: Do **not** create `launch_test.py` unit test files for standalone example launch scripts under `examples/*/launch.py`. Verify example launch scripts via end-to-end execution against a live or local XMC environment.
- **`ExperimentStateGRpcApi` Wrapper Encapsulation & Clarity in Examples**: Prefer extending `ExperimentStateGRpcApi` (`experiment_state_api.py`) with missing public wrapper methods over accessing `stub._stub` directly. When mixing existing `ExperimentStateGRpcApi` public wrapper methods (`stub.get_work_unit`, `stub.create_artifact`, `stub.list_artifacts`) with unwrapped gRPC stub calls (`stub._stub.GetArtifact`) in `examples/`, document the public wrapper methods with a brief inline comment so reviewers and automated bots do not misidentify them as missing methods.
- **Hermetic Testing**: Isolate tests from external environment side-effects (file system mutations, process invocations, network RPCs) using `unittest.mock.patch` to keep unit test suites fast, deterministic, and self-contained.
- **Mandatory Bazel Test Execution**: Because Python packages in this repository rely on Bazel-generated Protobuf bindings (`api.protos...`), tests MUST be run using Bazel. ALWAYS execute unit tests via `./tools/run_affected_tests.py` or `bazel test <target>`. Do NOT attempt direct system `python3 -m unittest` or `pip install` invocations outside of Bazel.

## 4. Virtual Environments & Execution Safety
- **Python Virtual Environment**: Before executing or developing CLI tools under `tools/` or `deploy/`, ensure the local Python virtual environment is initialized and activated. Virtual environments must be explicitly created using **Python 3.13.14**:
  ```bash
  python3.13 -m venv ./tools/venv
  source ./tools/venv/bin/activate
  pip install -r ./tools/requirements.txt
  ```
- **Standalone Script Files Over `python3 -c`**: Always write Python code to dedicated `.py` files on disk using `write_to_file` rather than invoking inline commands via `python3 -c "..."` to prevent shell expansion and backtick command substitution errors.
