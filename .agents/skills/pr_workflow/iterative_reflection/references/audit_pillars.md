# Pragmatism Filter & Draft PR Safety Rules

## Pragmatism & Anti-Hallucination Filter
1. **Recurrence Check**: Omit one-off edge cases.
2. **ROI Check**: Ensure proposed enhancements save more turns than maintenance cost.
3. **Zero-Friction Result**: Allow "No Action Needed" as a valid reflection outcome.

## Draft PR Creation Safety
- **Subagent Isolation**: Use worker subagents starting from clean `origin/main` for PR creation.
- **Single-Workspace Fallback**: If subagent workspace isolation is unavailable, do NOT perform local git branch switches or stashing in active worktree. Present proposals directly in chat/artifact.
