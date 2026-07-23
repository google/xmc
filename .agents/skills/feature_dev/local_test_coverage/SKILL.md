---
name: XMC Local Test Coverage
description: Executes local unit tests and measures code coverage across affected packages.
---

# Local Test Coverage Workflow

Guides agents through discovering, executing, and reporting unit test coverage.

## 1. Identify Affected Packages
Determine modified source files and identify corresponding unit test files. Enforce 1-to-1 file test cohesion.

## 2. Execute Tests & Measure Coverage
Run tests and generate coverage reports using language-specific tools. Refer to `references/coverage_commands.md` for detailed commands across Go, Python, and TypeScript.

## 3. Coverage Analysis & Reporting
Parse report outputs (e.g. `lcov.dat`, HTML summaries) and verify that all newly added lines and branch logic have active test coverage.
