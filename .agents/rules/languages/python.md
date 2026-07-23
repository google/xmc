# Python Language Guidelines

Coding standards, style guidelines, version enforcement, and best practices for Python development.

---

## 1. Version Enforcement & Style
- **Python 3.14+ Version Enforcement**: Target Python 3.14+ runtime features and follow PEP 8 strictly.
- **Standard Library Preference**: Strongly prefer standard library modules (`pathlib`, `argparse`, `dataclasses`, `enum`, `subprocess`, `typing`) over third-party external dependencies.
- **Prefer Python over Shell**: Write Python scripts instead of complex Shell scripts for automation and CLI tools.

## 2. Type Hints & Environment Standards
- **Explicit Public API Type Hints**: Enforce explicit type annotations (`typing`) on all public function signatures, method parameters, and return types.
- **Local Virtual Environment Requirements**: Execute scripts and tools within dedicated local virtual environments (`tools/setup_venv.py` or `.venv`). Never rely on global system Python packages.
- **Structured Docstrings**: Write clear docstrings detailing intent, arguments, return values, and raised exceptions.

## 3. Unit Testing & Bazel Execution Protocol
- **Standard `unittest` Framework**: Standardize test suites using `unittest` (`_test.py` files).
- **Hermetic Testing**: Isolate tests using `unittest.mock.patch`.
- **Mandatory Bazel Execution**: Run unit tests via `./tools/run_affected_tests.py` or `bazel test <target>`.
