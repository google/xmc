# Shell Scripting Guidelines

This document defines standards for shell script usage across the codebase.

---

## 1. Minimal Shell Scope
- **Thin Entrypoints Only**: Restrict Shell scripts strictly to thin entrypoint wrappers, bootstrap sequences, or simple binary invocation triggers.
- **Defer Complex Logic to Python**: Never write multi-step logic, procedural string parsing, regex evaluation, or nested conditional blocks in Shell scripts. Defer all non-trivial logic to dedicated Python scripts.

## 2. Safety Options & Robust Execution
- **Strict Execution Flags**: Every Bash script must include strict safety flags at the top of the file:
```bash
#!/usr/bin/env bash
set -euo pipefail
```
- **Variable Expansion Quoting & Braces**: Always double-quote variable expansions and use explicit curly braces (`"${VAR}"`) to prevent word splitting, variable name ambiguity, path globbing issues, and shell expansion vulnerabilities.
- **Executable File Modes**: Ensure shell scripts have appropriate executable permissions set (`chmod +x`).
