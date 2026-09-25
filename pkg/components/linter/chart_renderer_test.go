// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package linter_test

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"gopkg.in/yaml.v3"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/engine"

	"xmanager_cloud/pkg/components/linter"
)

func TestRenderChartTemplate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		chartYAML string
		values    string
		template  string
		wantSub   string
		wantErr   bool
	}{
		{
			name:      "renders default, quote, toString, trim, lower, upper and Chart metadata",
			chartYAML: "apiVersion: v2\nname: redpanda\nversion: 2.1.0\nappVersion: v24.1.1\n",
			values:    "enabled: false\nparameters:\n  kafkaPort: 9092\n  rawLabel: \"  MixedCase  \"\n",
			template: `name: {{ .Values.missingName | default "redpanda" | quote }}
version: {{ .Chart.Version | quote }}
port: {{ .Values.parameters.kafkaPort | toString | quote }}
trimmed: {{ .Values.parameters.rawLabel | trim | lower | quote }}
upper: {{ .Values.parameters.rawLabel | trim | upper | quote }}`,
			wantSub: `name: "redpanda"
version: "2.1.0"
port: "9092"
trimmed: "mixedcase"
upper: "MIXEDCASE"`,
		},
		{
			name:      "reports error on invalid Go template syntax",
			chartYAML: "apiVersion: v2\nname: redpanda\nversion: 2.1.0\n",
			values:    "enabled: false\n",
			template:  "name: {{ .Values.name | unknownFunc }}",
			wantErr:   true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeTestManifest(t, dir, "Chart.yaml", tc.chartYAML)
			writeTestManifest(t, dir, "values.yaml", tc.values)
			tmplDir := filepath.Join(dir, "templates")
			if err := os.MkdirAll(tmplDir, 0755); err != nil {
				t.Fatalf("failed to create templates dir: %v", err)
			}
			tmplPath := filepath.Join(tmplDir, "component.yaml")
			if err := os.WriteFile(tmplPath, []byte(tc.template), 0644); err != nil {
				t.Fatalf("failed to write template: %v", err)
			}

			got, err := linter.RenderChartTemplate(dir, tmplPath)
			if (err != nil) != tc.wantErr {
				t.Fatalf("RenderChartTemplate() error = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && !strings.Contains(string(got), tc.wantSub) {
				t.Errorf("RenderChartTemplate() got:\n%s\nwant substring:\n%s", string(got), tc.wantSub)
			}
		})
	}
}

func boolPtr(v bool) *bool { return &v }

func verifyBlueprintChart(t *testing.T, chartDir, crdPath string, want linter.ComponentManifest) {
	t.Helper()
	res, err := linter.NewLinter(nil).ValidateDirectory(chartDir)
	if err != nil {
		t.Fatalf("ValidateDirectory(%q) unexpected error: %v", chartDir, err)
	}
	if !res.Valid {
		t.Fatalf("ValidateDirectory(%q) failed with errors: %v", chartDir, res.Errors)
	}

	tmplPath := filepath.Join(chartDir, "templates", "component.yaml")
	rendered, err := linter.RenderChartTemplate(chartDir, tmplPath)
	if err != nil {
		t.Fatalf("RenderChartTemplate() failed: %v", err)
	}

	var got linter.ComponentManifest
	if err := yaml.Unmarshal(rendered, &got); err != nil {
		t.Fatalf("failed to unmarshal rendered Component manifest: %v", err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("rendered Component manifest mismatch (-want +got):\n%s", diff)
	}
	validateManifestAgainstCRD(t, crdPath, &got)
}

func stageChartFile(t *testing.T, srcPath, dstPath string) {
	t.Helper()
	data, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("failed to read %s: %v", srcPath, err)
	}
	if err := os.WriteFile(dstPath, data, 0o644); err != nil {
		t.Fatalf("failed to write %s: %v", dstPath, err)
	}
}

func mergeValuesMap(dst, src map[string]any) {
	for k, v := range src {
		srcMap, srcOk := v.(map[string]any)
		dstMap, dstOk := dst[k].(map[string]any)
		if srcOk && dstOk {
			mergeValuesMap(dstMap, srcMap)
			continue
		}
		dst[k] = v
	}
}

func stageMergedChartValues(t *testing.T, srcDir, chartDir, customValues string) {
	t.Helper()
	baseBytes, err := os.ReadFile(filepath.Join(srcDir, "values.yaml"))
	if err != nil {
		t.Fatalf("failed to read base values.yaml: %v", err)
	}
	baseMap := make(map[string]any)
	if err := yaml.Unmarshal(baseBytes, &baseMap); err != nil {
		t.Fatalf("failed to unmarshal base values.yaml: %v", err)
	}
	customMap := make(map[string]any)
	if err := yaml.Unmarshal([]byte(customValues), &customMap); err != nil {
		t.Fatalf("failed to unmarshal customValues: %v", err)
	}
	mergeValuesMap(baseMap, customMap)
	mergedBytes, err := yaml.Marshal(baseMap)
	if err != nil {
		t.Fatalf("failed to marshal merged values.yaml: %v", err)
	}
	writeTestManifest(t, chartDir, "values.yaml", string(mergedBytes))
}

func stageChartWithValues(t *testing.T, srcDir, customValues string) string {
	t.Helper()
	chartDir := t.TempDir()
	stageChartFile(t, filepath.Join(srcDir, "Chart.yaml"), filepath.Join(chartDir, "Chart.yaml"))
	err := filepath.WalkDir(filepath.Join(srcDir, "templates"), func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(filepath.Join(srcDir, "templates"), path)
		if err != nil {
			return err
		}
		dst := filepath.Join(chartDir, "templates", rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		stageChartFile(t, path, dst)
		return nil
	})
	if err != nil {
		t.Fatalf("failed to copy templates: %v", err)
	}
	stageMergedChartValues(t, srcDir, chartDir, customValues)
	return chartDir
}

func TestBlueprint_LinterAndCRDSchemaValidation(t *testing.T) {
	t.Parallel()
	crdPath := resolveRepoPath(t, filepath.Join("deploy", "helm", "templates", "components", "crd.yaml"))

	tests := []struct {
		name         string
		component    string
		customValues string
		want         linter.ComponentManifest
	}{
		{
			name:      "RedpandaDefaultBlueprint",
			component: "redpanda",
			want: linter.ComponentManifest{
				APIVersion: "xmc.dev/v1alpha1",
				Kind:       "Component",
				Metadata: linter.ObjectMetadata{
					Name:      "redpanda",
					Namespace: "xmc",
					Labels: map[string]string{
						"xmc.dev/profile":              "stable-2026a",
						"app.kubernetes.io/name":       "redpanda",
						"app.kubernetes.io/managed-by": "Helm",
					},
				},
				Spec: linter.ComponentManifestSpec{
					Enabled:            boolPtr(false),
					ComponentType:      "COMPONENT_TYPE_CUSTOM_3P_WRAPPER",
					ComponentClass:     "event-stream",
					Version:            "0.1.0",
					ChartVersion:       "0.1.0",
					DeletionProtection: true,
					DeletionPolicy:     "Delete",
					Parameters: map[string]string{
						"brokerEndpoints": "redpanda-0.redpanda.xmc.svc.cluster.local:9092",
						"kafkaPort":       "9092",
						"defaultTopic":    "xmc-metrics",
						"storageSize":     "10Gi",
					},
				},
			},
		},
		{
			name:      "ClickHouseDefaultBlueprint",
			component: "clickhouse",
			want: linter.ComponentManifest{
				APIVersion: "xmc.dev/v1alpha1",
				Kind:       "Component",
				Metadata: linter.ObjectMetadata{
					Name:      "clickhouse",
					Namespace: "xmc",
					Labels: map[string]string{
						"xmc.dev/profile":              "stable-2026a",
						"app.kubernetes.io/name":       "clickhouse",
						"app.kubernetes.io/managed-by": "Helm",
					},
				},
				Spec: linter.ComponentManifestSpec{
					Enabled:            boolPtr(false),
					ComponentType:      "COMPONENT_TYPE_CUSTOM_3P_WRAPPER",
					ComponentClass:     "analytical-db",
					Version:            "0.1.0",
					ChartVersion:       "0.1.0",
					DeletionProtection: true,
					DeletionPolicy:     "Delete",
					Parameters: map[string]string{
						"host":                "clickhouse.xmc-metrics.svc.cluster.local",
						"clusterEndpoints":    "clickhouse.xmc-metrics.svc.cluster.local:9000",
						"nativePort":          "9000",
						"httpPort":            "8123",
						"defaultDatabase":     "default",
						"user":                "default",
						"secretName":          "clickhouse-secret",
						"secretPasswordKey":   "clickhouse-password",
						"healthCheckEndpoint": "http://clickhouse.xmc-metrics.svc.cluster.local:8123/ping",
						"storageSize":         "10Gi",
					},
				},
			},
		},
		{
			name:      "ClickHouseCustomOverrides",
			component: "clickhouse",
			customValues: `enabled: true
namespace: xmc-metrics
deletionProtection: false
deletionPolicy: Orphan
parameters:
  host: "clickhouse-custom.xmc-metrics.svc.cluster.local"
  nativePort: 9440
  httpPort: 8443
  defaultDatabase: "analytics"
  user: "xmc_reader"
  secretName: "clickhouse-custom-secret"
  storageSize: "100Gi"
`,
			want: linter.ComponentManifest{
				APIVersion: "xmc.dev/v1alpha1",
				Kind:       "Component",
				Metadata: linter.ObjectMetadata{
					Name:      "clickhouse",
					Namespace: "xmc-metrics",
					Labels: map[string]string{
						"xmc.dev/profile":              "stable-2026a",
						"app.kubernetes.io/name":       "clickhouse",
						"app.kubernetes.io/managed-by": "Helm",
					},
				},
				Spec: linter.ComponentManifestSpec{
					Enabled:            boolPtr(true),
					ComponentType:      "COMPONENT_TYPE_CUSTOM_3P_WRAPPER",
					ComponentClass:     "analytical-db",
					Version:            "0.1.0",
					ChartVersion:       "0.1.0",
					DeletionProtection: false,
					DeletionPolicy:     "Orphan",
					Parameters: map[string]string{
						"host":                "clickhouse-custom.xmc-metrics.svc.cluster.local",
						"clusterEndpoints":    "clickhouse-custom.xmc-metrics.svc.cluster.local:9440",
						"nativePort":          "9440",
						"httpPort":            "8443",
						"defaultDatabase":     "analytics",
						"user":                "xmc_reader",
						"secretName":          "clickhouse-custom-secret",
						"secretPasswordKey":   "clickhouse-password",
						"healthCheckEndpoint": "http://clickhouse-custom.xmc-metrics.svc.cluster.local:8443/ping",
						"storageSize":         "100Gi",
					},
				},
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			chartDir := resolveRepoPath(t, filepath.Join("deploy", "profiles", "stable-2026a", "components", tc.component))
			if tc.customValues != "" {
				chartDir = stageChartWithValues(t, chartDir, tc.customValues)
			}
			verifyBlueprintChart(t, chartDir, crdPath, tc.want)
		})
	}
}

type crdSchemaDoc struct {
	Spec struct {
		Group string `yaml:"group"`
		Names struct {
			Kind string `yaml:"kind"`
		} `yaml:"names"`
		Versions []struct {
			Name   string `yaml:"name"`
			Schema struct {
				OpenAPIV3Schema struct {
					Properties struct {
						Spec struct {
							Properties struct {
								WrapperRef struct {
									Properties struct {
										ObservedGenerationPath struct {
											Pattern string `yaml:"pattern"`
										} `yaml:"observedGenerationPath"`
										ReadyConditionPath struct {
											Pattern string `yaml:"pattern"`
										} `yaml:"readyConditionPath"`
									} `yaml:"properties"`
								} `yaml:"wrapperRef"`
							} `yaml:"properties"`
						} `yaml:"spec"`
					} `yaml:"properties"`
				} `yaml:"openAPIV3Schema"`
			} `yaml:"schema"`
		} `yaml:"versions"`
	} `yaml:"spec"`
}

func validateManifestAgainstCRD(t *testing.T, crdPath string, comp *linter.ComponentManifest) {
	t.Helper()
	rawCRD, err := os.ReadFile(crdPath)
	if err != nil {
		t.Fatalf("failed to read CRD %s: %v", crdPath, err)
	}
	re := regexp.MustCompile(`(?s)\{\{.*?\}\}`)
	cleaned := re.ReplaceAllString(string(rawCRD), "")

	var crd crdSchemaDoc
	if err := yaml.Unmarshal([]byte(cleaned), &crd); err != nil {
		t.Fatalf("failed to unmarshal CRD YAML: %v", err)
	}

	if len(crd.Spec.Versions) == 0 {
		t.Fatalf("CRD has no versions defined")
	}
	version := crd.Spec.Versions[0]
	wantAPIVersion := crd.Spec.Group + "/" + version.Name
	if comp.APIVersion != wantAPIVersion || comp.Kind != crd.Spec.Names.Kind {
		t.Errorf("manifest GVK (%s, %s) does not match CRD (%s, %s)", comp.APIVersion, comp.Kind, wantAPIVersion, crd.Spec.Names.Kind)
	}

	if comp.Spec.WrapperRef != nil {
		wrapperProps := version.Schema.OpenAPIV3Schema.Properties.Spec.Properties.WrapperRef.Properties
		obsRe := regexp.MustCompile(wrapperProps.ObservedGenerationPath.Pattern)
		if !obsRe.MatchString(comp.Spec.WrapperRef.ObservedGenerationPath) {
			t.Errorf("observedGenerationPath %q does not match CRD pattern %q", comp.Spec.WrapperRef.ObservedGenerationPath, obsRe.String())
		}
		readyRe := regexp.MustCompile(wrapperProps.ReadyConditionPath.Pattern)
		if !readyRe.MatchString(comp.Spec.WrapperRef.ReadyConditionPath) {
			t.Errorf("readyConditionPath %q does not match CRD pattern %q", comp.Spec.WrapperRef.ReadyConditionPath, readyRe.String())
		}
	}
}

func resolveRepoPath(t *testing.T, relPath string) string {
	t.Helper()
	candidates := []string{
		relPath,
		filepath.Join("..", "..", "..", relPath),
	}
	if srcDir := os.Getenv("TEST_SRCDIR"); srcDir != "" {
		candidates = append(candidates,
			filepath.Join(srcDir, "_main", relPath),
			filepath.Join(srcDir, "xmanager_cloud", relPath),
		)
	}
	for _, cand := range candidates {
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
	}
	t.Fatalf("unable to resolve repository path %q from candidates %v", relPath, candidates)
	return ""
}

func TestChartTemplateFuncsAndHelpers(t *testing.T) {
	t.Parallel()

	chartDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(chartDir, "templates"), 0o755); err != nil {
		t.Fatalf("failed to create templates dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte("apiVersion: v2\nname: release-name\nversion: 1.0.0\n"), 0o644); err != nil {
		t.Fatalf("failed to write Chart.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartDir, "values.yaml"), []byte("emptyList: []\nemptyMap: {}\nprefixed: \"<nil>value\"\nmeta:\n  tier: core\n"), 0o644); err != nil {
		t.Fatalf("failed to write values.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartDir, "templates", "_helpers.yaml"), []byte("{{- define \"ignored\" -}}helper{{- end -}}"), 0o644); err != nil {
		t.Fatalf("failed to write _helpers.yaml: %v", err)
	}
	tmplContent := `apiVersion: xmc.dev/v1alpha1
kind: Component
metadata:
  name: {{ .Release.Name | quote }}
  namespace: "xmc"
spec:
  componentType: COMPONENT_TYPE_CUSTOM_1P_COMPONENT
  chartVersion: {{ .Values.emptyList | default "1.0.0" | quote }}
  parameters:
    prefixed: {{ .Values.prefixed | toString | quote }}
    fallbackMap: {{ .Values.emptyMap | default "fallback" | quote }}
    helperVal: {{ include "ignored" . | quote }}
    yamlBlock: |{{ .Values.meta | toYaml | nindent 6 }}
`
	tmplPath := filepath.Join(chartDir, "templates", "values-component.yaml")
	if err := os.WriteFile(tmplPath, []byte(tmplContent), 0o644); err != nil {
		t.Fatalf("failed to write values-component.yaml: %v", err)
	}

	rendered, err := linter.RenderChartTemplate(chartDir, tmplPath)
	if err != nil {
		t.Fatalf("RenderChartTemplate(%s) returned error: %v", chartDir, err)
	}
	if !strings.Contains(string(rendered), `helperVal: "helper"`) {
		t.Errorf("expected include output helperVal: \"helper\", got:\n%s", string(rendered))
	}

	res, err := linter.NewLinter(nil).ValidateDirectory(chartDir)
	if err != nil {
		t.Fatalf("ValidateDirectory(%s) returned error: %v", chartDir, err)
	}
	if !res.Valid {
		t.Errorf("expected valid chart render and lint, got errors: %+v", res.Errors)
	}
}

func TestIngestionGatewaySubChartRendering(t *testing.T) {
	t.Parallel()

	igDir := resolveRepoPath(t, filepath.Join("deploy", "profiles", "stable-2026a", "components", "ingestion-gateway"))

	res, err := linter.NewLinter(nil).ValidateDirectory(igDir)
	if err != nil {
		t.Fatalf("ValidateDirectory(%q) unexpected error: %v", igDir, err)
	}
	if !res.Valid {
		t.Fatalf("ValidateDirectory(%q) failed with errors: %v", igDir, res.Errors)
	}

	tmplPath := filepath.Join(igDir, "templates", "component.yaml")
	rendered, err := linter.RenderChartTemplate(igDir, tmplPath)
	if err != nil {
		t.Fatalf("RenderChartTemplate() failed: %v", err)
	}

	var got linter.ComponentManifest
	if err := yaml.Unmarshal(rendered, &got); err != nil {
		t.Fatalf("failed to unmarshal rendered Ingestion Gateway Component manifest: %v", err)
	}

	enabled := false
	want := linter.ComponentManifest{
		APIVersion: "xmc.dev/v1alpha1",
		Kind:       "Component",
		Metadata: linter.ObjectMetadata{
			Name:      "ingestion-gateway",
			Namespace: "xmc",
			Labels: map[string]string{
				"xmc.dev/profile":              "stable-2026a",
				"xmc.dev/component-type":       "custom-1p-component",
				"app.kubernetes.io/name":       "ingestion-gateway",
				"app.kubernetes.io/managed-by": "Helm",
			},
		},
		Spec: linter.ComponentManifestSpec{
			Enabled:            &enabled,
			ComponentType:      "COMPONENT_TYPE_CUSTOM_1P_COMPONENT",
			ComponentClass:     "metrics-ingestion",
			Version:            "0.1.0",
			ChartVersion:       "0.1.0",
			DeletionProtection: true,
			DeletionPolicy:     "Delete",
			Dependencies: []linter.ComponentDependencyRef{
				{Name: "redpanda"},
				{Name: "clickhouse"},
			},
			Parameters: map[string]string{
				"port":            "8080",
				"redpandaBrokers": "redpanda-0.redpanda.xmc.svc.cluster.local:9092",
				"topicName":       "xmc-metrics",
				"minPartitions":   "128",
				"minReplicas":     "3",
			},
		},
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("rendered Ingestion Gateway Component manifest mismatch (-want +got):\n%s", diff)
	}

	verifySubChartOverridesAndGuards(t, igDir)
}

func verifySubChartOverridesAndGuards(t *testing.T, igDir string) {
	t.Helper()

	overrides := map[string]any{
		"enabled":        true,
		"deployWorkload": true,
		"global":         map[string]any{"imageTag": "v1.2.3"},
		"parameters":     map[string]any{"port": 9090},
	}
	renderedMap, err := renderChartWithValues(igDir, "ingestion-gateway", overrides)
	if err != nil {
		t.Fatalf("renderChartWithValues(ingestion-gateway) failed: %v", err)
	}
	compYAML := renderedMap["ingestion-gateway/templates/component.yaml"]
	if !strings.Contains(compYAML, `imageTag: "v1.2.3"`) || !strings.Contains(compYAML, `port: "9090"`) {
		t.Errorf("expected imageTag v1.2.3 and port 9090 in component.yaml, got:\n%s", compYAML)
	}
	depYAML := renderedMap["ingestion-gateway/templates/deployment.yaml"]
	if !strings.Contains(depYAML, `image: "us-docker.pkg.dev/xmc-prod/xmc/ingestion-gateway:v1.2.3"`) || !strings.Contains(depYAML, `"-port=9090"`) {
		t.Errorf("expected overridden image tag and port in deployment.yaml, got:\n%s", depYAML)
	}
	verifyBridgeReleaseGuards(t, igDir)
}

func verifyBridgeReleaseGuards(t *testing.T, igDir string) {
	t.Helper()

	bridgeMap, err := renderChartWithValues(igDir, "xmc-cmp-ingestion-gateway", map[string]any{
		"global":                     map[string]any{"imageTag": "v1.2.3"},
		"ingestionGatewayDeployment": map[string]any{"enabled": true, "image": "custom.registry.io/ig:custom-tag"},
	})
	if err != nil {
		t.Fatalf("renderChartWithValues(xmc-cmp-ingestion-gateway) failed: %v", err)
	}
	if strings.Contains(bridgeMap["ingestion-gateway/templates/component.yaml"], "kind: Component") {
		t.Errorf("expected component.yaml to be suppressed for xmc-cmp- release")
	}
	bridgeDep := bridgeMap["ingestion-gateway/templates/deployment.yaml"]
	for _, want := range []string{"kind: Deployment", "kind: Service", "kind: HorizontalPodAutoscaler", "kind: Role", "kind: RoleBinding", "name: ingestion-gateway-components-reader"} {
		if !strings.Contains(bridgeDep, want) {
			t.Errorf("expected %q in deployment.yaml for xmc-cmp- release (component self-provisions workload and reader RBAC)", want)
		}
	}
	if !strings.Contains(bridgeDep, `image: "custom.registry.io/ig:custom-tag"`) {
		t.Errorf("expected explicit parameters[image] to take precedence, got:\n%s", bridgeDep)
	}
	assertWellFormedManifests(t, bridgeDep)
}

// assertWellFormedManifests fails if any rendered YAML document lacks apiVersion or kind, which
// Kubernetes rejects at install time (e.g. a template chomping the newline before apiVersion).
func assertWellFormedManifests(t *testing.T, manifest string) {
	t.Helper()
	dec := yaml.NewDecoder(strings.NewReader(manifest))
	for i := 0; ; i++ {
		var doc map[string]any
		err := dec.Decode(&doc)
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatalf("yaml document %d failed to decode: %v", i, err)
		}
		if doc == nil {
			continue
		}
		if doc["apiVersion"] == nil || doc["kind"] == nil {
			t.Errorf("yaml document %d missing apiVersion or kind: apiVersion=%v kind=%v", i, doc["apiVersion"], doc["kind"])
		}
	}
}

func TestStable2026aProfileDAGValidation(t *testing.T) {
	t.Parallel()

	profileDir := resolveRepoPath(t, filepath.Join("deploy", "profiles", "stable-2026a"))
	rulesPath := filepath.Join(profileDir, "rules.yaml")

	rules, err := linter.LoadRules(rulesPath)
	if err != nil {
		t.Fatalf("LoadRules(%q) failed: %v", rulesPath, err)
	}

	res, err := linter.NewLinter(rules).ValidateDirectory(profileDir)
	if err != nil {
		t.Fatalf("ValidateDirectory(%q) unexpected error: %v", profileDir, err)
	}
	if !res.Valid {
		t.Fatalf("ValidateDirectory(%q) failed with errors: %v", profileDir, res.Errors)
	}

	igRendered, err := renderChartWithValues(
		filepath.Join(profileDir, "components", "ingestion-gateway"),
		"ingestion-gateway",
		map[string]any{"enabled": true},
	)
	if err != nil {
		t.Fatalf("failed to render enabled ingestion-gateway: %v", err)
	}

	tests := []struct {
		name       string
		redpanda   bool
		clickhouse bool
		wantValid  bool
	}{
		{name: "both dependencies enabled", redpanda: true, clickhouse: true, wantValid: true},
		{name: "redpanda disabled", redpanda: false, clickhouse: true},
		{name: "clickhouse disabled", redpanda: true, clickhouse: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeTestManifest(t, dir, "ingestion-gateway.yaml", igRendered["ingestion-gateway/templates/component.yaml"])
			writeTestManifest(t, dir, "redpanda.yaml", renderProfileComponent(t, profileDir, "redpanda", tt.redpanda))
			writeTestManifest(t, dir, "clickhouse.yaml", renderProfileComponent(t, profileDir, "clickhouse", tt.clickhouse))
			got, err := linter.NewLinter(rules).ValidateDirectory(dir)
			if err != nil {
				t.Fatalf("ValidateDirectory() error: %v", err)
			}
			if got.Valid != tt.wantValid {
				t.Fatalf("ValidateDirectory() valid = %v, want %v (errors: %+v)", got.Valid, tt.wantValid, got.Errors)
			}
			if !tt.wantValid {
				assertDiagnosticRule(t, got, "DISABLED_DEPENDENCY")
			}
		})
	}
}

// renderProfileComponent renders a stable-2026a component's Component CR with the given enabled flag.
func renderProfileComponent(t *testing.T, profileDir, name string, enabled bool) string {
	t.Helper()
	rendered, err := renderChartWithValues(filepath.Join(profileDir, "components", name), name, map[string]any{"enabled": enabled})
	if err != nil {
		t.Fatalf("failed to render %s (enabled=%v): %v", name, enabled, err)
	}
	return rendered[name+"/templates/component.yaml"]
}

func renderChartWithValues(chartRoot, releaseName string, overrides map[string]any) (map[string]string, error) {
	ch, err := loader.LoadDir(chartRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to load Helm chart in %s: %w", chartRoot, err)
	}
	if releaseName == "" {
		releaseName = ch.Name()
	}
	releaseOpts := chartutil.ReleaseOptions{
		Name:      releaseName,
		Namespace: "xmc",
		Revision:  1,
		IsInstall: true,
	}
	mergedVals := chartutil.CoalesceTables(overrides, ch.Values)
	renderValues, err := chartutil.ToRenderValues(ch, mergedVals, releaseOpts, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build Helm render values in %s: %w", chartRoot, err)
	}
	rendered, err := engine.Render(ch, renderValues)
	if err != nil {
		return nil, fmt.Errorf("failed to render Helm chart in %s: %w", chartRoot, err)
	}
	return rendered, nil
}
