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
Contains all features that are fully implemented, tested, and verified safe to enable, but whose default activation is temporarily postponed solely to align with managed solution milestones (e.g. `rod_deployment_enabled`, `health_service_deployment_enabled`).

> [!IMPORTANT]
> **Subsystem Grounding & Anti-Embellishment Invariant**:
> Agents must ground subsystem headings and feature titles strictly in repository naming contracts (`deploy/`, `pkg/`, and `tools/release/feature_flags.py`). Prohibit speculative extrapolation, marketing embellishments, or buzzword additions (e.g., use "Pluggable Components for XMC" or the clean derived name "Components", NOT "AI Components").

For each approved/production-ready feature:
- **Subsystem Title**: Explicit contract-grounded name of the gated subsystem.
- **Activation Flag & File**: Exact parameter name and configuration file (e.g. `rod_deployment_enabled: true` in `deploy/ctk_v2/xmanager-cloud-deployment.yaml` and `rodDeployment.enabled: true` in Helm `values.yaml`).
- **Narrative Summary of Changes**: Synthesized description of ready endpoints, controllers, UI components, and capabilities added in this release with grouped PR links (`#123, #124`).

---

### Section 3: In-Development / Experimental Features Behind Flags
Contains features under active ongoing development that are experimental, opt-in, and not yet guaranteed to be fully functional (e.g. `enable_components`, `dashboard_service_deployment_enabled`).

> [!WARNING]
> Always include a clear disclaimer callout under Section 3 noting that these gated features are in active development, experimental, and may not yet be functional. Subsystem titles must strictly follow the Subsystem Grounding & Anti-Embellishment Invariant.

For each experimental gated feature:
- **Subsystem Title**: Explicit contract-grounded name of the gated subsystem.
- **Activation Flag & File**: Exact parameter name and configuration file (e.g. `enable_components: true` in `deploy/ctk_v2/xmanager-cloud-deployment.yaml` and `components.enabled: true` in Helm `values.yaml`).
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

The release generator calculates the recommended version bump (`Major`, `Minor`, or `Patch`) using deterministic rules:

### A. Pre-1.0 Zero-Major Versioning (`0.Y.Z`):
* **Minor Bump (`0.(Y+1).0`)**:
  - Any new feature enabled out-of-the-box (`commit_type == "feat"`).
  - Any production-ready feature approved for enablement behind flags (`approved_flags`).
  - Any feature flag promoted from `false` to `true` (`is_promotion_to_default`).
  - Any new deployment configuration knob introduced in Cluster Toolkit or Helm.
  - Any database schema migration or breaking change in `0.y.z` development.
* **Patch Bump (`0.Y.(Z+1)`)**:
  - Contains **only** bug fixes, security patches, internal refactoring, CI improvements, or experimental preview work without activating new capabilities out-of-the-box.

### B. Standard Post-1.0 Versioning (`X.Y.Z`):
* **Major Bump (`(X+1).0.0`)**: Incompatible API changes, breaking Spanner migrations, removed CTK flags.
* **Minor Bump (`X.(Y+1).0`)**: Backwards-compatible new features, approved feature flags, flag promotions, new config knobs.
* **Patch Bump (`X.Y.(Z+1)`)**: Backwards-compatible bug fixes and stability improvements only.

---

## 4. Narrative Synthesis vs. PR Dumping

To ensure release notes are readable and constructive:

* **Group Related PRs**: If 4 PRs contribute to the same subsystem (e.g. PRs adding backend stubs, cache helpers, and UI telemetry for ROD), synthesize them into a single cohesive bullet point citing all PR numbers:
  * ❌ *Bad*:
    * `feat(rod): sort listSnapshot by key name (#435)`
    * `feat(rod): implement GetSubmitQueue primary data endpoint (#417)`
    * `feat(rod): add GetLocalQueuesForClusterQueue helper to KueueCache (#405)`
  * ✅ *Good*:
    * **ROD Queue Telemetry & Caching**: Added primary queue data endpoints, Kueue cache local queue helpers, and snapshot sorting by key name (#405, #417, #435).
* **Explain Impact, Not Just Mechanics**: Describe what the change enables rather than repeating the commit verb.
