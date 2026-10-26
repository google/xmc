# XMC Documentation Topography Map & Source of Truth Guide

This document serves as the navigation and verification guide for the **XMC Review Docs Drift Skill** ([`SKILL.md`](../SKILL.md)). Together with the canonical machine-readable topography registry ([`xmc_docs_topography_map.json`](xmc_docs_topography_map.json)), it defines how source paths map to authoritative documentation files, how identifier claims are verified against source code, which areas act as recurring drift magnets, and how testable assertions are mechanically evaluated.

---

## 1. Code-to-Docs Topography Mapping Registry (`quadrants`)

To uphold the **One Canonical Home per Fact** rule, the authoritative Code-to-Docs mapping matrix is maintained in [`xmc_docs_topography_map.json`](xmc_docs_topography_map.json) under the `quadrants` key.

Each entry in `quadrants` defines:
- `name`: The codebase quadrant name (e.g., `Control Plane Services & CLI`, `Helm Charts, Addons & Profiles`, `Python SDKs (xmc, xdash)`).
- `source_patterns`: Repository-relative `PurePosixPath` glob patterns matching the core source directories and files in that quadrant.
- `doc_patterns`: Repository-relative `PurePosixPath` glob patterns matching the authoritative Markdown documentation files that must remain synchronized when `source_patterns` change.

---

## 2. Identifier Sources of Truth Registry (`identifier_sources`)

Documentation frequently asserts facts regarding CLI flags, environment variables, default network ports, Kubernetes resource kinds, and configuration parameters. These assertions MUST be tested against their code sources of truth—never against other documentation or human memory.

The canonical registry of identifier sources is maintained in [`xmc_docs_topography_map.json`](xmc_docs_topography_map.json) under the `identifier_sources` key:
- `identifier_type`: The category of documented identifier (`CLI Flags & Defaults`, `Environment Variables`, `Network Ports & Protocols`, `Helm Chart Parameters`, `Cluster Toolkit Blueprint Vars`, `Protobuf RPCs & Field Names`, `Python SDK Methods & Signatures`, `Container Images & Registries`).
- `source_patterns`: Repository-relative `PurePosixPath` globs pointing to the authoritative source files.
- `verification_command`: The mechanical `git grep` or `grep` probe used to verify documented identifiers against code truth.

---

## 3. Known Drift Magnets in XMC

Experience with customer reports and repository evolution has identified recurring categories of documentation drift:

1. **External Third-Party Helm Chart Repositories**:
   - *Failure Mode*: Upstream projects rename chart repositories, restructure chart names, or deprecate chart versions (e.g., legacy Altinity `clickhouse/clickhouse` vs official ClickHouse `clickstack/clickstack-operators` and `clickstack/clickstack`).
   - *Detection*: Automated HTTP probe of repository `index.yaml` and chart tarball URLs cited in `deploy/addons/**/README.md` and `INSTALL.md`.

2. **Helm Parameter Tables vs `values.yaml`**:
   - *Failure Mode*: Developers add, rename, or adjust default values of keys in `values.yaml` during feature work but omit updating the markdown table in the accompanying `README.md`.
   - *Detection*: Bi-directional cross-check between keys in `values.yaml` and the markdown parameter table.

3. **Relative Markdown Link Path Rot**:
   - *Failure Mode*: Moving or renaming subdirectories without updating relative `../` traversal links (e.g. `../../../../pkg/clickhouse/...` requiring 5 levels of traversal `../../../../../...`).
   - *Detection*: Mechanical filesystem existence check for all relative markdown file links.

4. **Legacy Blueprint & CTK Traversal Links**:
   - *Failure Mode*: References to legacy CTK v1 paths (`deploy/ctk_v1/README.md`) remaining in architecture or topology docs after the CTK v2 migration.
   - *Detection*: Scanning for references to deleted deployment directories.

5. **Example Worker Environment Sync (`worker.env` vs `launch.py`)**:
   - *Failure Mode*: An example's `launch.py` introduces new environment variables injected into the Kubernetes Pod or Job, but `worker.env` or the example's `README.md` does not list the new variable.
   - *Detection*: Diffing environment variables read in `examples/**/worker.py` against `worker.env` and `launch.py`.

6. **Hardcoded Version Numbers & Release Milestones**:
   - *Failure Mode*: Hardcoded references to outdated versions (e.g., `0.5.0` when repository is at `0.6.0+`) or obsolete release branch targets.
   - *Detection*: Auditing version assertions in `INSTALL.md` and `README.md` against git tags and `MODULE.bazel` / `pyproject.toml`.

7. **Open-Source Link Compliance**:
   - *Failure Mode*: Accidental introduction of prohibited internal URLs into open-source public documentation files.
   - *Detection*: Scanning documentation against [Markdown Documentation Guidelines](../../../rules/languages/markdown.md#1-markdown-formatting--structure).

---

## 4. Testable Assertion Verification Rubric

When auditing a document, treat every specific claim as a hypothesis that must be mechanically proven or disproven against the codebase:

```bash
# 1. Verify Go CLI Flag existence and default value
git grep -E '(StringVar|IntVar|BoolVar|Flag)\([^,]*"[^"]*"' cmd/

# 2. Verify Environment Variable consumption in source code
git grep -E 'os\.(Getenv|LookupEnv)\("([A-Z0-9_]+)"\)' pkg/ internal/ cmd/

# 3. Verify Python Environment Variable consumption
git grep -E 'os\.(environ\.get|getenv)\(['\''"][A-Z0-9_]+['\''"][,)]' sdks/ examples/

# 4. Verify Helm Parameter exists in values.yaml
grep -E '^[a-zA-Z0-9_]+:' deploy/helm/values.yaml

# 5. Verify Relative Markdown Links
# Resolves target relative to the document parent directory and confirms path exists.

# 6. Verify External HTTP URLs
curl -ILs --connect-timeout 5 --max-time 10 -o /dev/null -w "%{http_code}" "<target_url>"
```
