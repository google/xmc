# Python Language Guidelines

This document outlines coding standards, style guidelines, and best practices for Python development across the codebase.

---

## 1. Style & Conventions
- **Google Python Style Guide**: Strictly follow the Google Python Style Guide (`go/pystyle`) and best practice recommendations in `go/python-tips`.
- **Standard Library Preference**: Prefer built-in Python standard library modules (`pathlib`, `argparse`, `dataclasses`, `enum`, `subprocess`, `typing`) over third-party dependencies or custom utility wrappers.
- **Prefer Python over Shell**: Write Python scripts instead of complex Shell scripts for automation, operational hooks, and CLI tools whenever procedural logic, JSON/YAML parsing, or conditional execution is required.

## 2. Type Annotations & Documentation
- **Public API Type Hints**: Enforce explicit type annotations (`typing`) on all public function signatures, class methods, module interfaces, and data structures.
- **Google-Style Docstrings**: Write clear, structured Google-style docstrings for all public modules, classes, and methods detailing operational intent, arguments, return values, and raised exceptions.

## 3. Unit Testing & Hermetic Mocks
- **Standard `unittest` Framework**: Standardize test suites using the Python standard library `unittest` module (with tests named via `_test.py` suffix alongside target source files).
- **Hermetic Testing**: Isolate tests from external environment side-effects (file system mutations, process invocations, network RPCs) using `unittest.mock.patch` to keep unit test suites fast, deterministic, and self-contained.
