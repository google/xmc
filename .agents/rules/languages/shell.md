# Shell Scripting Guidelines

Standards for shell script usage across the codebase.

---

## 1. Minimal Scope
- **Thin Entrypoints Only**: Restrict Shell scripts strictly to thin entrypoint wrappers or bootstrap sequences.
- **Defer Complex Logic to Python**: Never write multi-step logic or regex parsing in Shell. Defer to Python scripts.

## 2. Execution Flags & Double-Quoting
- **Strict Flags**: Every script must start with `set -euo pipefail`.
- **Variable Expansion Quoting**: Always double-quote variable expansions with curly braces (`"${VAR}"`).
- **Safe Command Substitution**: Avoid inline scripts with backticks in double quotes. Pass content safely via standalone `.py` scripts or stdin heredocs.
