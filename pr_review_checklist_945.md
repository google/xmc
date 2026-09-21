# PR Review Checklist: google/xmc#945

Total Unresolved Comments: 1 (High/Critical: 0)

## Unresolved Review Comments

- [ ] **[pkg/components/linter/chart_renderer.go:37](https://github.com/google/xmc/pull/945#discussion_r4063476973)** [MEDIUM] (@gemini-code-assist) (Comment ID 4063476973)
  > ![medium](https://www.gstatic.com/codereviewagent/medium-priority.svg)
  > 
  > The current implementation of `isChartOrRulesConfigFile` silently skips any YAML file starting with `values` (e.g., `values-configmap.yaml` or `values-store.yaml`) regardless of whether it is a legitimate Kubernetes manifest template inside a Helm chart's `templates/` directory or a standalone manifest in a flat directory. 
  > 
  > To prevent silently skipping valid manifests, we should only skip `values*.yaml` files if they are located at the root of a Helm chart. Legitimate templates under `templates/` should always be linted unless they are helper files starting with `_`.
  > 
  > ```go
  > func isChartOrRulesConfigFile(path string) bool {
  > 	base := strings.ToLower(filepath.Base(path))
  > 	if base == "rules.yaml" || base == "rules.yml" {
  > 		return true
  > 	}
  > 	if chartRoot := findChartRoot(path); chartRoot != "" {
  > 		rel, err := filepath.Rel(chartRoot, path)
  > 		if err != nil {
  > 			return false
  > 		}
  > 		components := strings.Split(filepath.ToSlash(rel), "/")
  > 		if len(components) == 1 {
  > 			return true
  > 		}
  > 		if len(components) > 1 && components[0] == "templates" {
  > 			return strings.HasPrefix(components[len(components)-1], "_")
  > 		}
  > 		return true
  > 	}
  > 	return false
  > }
  > ```
