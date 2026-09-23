# Skills & Rules Benchmark Suite (`tools/benchmark/`)

This package provides CLI harnesses, telemetry extractors, and deterministic assertion evaluators for benchmarking candidate `.agents/` rule and skill modifications in the XManager on Cloud (XMC) repository.

---

## Package Components

- [`scenario_assertions.py`](scenario_assertions.py): Evaluates pre-registered file and regex assertions across `origin`, `transfer`, and `negative` benchmark scenarios and deterministically derives the multi-scenario verdict matrix.
- [`compare_trajectories.py`](compare_trajectories.py): Parses baseline and candidate subagent `transcript.jsonl` files, compares execution telemetry (turns, token footprint, tool call counts, error retries), and embeds the machine-derived [`scenario_assertions.py`](scenario_assertions.py) verdict matrix when `--assertions` is supplied.
- [`teardown_benchmark.py`](teardown_benchmark.py): Safely terminates benchmark subagents and removes temporary Git worktrees after trajectory comparison completes.
- [`analyze_diff_and_synthesize.py`](analyze_diff_and_synthesize.py): Extracts structured Git diffs and categorizes modifications across `.agents/rules/` and `.agents/skills/`.
- [`extract_telemetry.py`](extract_telemetry.py): Extracts structured subagent telemetry embedded in GitHub PR review comments.
- [`resolve_test_scope.py`](resolve_test_scope.py): Resolves affected Bazel test targets from Git diffs for selective test execution.
- [`analyze_ci_performance.py`](analyze_ci_performance.py): Analyzes CI workflow run durations and bottlenecks.

---

## Pre-Registered Scenario Assertions (`scenario_assertions.py`)

When validating a candidate rule or skill via the [XMC Trajectory Comparison Skill](../../.agents/skills/xmc_trajectory_comparison/SKILL.md), pass predicates must be registered **before** spawning subagent arms so the verdict cannot be post-hoc fitted to the observed transcripts.

### 1. JSON Specification Schema

A specification file is a JSON object mapping up to three scenario keys (`origin`, `transfer`, `negative`) to their target workspace paths and ordered assertion lists:

- **`origin`** *(required)*: The scenario reproducing the originating incident. A specification without `origin` is rejected.
- **`transfer`** *(optional)*: A held-out positive-set scenario verifying that the candidate rule generalizes beyond the incident file.
- **`negative`** *(optional)*: A negative-set control scenario verifying that the candidate rule does not trigger false positives or collateral edits on out-of-scope files.

Each present scenario object must specify:
- **`workspace`** *(required, non-empty string)*: Path to the directory evaluated for that scenario. Implicit fallback to `"."` is rejected to prevent accidental evaluation against the caller's working directory.
- **`assertions`** *(required, non-empty list)*: One or more assertion objects. A scenario with zero assertions is rejected, and a scenario passes only when **every** assertion in its list passes.

```json
{
  "origin": {
    "workspace": "/tmp/bench_origin_candidate",
    "assertions": [
      {
        "type": "file_contains",
        "path": "internal/controller/test_helpers.go",
        "pattern": "^type FakeJobSetClient struct"
      },
      {
        "type": "file_omits",
        "path": "internal/controller/test_helpers.go",
        "pattern": "mock\\.Anything"
      }
    ]
  },
  "transfer": {
    "workspace": "/tmp/bench_transfer_candidate",
    "assertions": [
      {
        "type": "file_omits",
        "path": "internal/storage/spanner/emulator/emulator.go",
        "pattern": "mock\\.Anything"
      }
    ]
  },
  "negative": {
    "workspace": "/tmp/bench_negative_candidate",
    "assertions": [
      {
        "type": "file_exists",
        "path": "tools/pr_review/comment_formatter.py"
      },
      {
        "type": "file_omits",
        "path": "tools/pr_review/comment_formatter.py",
        "pattern": "FakeJobSetClient"
      }
    ]
  }
}
```

### 2. Supported Assertion Predicate Kinds

| `type` | Required Fields | Pass Condition | Notes |
| :--- | :--- | :--- | :--- |
| `file_exists` | `path` | Target file exists (`Path.is_file()`) inside `workspace` | Fails if missing or if `path` points to a directory |
| `file_absent` | `path` | Target path does not exist (`not Path.exists()`) | Fails if file or directory exists at `path` |
| `file_contains` | `path`, `pattern` | Target file exists and regex `pattern` matches at least once | Compiled with `re.MULTILINE` (`^`/`$` match line boundaries; use `\A`/`\Z` for whole-file anchors) |
| `file_omits` | `path`, `pattern` | Target file exists and regex `pattern` does **not** match | Fails if the target file is absent (use `file_absent` when testing deletion) |

#### Security & Validation Guardrails
- **Workspace Path Confinement**: Every `path` must be relative and resolve strictly inside the scenario's `workspace` (`resolved_target.is_relative_to(resolved_workspace)`). Absolute paths (`/etc/passwd`) or parent traversal (`../../secret`) raise a `HandledError`.
- **Regex & Schema Validation**: Malformed regular expressions (`re.error`), unknown scenario keys, unknown assertion `type` values, or non-string `pattern`/`path` fields fail fast with a descriptive `HandledError`.

---

### 3. Deterministic Verdict Derivation Matrix

[`derive_verdict`](scenario_assertions.py) maps scenario pass/fail outcomes onto seven `Verdict` states. Failure predicates on any present scenario are always evaluated before returning partial-coverage `SHIP_*` verdicts:

| `origin` | `transfer` | `negative` | Derived `Verdict` | Meaning & Next Action |
| :---: | :---: | :---: | :--- | :--- |
| ✅ | ✅ | ✅ | **`SHIP`** | Candidate fixes the incident, generalizes to held-out positive cases, and avoids negative regressions |
| ✅ | ✅ | — *(omitted)* | **`SHIP (NO NEGATIVE REGRESSION SCENARIO)`** | Candidate passes origin and held-out transfer; negative evidence set was documented as empty |
| ✅ | — *(omitted)* | — / ✅ | **`SHIP (ORIGIN ONLY - GENERALIZATION UNTESTED)`** | Candidate passes origin (and negative if provided), but held-out transfer was not evaluated |
| ✅ | ❌ | ✅ / — | **`UNDER-GENERALIZED`** | Candidate overfit to the incident file; widen the positive set and re-derive the scope predicate |
| ✅ | ✅ / — | ❌ | **`OVER-GENERALIZED`** | Candidate caused collateral damage on the negative control; narrow the predicate to exclude the negative set |
| ✅ | ❌ | ❌ | **`WRONG PREDICATE`** | Candidate missed the held-out transfer case and broke the negative control; re-run boundary derivation |
| ❌ | *any* | *any* | **`REJECTED`** | Candidate failed to resolve the originating incident |

---

### 4. CLI Usage

#### Integrated with `compare_trajectories.py` (Recommended)
Pass `--assertions <spec.json>` when generating the trajectory comparison report so the `### 🏆 Pre-Registered Scenario Verdict` table and `verdict` JSON key are populated automatically:

```bash
python3 tools/benchmark/compare_trajectories.py \
  /path/to/baseline_transcript.jsonl \
  /path/to/candidate_transcript.jsonl \
  --assertions /tmp/assertions_branch.json \
  --output /tmp/benchmark_report_branch.md
```

#### Standalone Evaluation
Run [`scenario_assertions.py`](scenario_assertions.py) directly to validate a specification file and emit the JSON outcome and optional Markdown report:

```bash
python3 tools/benchmark/scenario_assertions.py \
  /tmp/assertions_branch.json \
  --output /tmp/verdict_matrix.md
```

---

### 5. Running Unit Tests

```bash
python3 -m unittest tools.benchmark.scenario_assertions_test tools.benchmark.compare_trajectories_test
# or via Bazel:
bazel test //tools/benchmark:scenario_assertions_test //tools/benchmark:compare_trajectories_test
```
