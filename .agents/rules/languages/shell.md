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

## 3. Command Substitution & Payload Truncation Safety
- **Avoid Inline Scripts and Double-Quoted Payload Strings**: Never pass Python code strings (`python3 -c "..."`), API body payloads, or strings containing Markdown/code formatting inside double quotes (`"..."`) in shell commands.
- **Backtick Command Substitution Risk**: Bash treats backticks (`` `...` ``) inside double-quoted strings as command substitution expressions. When text containing Markdown inline code (e.g., `` `request()` ``) is evaluated inside double quotes, Bash attempts to execute the backticks as shell commands, fails, and replaces the expression with empty output—silently truncating and corrupting the payload.
- **Use Standalone Script Files or Safe Channels**: Always write multi-line logic, Python code, or comment payloads containing code/markdown to a dedicated `.py` file using `write_to_file` and execute the script directly (e.g. `python3 script.py`), or pass content safely via stdin (e.g., using single-quoted heredocs) or process-level environment variables (avoiding inline shell assignments like `VAR="..."`).



## 4. Terminal Command Execution & Sandbox Safety
- **Forbidden Unconstrained Traversals**: NEVER execute unconstrained root searches (`find /`, `find ~`, `grep -r /`) outside the active workspace directory. Bounded searches MUST specify explicit workspace paths or subdirectories (`find .agents/`, `find tools/`).
- **Avoid Heavy Compound Chaining**: Avoid chaining long compound command chains (`cmd1 && cmd2 && cmd3 && cmd4`) in a single `run_command` invocation, as network or sandbox resets can break terminal pipes (`connection reset by peer`). Split preparation and execution into discrete single commands.
