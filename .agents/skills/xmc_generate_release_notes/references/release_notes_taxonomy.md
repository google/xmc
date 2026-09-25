# XMC Release Notes Taxonomy & Categorization Reference

This document defines the categorization standards, gating rules, and narrative formatting guidelines used to generate structured, meaningful release notes for XMC.

---

## 1. Core Taxonomy & Section Breakdown

Release notes generated for XMC are structured into a unified document with four functional sections:

### Section 1: Public Changes (Available Out-of-the-Box)
Contains all changes that directly impact end users, cluster users, or administrators without requiring any non-default feature flags.

1. **🚀 Major Highlights & Promoted Capabilities**:
   - **Feature Flag Default Flips**: Any capability whose deployment flag or Helm toggle was changed from `false` to `true` in this release.
   - **Major User-Facing Additions**: High-impact capabilities activated out-of-the-box.
   - **Action Required / Breaking Changes**: Schema changes, required Spanner migrations, deprecated flags, or new IAM permissions.
2. **✨ New Features & Improvements**:
   - User-facing enhancements in active services (UI, Experiment State Server, Actuator, Reconciler, XID Service).
3. **🐛 User-Facing Bug Fixes & Stability**:
   - Bug fixes in active services and user-facing frontend.

---

### Section 2: Production-Ready Features Behind Feature Flags (Safe to Enable)
Contains all features that are fully implemented, tested, and verified safe to enable, but whose default activation is temporarily postponed solely to align with managed solution milestones.

> [!IMPORTANT]
> **Subsystem Grounding & Anti-Embellishment Invariant**:
> Agents must ground subsystem headings and feature titles strictly in repository naming contracts (`deploy/`, `pkg/`, and `tools/release/feature_flags.py`). Prohibit speculative extrapolation, marketing embellishments, or buzzword additions (e.g., use "Pluggable Components for XMC" or the clean derived name "Components", NOT "AI Components").

For each approved/production-ready feature:
- **Subsystem Title**: Explicit contract-grounded name of the gated subsystem.
- **Activation Flag & File**: Exact parameter name and configuration file as discovered from the repository (e.g. `<flag_name>: true` in `deploy/ctk_v2/xmanager-cloud-deployment.yaml` and corresponding Helm template values in `deploy/helm/`).
- **Narrative Summary of Changes**: Synthesized description of ready endpoints, controllers, UI components, and capabilities added in this release with grouped PR links (`#123, #124`).

---

### Section 3: In-Development / Experimental Features Behind Flags
Contains features under active ongoing development that are experimental, opt-in, and not yet guaranteed to be fully functional.

> [!WARNING]
> Always include a clear disclaimer callout under Section 3 noting that these gated features are in active development, experimental, and may not yet be functional. Subsystem titles must strictly follow the Subsystem Grounding & Anti-Embellishment Invariant.

For each experimental gated feature:
- **Subsystem Title**: Explicit contract-grounded name of the gated subsystem.
- **Activation Flag & File**: Exact parameter name and configuration file as discovered from the repository (e.g. `<flag_name>: true` in `deploy/ctk_v2/xmanager-cloud-deployment.yaml` and corresponding Helm template values in `deploy/helm/`).
- **Narrative Summary of Changes**: Synthesized description of in-progress capabilities with grouped PR links.

---

### Section 4: Operational, Infrastructure & Engineering Velocity
Changes that are critical for cluster operators, DevOps, CI/CD, and internal architecture, but do not directly alter the end-user interface.

1. **⚙️ Operational & Infrastructure Updates**:
   - Cluster Toolkit modules, Terraform, Helm chart infrastructure templates, GKE/Kueue configuration, Spanner index optimizations, OAuth/OIDC providers.
2. **🛠️ Developer Velocity & Architecture Progress**:
   - CI workflows, Bazel build optimizations, ARC runner improvements, linting rules, internal code refactoring, and test fixtures.

---

## 2. Semantic Commit Classification & Section Routing (Agent Reasoning)

Because commit categorization requires human and NLP semantic reasoning that static heuristics struggle with (e.g. distinguishing a user-impacting UI feature from internal test fixtures), the agent maps `raw_commits` and `flag_transitions` using the following decision rules:

1. **Section 1 (Public Out-of-the-Box Changes)**:
   - **Highlights & Promoted Capabilities**: Subsystems with `is_promotion_to_default == true` in `flag_transitions` or high-impact out-of-the-box milestones.
   - **New Features**: Commits with `commit_type == "feat"` that touch active, non-gated services (UI, Experiment State Server, Actuator, Reconciler, XID Service).
   - **Bug Fixes**: Commits with `commit_type == "fix"` that resolve bugs in active services.
2. **Section 2 (Production-Ready Preview Features)**:
   - Commits (`feat`, `fix`, `docs`) touching subsystems whose flags are present in `approved_flags` or whose status is `approved_preview_toggle` in `gated_subsystems_status`.
   - Subsystem titles must strictly adhere to the Subsystem Grounding & Anti-Embellishment Invariant.
3. **Section 3 (In-Development / Experimental Features)**:
   - Commits (`feat`, `fix`, `docs`) touching subsystems whose flags are default `false` and NOT approved (`status == "experimental_preview_toggle"` in `gated_subsystems_status`).
4. **Section 4 (Operational & Developer Progress)**:
   - Commits with `commit_type` in `refactor`, `chore`, `docs`, `test`, `build`, `ci`.
   - Commits touching `.github/`, `tools/`, `.agents/`, or internal developer tooling.

---

## 3. Automated Version Increment Determination (SemVer)

The release metadata engine (`tools/release/semver_engine.py`) calculates the baseline `recommended_bump` (`major`, `minor`, `patch`, or `none`) using deterministic trigger evaluation across `X.Y.Z` versions:

### A. Deterministic Engine Rules (`semver_engine.py`):
* **Major Bump (`(X+1).0.0`)**:
  - Triggered when `has_breaking` is `true` (commits containing `!:` in the Conventional Commit header or `BREAKING CHANGE:` in the commit subject/description).
* **Minor Bump (`X.(Y+1).0`)**:
  - Any new feature commit (`has_ootb_features`: `commit_type == "feat"`).
  - Any production-ready feature approved for enablement behind flags (`has_approved`: non-empty `approved_flags`).
  - Any feature flag promoted from `false` to `true` (`has_promotions`: `is_promotion_to_default == true`).
* **Patch Bump (`X.Y.(Z+1)`)**:
  - Triggered when changes contain bug fixes (`has_ootb_fixes`: `commit_type == "fix"`), security patches, internal refactoring, CI improvements, or experimental preview work without activating new capabilities.

### B. Advisory Metadata Signals & Pre-1.0 (`0.Y.Z`) Operator Review:
In addition to the automated bump calculation, the metadata extraction output surfaces advisory signals for the agent and operator during the Interactive Review Gate:
* **Database Migrations (`has_migrations`)**: Detected when commits touch `migrations/` directories or `.sql`/`.ddl` files and included in `recommended_version.rationale`, allowing operators to verify whether a migration requires a minor (`0.(Y+1).0`) or major (`(X+1).0.0`) increment.
* **New Configuration Knobs (`flag_transitions`)**: Newly introduced Cluster Toolkit variables or Helm toggles are surfaced with `transition_type == "added"` in `flag_transitions`.
* **Pre-1.0 (`0.Y.Z`) Versioning Convention**: If `has_breaking` triggers a baseline `major` recommendation (`1.0.0`) while the project remains in `0.Y.Z` pre-release development, the agent highlights this during Step 5 so the operator can confirm `0.(Y+1).0` if `1.0.0` graduation is not yet intended.

---

## 4. Narrative Synthesis vs. PR Dumping

To ensure release notes are readable and constructive:

* **Group Related PRs**: If multiple PRs contribute to the same subsystem (e.g. PRs adding backend stubs, cache helpers, and UI telemetry for ROD), synthesize them into a single cohesive bullet point citing all PR numbers:
  * ❌ *Bad*:
    * `feat(rod): sort listSnapshot by key name (#435)`
    * `feat(rod): implement GetSubmitQueue primary data endpoint (#417)`
    * `feat(rod): add GetLocalQueuesForClusterQueue helper to KueueCache (#405)`
  * ✅ *Good*:
    * **ROD Queue Telemetry & Caching**: Added primary queue data endpoints, Kueue cache local queue helpers, and snapshot sorting by key name (#405, #417, #435).
* **Explain Impact, Not Just Mechanics**: Describe what the change enables rather than repeating the commit verb.
