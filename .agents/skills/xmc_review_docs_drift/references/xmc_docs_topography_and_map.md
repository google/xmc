# XMC Documentation Topography Map & Source of Truth Registry

This document serves as the canonical navigation instrument for the **XMC Review Docs Drift Skill** ([`SKILL.md`](../SKILL.md)). It maps every major codebase quadrant to its authoritative documentation files, catalogs the identifier sources of truth, indexes known drift magnets, and defines the verification rubric for testable assertions.

---

## 1. Code-to-Docs Topography Mapping Matrix

When a source file changes in a delta audit (Mode 2) or during a full repository sweep (Mode 1), consult this matrix to locate the corresponding documentation files that must remain synchronized:

| Codebase Quadrant | Core Source Directories / Files | Authoritative Documentation Files |
| :--- | :--- | :--- |
| **Control Plane Services & CLI** | `cmd/*/`<br>`pkg/apis/`<br>`pkg/auth/`<br>`pkg/clickhouse/`<br>`pkg/components/` | `README.md`<br>`CONTRIBUTING.md`<br>`ABSENCE.md`<br>`cmd/**/README.md`<br>`pkg/**/README.md`<br>`docs/architecture/*.md`<br>`docs/design/*.md` |
| **Core Flows, Storage & Internal Packages** | `internal/*/` | `docs/flows/*.md`<br>`internal/**/README.md`<br>`internal/storage/AGENTS.md` |
| **gRPC, Protobuf & Filter Contracts** | `api/protos/**/*.proto`<br>`api/filter/`<br>`api/testdata/` | `docs/architecture/components.md`<br>`docs/flows/*.md`<br>`cmd/ingestion_gateway/README.md`<br>`cmd/experiment_state_server/README.md` |
| **Cluster Toolkit & Blueprints** | `deploy/ctk_v2/`<br>`deploy/clickhouse/`<br>`deploy/local/`<br>`deploy/cli/`<br>`deploy/ci/` | `INSTALL.md`<br>`deploy/ctk_v2/README.md`<br>`deploy/local/README.md`<br>`deploy/cli/README.md`<br>`deploy/ci/README.md`<br>`docs/developer.md` |
| **Helm Charts, Addons & Profiles** | `deploy/helm/`<br>`deploy/profiles/`<br>`deploy/addons/` | `deploy/helm/README.md`<br>`deploy/profiles/**/README.md`<br>`deploy/addons/**/README.md`<br>`INSTALL.md` |
| **Python SDKs (`xmc`, `xdash`)** | `sdks/python/xmc/`<br>`sdks/python/xdash/`<br>`pyproject.toml` | `sdks/python/xmc/README.md`<br>`sdks/python/xdash/README.md`<br>`README.md`<br>`docs/authentication.md`<br>`docs/developer.md` |
| **Examples & Workloads** | `examples/*/`<br>`examples/**/launch.py`<br>`examples/**/worker.py`<br>`examples/**/worker.env`<br>`examples/**/Dockerfile` | `examples/**/README.md`<br>`README.md` |
| **Diagnostic, CI & Dev Tools** | `tools/*/`<br>`tools/*.py` | `tools/README.md`<br>`tools/**/README.md`<br>`test/**/README.md`<br>`.github/**/*.md`<br>`docs/ci_runner_infrastructure.md` |
| **Multi-Agent Guidelines & Rules** | `.agents/rules/`<br>`.agents/skills/` | `.agents/AGENTS.md`<br>`.agents/rules/**/*.md`<br>`.agents/skills/**/*.md`<br>`docs/agent_workflows.md` |

---

## 2. Identifier Sources of Truth Registry

Documentation frequently asserts facts regarding CLI flags, environment variables, default ports, Kubernetes resource kinds, and configuration parameters. These assertions MUST be tested against their code sources of truth, never against other documentation or human memory:

| Documented Identifier Type | Canonical Source of Truth in Code | Verification Method / Regex |
| :--- | :--- | :--- |
| **CLI Flags & Defaults** | `cmd/<service>/main.go`<br>`internal/configutil/` | Search flag definitions (`flag.StringVar`, `pflag`, `cobra.Command`). Compare flag name and default value. |
| **Environment Variables** | `pkg/**/`<br>`internal/**/`<br>`cmd/**/`<br>`sdks/python/**/` | Search `os.Getenv("VAR")`, `os.LookupEnv("VAR")` in Go, or `os.environ.get("VAR")` in Python. Verify exact spelling and fallback default. |
| **Network Ports & Protocols** | `deploy/helm/templates/`<br>`cmd/<service>/main.go`<br>`deploy/helm/values.yaml` | Compare documented port (e.g. `8123`, `9000`, `8080`, `50051`) against container port declarations and server listen addresses. |
| **Helm Chart Parameters** | `deploy/helm/values.yaml`<br>`deploy/profiles/**/values.yaml` | Verify that every parameter documented in the chart README's markdown table exists in `values.yaml` with the documented default. |
| **Cluster Toolkit Blueprint Vars** | `deploy/ctk_v2/xmanager-cloud.yaml`<br>`deploy/ctk_v2/xmc-deployment-template.yaml`<br>`deploy/ctk_v2/modules/**/variables.tf` | Verify `vars:` keys, default values, and module input requirements against blueprint schema. |
| **Protobuf RPCs & Field Names** | `api/protos/**/*.proto` | Check that RPC method names, package names, and field names in prose or JSON examples match the `.proto` definitions. |
| **Python SDK Methods & Signatures** | `sdks/python/xmc/`<br>`sdks/python/xdash/` | Verify documented method names, arguments, keyword parameters, and return types against public module exports (`__all__`). |
| **Container Images & Registries** | `deploy/helm/values.yaml`<br>`examples/**/Dockerfile`<br>`examples/**/build_and_push.sh` | Confirm that image repository paths (e.g., `us-docker.pkg.dev/...`) match active build manifests and Artifact Registry repositories. |

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
# 0. Verify Bi-Directional Topography Map Integrity (Map <-> git ls-files)
python3 tools/lint/check_docs_topography_map.py

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
