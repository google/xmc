# Python Language Guidelines

Coding standards, style guidelines, and best practices for Python development.

---

## 1. Style & Conventions
- **PEP 8 & Python 3.14+**: Follow PEP 8 strictly and target Python 3.14+ standard library features.
- **Standard Library Preference**: Prefer built-in modules (`pathlib`, `argparse`, `dataclasses`, `enum`, `subprocess`, `typing`) over third-party dependencies.
- **Prefer Python over Shell**: Write Python scripts instead of complex Shell scripts for automation and CLI tools.

## 2. Type Hints & Docstrings
- **Public API Type Hints**: Enforce explicit type annotations (`typing`) on public function signatures and class methods.
- **Structured Docstrings**: Write clear docstrings detailing intent, arguments, return values, and raised exceptions.

## 3. Unit Testing & Bazel Execution Protocol
- **Standard `unittest` Framework**: Standardize test suites using `unittest` (`_test.py` files).
- **Hermetic Testing**: Isolate tests using `unittest.mock.patch`.
- **Mandatory Bazel Execution**: Run unit tests via `./tools/run_affected_tests.py` or `bazel test <target>`.
