---
name: XMC Root Cause Analysis
description: Systematically diagnoses build errors, test failures, and runtime exceptions.
---

# Root Cause Analysis (RCA) Workflow

Provides a structured protocol for isolating failures, parsing logs, and determining root cause hypotheses.

## 1. Log Isolation & Error Extraction
Isolate error logs without dumping raw streams into prompt memory. Refer to `references/log_parsing_guide.md` for Bazel testlog parsing techniques.

## 2. Reproduce & Hypothesize
1. Reproduce failure locally using minimal invocation.
2. Formulate 1-2 concrete root cause hypotheses.
3. Validate hypothesis by inspecting isolated stack trace or state mutation.

## 3. Fix Verification & Test Assertion
Write a failing unit test reproducing the issue before applying the production fix.
