---
name: XMC Generate Release Notes
description: Generates structured, intelligent release notes segmented into out-of-the-box user-facing changes, behind-the-flag preview features, and operational/dev progress.
---

# Intelligent Release Notes Generation Playbook

This skill provides step-by-step procedures for extracting release metadata, detecting feature flag transitions, and synthesizing high-impact, audience-segmented release notes for XMC releases.

> [!IMPORTANT]
> All execution steps in this skill MUST strictly adhere to the policy rules defined in [Core Engineering Standards](../../rules/core_engineering.md) and [Skills & Automation Guidelines](../../rules/skills_and_automation.md).

---

## Guidelines for Using CLI Tools

When invoking helper tools under `tools/` (e.g., `extract_release_metadata.py`):
1. **Do not read tool source code**: Do not open or read the Python source code of helper tools. Refer to [Skills & Automation Guidelines](../../rules/skills_and_automation.md#5-helper-tool-execution-policy--safety-constraints).
2. **Use `--help`**: Run tools with `--help` to inspect parameters and usage.
3. **Parse Structured Output & Handle Errors**: All tools output structured JSON (exit code 0 for success, 1 for handled error, 2 for unhandled error).
4. **Shell & Payload Safety**: Execute standalone `.py` script files or pass arguments via script parameters. Avoid double-quoted inline commands (`python3 -c "..."`).

---

## 1. Release Range & Git Reference Discovery

1. **Fetch Remote Tags & Canonical References**:
   Ensure local repository state has the latest tags and canonical commit history without switching branches:
   ```bash
   git fetch origin main --tags
   ```

2. **Identify Base and Target References**:
   - Determine target version (e.g., `0.6.0`, `origin/main`, or a specific release commit SHA). Default to `origin/main` for standard releases.
   - If not specified by the user, discover the latest previous tag on `origin/main`:
     ```bash
     git describe --tags --abbrev=0 origin/main^ 2>/dev/null || git rev-list --max-parents=0 origin/main
     ```
3. **Inspect Commit Range**:
   ```bash
   git log --oneline <base_tag>..<target_ref>
   ```

---

## 2. Deterministic Metadata Extraction & Approved Flags Discovery

1. **Discover Gated Subsystems & Feature Flags**:
   The release generator checks current disabled feature flags in `deploy/ctk_v2/xmanager-cloud-deployment.yaml` and `deploy/helm/values.yaml`.
2. **Interactive Check for Approved / Production-Ready Flags**:
   Before or during metadata extraction, prompt the user in chat:
   > *"Discovered disabled feature flags: `rod_deployment_enabled`, `health_service_deployment_enabled`, `enable_components`, `dashboard_service_deployment_enabled`, `enable_metrics`."*
   > *"Are any of these flags approved / production-ready for this release (safe to be enabled)?"*
   
   > [!TIP]
   > When specifying `--approved-flags`, provide both the CTK variable (e.g. `enable_metrics`, `health_service_deployment_enabled`) and the Helm manifest path (e.g. `metrics.enabled`, `healthServiceDeployment.enabled`) to ensure commits across both deployment layers are recognized as approved preview features.

3. **Execute Release Metadata Extraction Tool**:
   Run the extraction helper tool with `--approved-flags` and pin to the canonical `--target-ref` (`origin/main`):
   ```bash
   TARGET_REF="${TARGET_REF:-origin/main}"
   METADATA_FILE="${ARTIFACT_DIR:-/tmp}/release_metadata.json"

   python3 ./tools/release/extract_release_metadata.py \
       --base-ref <base_tag> \
       --target-ref "${TARGET_REF}" \
       --approved-flags rod_deployment_enabled rodDeployment.enabled enable_metrics metrics.enabled \
       --output "${METADATA_FILE}"
   ```
4. **Inspect Structured Output**:
   The output JSON provides:
   - `metadata`: Git commit, PR counts, and `approved_flags` list.
   - `recommended_version`: Automated SemVer recommendation (`current_version`, `recommended_version`, `recommended_bump`, `rationale`, boolean bump triggers).
   - `flag_transitions`: Flips (`false` -> `true`, `true` -> `false`) and configuration additions.
   - `gated_subsystems_status`: Current status of each gated subsystem (Active vs Production-Ready vs Experimental).
   - `raw_commits`: Full list of parsed git commits with `commit_hash`, `short_hash`, `commit_type`, `scope`, `description`, `pr_number`, and `files_changed`.

---

## 3. Semantic Commit Classification & Section Routing (Agent Reasoning)

The agent reads `raw_commits`, `flag_transitions`, and `gated_subsystems_status` to synthesize and classify commits into the 4-section layout using semantic reasoning:

> [!IMPORTANT]
> **Subsystem Grounding & Anti-Embellishment Invariant**:
> Ground subsystem headings and feature titles strictly in repository naming contracts (`deploy/`, `pkg/`, and `tools/release/feature_flags.py`). Prohibit speculative extrapolation, marketing embellishments, or buzzword additions (e.g., use "Pluggable Components for XMC" or the clean derived name "Components", NOT "AI Components").

1. **Section 1: Public Changes (Available Out-of-the-Box)**:
   - **Highlights & Promoted Capabilities**: Subsystems with `is_promotion_to_default == true` in `flag_transitions`, or major new out-of-the-box milestones.
   - **New Features**: Commits with `commit_type == "feat"` that touch active, non-gated services (UI, Experiment State Server, Actuator, Reconciler, XID Service).
   - **Bug Fixes**: Commits with `commit_type == "fix"` that resolve bugs in active services.
2. **Section 2: Production-Ready Features Behind Feature Flags (Safe to Enable)**:
   - Subsystems whose feature flags are listed in `approved_flags` or whose status is `approved_preview_toggle` in `gated_subsystems_status`.
   - Synthesize all commits (`feat`, `fix`, `docs`) touching this subsystem into cohesive bullet points.
3. **Section 3: In-Development / Experimental Features Behind Flags**:
   - Subsystems whose feature flags are default `false` and NOT approved (`status == "experimental_preview_toggle"`).
   - Clearly state that capabilities are under active ongoing development and may not yet be functional.
4. **Section 4: Operational, Infrastructure & Engineering Velocity**:
   - Commits with `commit_type` in `refactor`, `chore`, `docs`, `test`, `build`, `ci`, or developer tooling (`tools/`, `.github/`, `.agents/`).

---

## 4. Semantic Synthesis & Narrative Formatting

Using the classified commits and the [Release Notes Taxonomy Reference](references/release_notes_taxonomy.md), draft the release notes according to the 4-section layout:

```markdown
# XMC Release <Recommended_Version> (e.g., 0.7.0)

> **Release Version Determination**: <recommended_bump> bump (`<current_base_version>` -> `<recommended_version>`).
> *Rationale*: <rationale>

---

## 🌟 1. Public Changes (Available Out-of-the-Box)

### 🚀 Major Highlights & Promoted Capabilities
* **[Promoted to Default] Feature Name**: Description of the capability newly enabled by default (`flag_name: true`), and why it matters to users (#PRs).
* **[Action Required / Breaking]**: Any Spanner migration, new GCP IAM permissions, or altered configuration requirements.

### ✨ New Features & Improvements
* **Topic Title**: Cohesive summary of user-facing enhancements across active services (#PRs).

### 🐛 User-Facing Bug Fixes & Stability
* Description of fixes affecting user workflows (#PRs).

---

## 🚀 2. Production-Ready Features Behind Feature Flags (Safe to Enable)
> The following capabilities are fully developed, verified, and safe to be enabled. Their default enablement is temporarily postponed solely to align with managed solution milestones.

### 🔹 [Flag: `<ctk_flag_name>`] <Subsystem Display Name>
* **How to enable**: Set `<ctk_flag_name>: true` in `deploy/ctk_v2/xmanager-cloud-deployment.yaml` (or the nested `<helm_flag_path>: true` in Helm `values.yaml`).
* **What's New**:
  * Synthesized summary of ready endpoints, controllers, UI components, or APIs (#PRs).

---

## 🧪 3. In-Development / Experimental Features Behind Flags
> [!WARNING]
> The following capabilities are under active ongoing development, experimental, and may not yet be functional. They are available for early preview and evaluation by enabling their deployment flags in your Cluster Toolkit deployment YAML or Helm `values.yaml`.

### 🔹 [Flag: `<ctk_flag_name>`] <Subsystem Display Name>
* **How to enable**: Set `<ctk_flag_name>: true` in `deploy/ctk_v2/xmanager-cloud-deployment.yaml` (or the nested `<helm_flag_path>: true` in Helm `values.yaml`).
* **What's New**:
  * Synthesized summary of experimental capabilities (#PRs).

---

## ⚙️ 4. Operational, Infrastructure & Engineering Velocity
> Changes relevant to cluster operators, CI/CD, and internal architecture that do not alter the end-user interface.

* **Cluster & Portability**: Infrastructure updates, Terraform/Helm changes, OIDC/auth enhancements (#PRs).
* **CI & Build Performance**: Bazel caching, ARC runner optimizations, test scoping (#PRs).
* **Refactoring & Code Quality**: Internal architectural cleanups, lifecycle migrations, linting rules (#PRs).
```

### Narrative Synthesis Principles:
- **Group Related PRs**: Never output disjointed lists of PR titles. Combine multiple PRs under common feature headers.
- **Clear Flag Activation Instructions**: For every gated feature, explicitly mention the exact configuration flags (CTK YAML and Helm `values.yaml`) where operators can enable it.
- **Explain User Value**: Highlight what users or operators can accomplish with the changes.

---

## 5. Draft Generation & Interactive Review Gate

1. **Present Version Recommendation in Chat**:
   Report the proposed version increment and technical rationale clearly in the chat:
   * **Base Version**: `<base_version>`
   * **Proposed Release Version**: `<recommended_version>` (`<recommended_bump>` bump)
   * **Triggers**: List the concrete commits, migrations, promotions, or OOTB features justifying the bump.
2. **Generate Draft Markdown File**:
   Write the structured release notes to a dedicated markdown file:
   * Recommended path: `"${ARTIFACT_DIR:-/tmp}/release_notes_v<version>.md"`
   * Include the complete 4-section layout with flag instructions and narrative synthesis.
3. **Interactive Review**:
   Present the draft file path to the user for review. If the user requests adjustments (e.g. changing version number, tuning descriptions, or regrouping entries), use `replace_file_content` to update the markdown file.

---

## 6. Mandatory Confirmation Gate & Release Publishing

> [!CAUTION]
> **STRICT INVARIANT: NEVER CREATE A GIT TAG OR GITHUB RELEASE WITHOUT EXPLICIT CONFIRMATION FROM THE USER.**
>
> The agent MUST NOT call `publish_release.py`, run `git tag`, `git push origin <tag>`, or `gh release create` proactively or autonomously. The agent MUST stop and wait after presenting the draft and recommended version. Only proceed to step 2 after the user provides an explicit affirmative instruction (e.g., "approve release", "create tag and release", "publish release").

1. **Wait for Explicit User Approval**:
   Confirm that the user has explicitly approved both the **version number** and the **release notes content**.
2. **Publish Release as GitHub Draft (Post-Approval Only)**:
   Execute the release publishing tool with `--draft` and explicitly pin `--target-commit origin/main` to ensure the release tag points to the canonical verified main commit:
   ```bash
   NOTES_FILE="${ARTIFACT_DIR:-/tmp}/release_notes_v<version>.md"
   python3 ./tools/release/publish_release.py \
       --tag <version> \
       --target-commit origin/main \
       --notes-file "${NOTES_FILE}" \
       --draft
   ```
   > [!NOTE]
   > Publishing as a draft gives the team a collaborative proofreading window (e.g. 1–2 hours) on GitHub Releases to review the changelog and propose adjustments before manually publishing it to the public. Omit `--draft` only if the user explicitly instructs to publish publicly immediately.
3. **Confirm Publication**:
   Provide the user with the generated GitHub Release URL and confirm that the git tag was successfully pushed.
