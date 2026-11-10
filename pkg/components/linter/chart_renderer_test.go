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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"

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

// profileCRAnnotations returns the annotations every stable-2026a Component CR renders with.
func profileCRAnnotations() map[string]string {
	return map[string]string{"argocd.argoproj.io/sync-options": "SkipDryRunOnMissingResource=true"}
}

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
	validateManifestAgainstCRD(t, crdPath, rendered, &got)
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
					Annotations: profileCRAnnotations(),
				},
				Spec: linter.ComponentManifestSpec{
					Enabled:            boolPtr(false),
					ComponentType:      "COMPONENT_TYPE_CUSTOM_3P_WRAPPER",
					ComponentClass:     "event-stream",
					Version:            "0.1.0",
					ChartVersion:       "0.1.0",
					Stability:          "STABLE",
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
					Annotations: profileCRAnnotations(),
				},
				Spec: linter.ComponentManifestSpec{
					Enabled:            boolPtr(false),
					ComponentType:      "COMPONENT_TYPE_CUSTOM_3P_WRAPPER",
					ComponentClass:     "analytical-db",
					Version:            "0.1.0",
					ChartVersion:       "0.1.0",
					Stability:          "STABLE",
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
					Annotations: profileCRAnnotations(),
				},
				Spec: linter.ComponentManifestSpec{
					Enabled:            boolPtr(true),
					ComponentType:      "COMPONENT_TYPE_CUSTOM_3P_WRAPPER",
					ComponentClass:     "analytical-db",
					Version:            "0.1.0",
					ChartVersion:       "0.1.0",
					Stability:          "STABLE",
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
		{
			name:      "VizierDefaultBlueprint",
			component: "vizier",
			want: linter.ComponentManifest{
				APIVersion: "xmc.dev/v1alpha1",
				Kind:       "Component",
				Metadata: linter.ObjectMetadata{
					Name:      "vizier",
					Namespace: "xmc",
					Labels: map[string]string{
						"xmc.dev/profile":              "stable-2026a",
						"app.kubernetes.io/name":       "vizier",
						"app.kubernetes.io/managed-by": "Helm",
					},
					Annotations: profileCRAnnotations(),
				},
				Spec: linter.ComponentManifestSpec{
					Enabled:            boolPtr(false),
					ComponentType:      "COMPONENT_TYPE_CUSTOM_3P_WRAPPER",
					ComponentClass:     "optimization-engine",
					Version:            "0.1.0",
					ChartVersion:       "0.1.0",
					Stability:          "EXPERIMENTAL",
					DeletionProtection: true,
					DeletionPolicy:     "Delete",
					Parameters: map[string]string{
						"endpoint": "vizier-service.xmc.svc.cluster.local:7077",
					},
				},
			},
		},
		{
			name:      "HPODefaultBlueprint",
			component: "hpo",
			want: linter.ComponentManifest{
				APIVersion: "xmc.dev/v1alpha1",
				Kind:       "Component",
				Metadata: linter.ObjectMetadata{
					Name:      "hpo",
					Namespace: "xmc",
					Labels: map[string]string{
						"xmc.dev/profile":              "stable-2026a",
						"xmc.dev/component-type":       "custom-1p-component",
						"app.kubernetes.io/name":       "hpo",
						"app.kubernetes.io/managed-by": "Helm",
					},
					Annotations: profileCRAnnotations(),
				},
				Spec: linter.ComponentManifestSpec{
					Enabled:            boolPtr(false),
					ComponentType:      "COMPONENT_TYPE_CUSTOM_1P_COMPONENT",
					ComponentClass:     "hyperparameter-optimization",
					Version:            "0.1.0",
					ChartVersion:       "0.1.0",
					Stability:          "EXPERIMENTAL",
					DeletionProtection: true,
					DeletionPolicy:     "Delete",
					Dependencies: []linter.ComponentDependencyRef{
						{Name: "vizier"},
						{Name: "clickhouse"},
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
							Required   []string `yaml:"required"`
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

func validateManifestAgainstCRD(t *testing.T, crdPath string, rendered []byte, comp *linter.ComponentManifest) {
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

	var rawDoc map[string]any
	if err := yaml.Unmarshal(rendered, &rawDoc); err != nil {
		t.Fatalf("failed to unmarshal rendered manifest map: %v", err)
	}
	rawSpec, _ := rawDoc["spec"].(map[string]any)
	for _, reqField := range version.Schema.OpenAPIV3Schema.Properties.Spec.Required {
		if val, ok := rawSpec[reqField]; !ok || val == nil || fmt.Sprint(val) == "" {
			t.Errorf("rendered manifest %q missing CRD required spec.%s field", comp.Metadata.Name, reqField)
		}
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
  stability: "STABLE"
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
	helmDir := resolveRepoPath(t, filepath.Join("deploy", "helm"))

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
			Annotations: profileCRAnnotations(),
		},
		Spec: linter.ComponentManifestSpec{
			Enabled:            &enabled,
			ComponentType:      "COMPONENT_TYPE_CUSTOM_1P_COMPONENT",
			ComponentClass:     "metrics-ingestion",
			Version:            "0.1.0",
			ChartVersion:       "0.1.0",
			Stability:          "STABLE",
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
				"minReplicas":     "1",
			},
		},
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("rendered Ingestion Gateway Component manifest mismatch (-want +got):\n%s", diff)
	}
	crdPath := resolveRepoPath(t, filepath.Join("deploy", "helm", "templates", "components", "crd.yaml"))
	validateManifestAgainstCRD(t, crdPath, rendered, &got)

	verifySubChartOverridesAndGuards(t, igDir)
	verifyUmbrellaChartIntegration(t, helmDir)
	verifyGatewayNetworkPolicy(t, igDir)
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
	for _, want := range []string{"kind: Deployment", "kind: Service", "kind: HorizontalPodAutoscaler", "kind: Role", "kind: RoleBinding", "name: ingestion-gateway-components-reader", "kind: NetworkPolicy", "name: ingestion-gateway-network-policy"} {
		if !strings.Contains(bridgeDep, want) {
			t.Errorf("expected %q in deployment.yaml for xmc-cmp- release (component self-provisions workload, reader RBAC and NetworkPolicy)", want)
		}
	}
	if !strings.Contains(bridgeDep, `image: "custom.registry.io/ig:custom-tag"`) {
		t.Errorf("expected explicit parameters[image] to take precedence, got:\n%s", bridgeDep)
	}
	assertWellFormedManifests(t, bridgeDep)

	crOnlyMap, err := renderChartWithValues(igDir, "ingestion-gateway", map[string]any{"enabled": true})
	if err != nil {
		t.Fatalf("renderChartWithValues(ingestion-gateway, CR only) failed: %v", err)
	}
	if crOnly := crOnlyMap["ingestion-gateway/templates/deployment.yaml"]; strings.Contains(crOnly, "kind: ") {
		t.Errorf("expected no workload or NetworkPolicy when only the Component CR is declared, got:\n%s", crOnly)
	}
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

// verifyUmbrellaChartIntegration checks the umbrella chart renders all xmc-bridge resources and
// kept CRD/VAP objects by default, ships no legacy gateway or umbrella NetworkPolicy templates,
// and fails closed when legacy flags (componentsDeployment.enabled=false or
// ingestionGatewayDeployment.enabled=true) are passed.
func verifyUmbrellaChartIntegration(t *testing.T, helmDir string) {
	t.Helper()

	rendered, err := renderChartWithValues(helmDir, "xmanager-cloud", map[string]any{})
	if err != nil {
		t.Fatalf("renderChartWithValues(umbrella, default) failed: %v", err)
	}
	for _, f := range []string{
		"components/deployment.yaml",
		"components/configmap.yaml",
		"components/rbac.yaml",
		"components/serviceaccount.yaml",
	} {
		dec := yaml.NewDecoder(strings.NewReader(rendered["xmanager-cloud/templates/"+f]))
		docCount := 0
		for {
			var doc map[string]any
			err := dec.Decode(&doc)
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("umbrella %s YAML decode failed: %v", f, err)
			}
			if len(doc) == 0 {
				continue
			}
			docCount++
			if apiVer, _ := doc["apiVersion"].(string); apiVer == "" {
				t.Errorf("umbrella %s doc[%d] missing apiVersion: %v", f, docCount-1, doc)
			}
			if kind, _ := doc["kind"].(string); kind == "" {
				t.Errorf("umbrella %s doc[%d] missing kind: %v", f, docCount-1, doc)
			}
		}
		if docCount == 0 {
			t.Errorf("umbrella %s did not render any Kubernetes object by default", f)
		}
	}
	for _, legacy := range []string{
		"ingestion-gateway/deployment.yaml",
		"ingestion-gateway/service.yaml",
		"ingestion-gateway/hpa.yaml",
		"network-policies.yaml",
	} {
		if _, exists := rendered["xmanager-cloud/templates/"+legacy]; exists {
			t.Errorf("legacy umbrella template %s still present in rendered map", legacy)
		}
	}
	verifyUmbrellaKeptResources(t, "default", rendered)

	failClosedCases := []struct {
		name    string
		values  map[string]any
		wantErr string
	}{
		{
			name:    "componentsDeployment.enabled=false rejected",
			values:  map[string]any{"componentsDeployment": map[string]any{"enabled": false}},
			wantErr: "componentsDeployment.enabled=false",
		},
		{
			name:    "componentsDeployment.enabled string false rejected",
			values:  map[string]any{"componentsDeployment": map[string]any{"enabled": "false"}},
			wantErr: "componentsDeployment.enabled=false",
		},
		{
			name:    "componentsDeployment scalar false rejected",
			values:  map[string]any{"componentsDeployment": false},
			wantErr: "componentsDeployment must be a map",
		},
		{
			name:    "ingestionGatewayDeployment.enabled=true rejected",
			values:  map[string]any{"ingestionGatewayDeployment": map[string]any{"enabled": true}},
			wantErr: "ingestionGatewayDeployment.enabled=true",
		},
		{
			name:    "ingestionGatewayDeployment.enabled string true rejected",
			values:  map[string]any{"ingestionGatewayDeployment": map[string]any{"enabled": "true"}},
			wantErr: "ingestionGatewayDeployment.enabled=true",
		},
	}
	for _, tc := range failClosedCases {
		if _, err := renderChartWithValues(helmDir, "xmanager-cloud", tc.values); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: renderChartWithValues error = %v, want substring %q", tc.name, err, tc.wantErr)
		}
	}
	if _, err := renderChartWithValues(helmDir, "xmanager-cloud", map[string]any{
		"componentsDeployment":       map[string]any{"enabled": "true"},
		"ingestionGatewayDeployment": map[string]any{"enabled": "false"},
	}); err != nil {
		t.Errorf("renderChartWithValues with deprecated no-op string flags failed: %v", err)
	}
	verifyUmbrellaLegacyGatewayLookupGuard(t, helmDir, rendered["xmanager-cloud/templates/components/deployment.yaml"])
}

// verifyUmbrellaKeptResources asserts the full Helm engine renders the Component CRD and the
// deletion admission policy with their keep policy.
func verifyUmbrellaKeptResources(t *testing.T, name string, rendered map[string]string) {
	t.Helper()
	wantByFile := map[string][]string{
		"components/crd.yaml":          {"kind: CustomResourceDefinition"},
		"components/deletion-vap.yaml": {"kind: ValidatingAdmissionPolicy\n", "kind: ValidatingAdmissionPolicyBinding"},
	}
	for f, kinds := range wantByFile {
		manifest := rendered["xmanager-cloud/templates/"+f]
		for _, want := range append(kinds, "helm.sh/resource-policy: keep") {
			if !strings.Contains(manifest, want) {
				t.Errorf("%s: umbrella %s missing %q", name, f, want)
			}
		}
	}
}

// verifyGatewayNetworkPolicy checks the xmc-cmp-ingestion-gateway release ships the expected
// pod-level NetworkPolicy ingress and egress rules.
func verifyGatewayNetworkPolicy(t *testing.T, igDir string) {
	t.Helper()

	component, err := renderChartWithValues(igDir, "xmc-cmp-ingestion-gateway", map[string]any{
		"ingestionGatewayDeployment": map[string]any{"enabled": true},
	})
	if err != nil {
		t.Fatalf("renderChartWithValues(xmc-cmp-ingestion-gateway) failed: %v", err)
	}
	const wantNetworkPolicyYAML = `
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
spec:
  podSelector:
    matchLabels:
      app: ingestion-gateway
  policyTypes:
    - Ingress
    - Egress
  ingress:
    - from:
        - podSelector:
            matchLabels:
              xmc.google.com/metrics-client: "true"
      ports:
        - protocol: TCP
          port: 8080
  egress:
    - ports:
        - protocol: UDP
          port: 53
        - protocol: TCP
          port: 53
    - ports:
        - protocol: TCP
          port: 443
    - ports:
        - protocol: TCP
          port: 9092
        - protocol: TCP
          port: 9094
        - protocol: TCP
          port: 9644
`
	want := manifestSpecByKind(t, wantNetworkPolicyYAML, "NetworkPolicy")
	got := manifestSpecByKind(t, component["ingestion-gateway/templates/deployment.yaml"], "NetworkPolicy")
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("xmc-cmp-ingestion-gateway NetworkPolicy spec mismatch (-want +got):\n%s", diff)
	}
}

// manifestSpecByKind returns the spec of the first document of the given kind in manifest.
func manifestSpecByKind(t *testing.T, manifest, kind string) map[string]any {
	t.Helper()
	dec := yaml.NewDecoder(strings.NewReader(manifest))
	for {
		var doc map[string]any
		err := dec.Decode(&doc)
		if err == io.EOF {
			t.Fatalf("no %s document found in manifest:\n%s", kind, manifest)
		}
		if err != nil {
			t.Fatalf("yaml decode failed: %v", err)
		}
		if doc["kind"] == kind {
			spec, _ := doc["spec"].(map[string]any)
			return spec
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

	tests := []struct {
		name      string
		overrides map[string]any
		wantValid bool
		wantRules []string
	}{
		{
			name: "both dependencies enabled",
			overrides: map[string]any{
				"ingestion-gateway": map[string]any{"enabled": true},
				"redpanda":          map[string]any{"enabled": true},
				"clickhouse":        map[string]any{"enabled": true},
			},
			wantValid: true,
		},
		{
			name: "redpanda disabled",
			overrides: map[string]any{
				"ingestion-gateway": map[string]any{"enabled": true},
				"redpanda":          map[string]any{"enabled": false},
				"clickhouse":        map[string]any{"enabled": true},
			},
			wantRules: []string{"DISABLED_DEPENDENCY", "REQUIRED_COMPONENT_MISSING"},
		},
		{
			name: "clickhouse disabled",
			overrides: map[string]any{
				"ingestion-gateway": map[string]any{"enabled": true},
				"redpanda":          map[string]any{"enabled": true},
				"clickhouse":        map[string]any{"enabled": false},
			},
			wantRules: []string{"DISABLED_DEPENDENCY", "REQUIRED_COMPONENT_MISSING"},
		},
		{
			name:      "default profile renders valid with disabled experimental components",
			overrides: map[string]any{},
			wantValid: true,
		},
		{
			name: "vizier enabled without acknowledgeExperimental fails",
			overrides: map[string]any{
				"vizier": map[string]any{"enabled": true},
			},
			wantRules: []string{"EXPERIMENTAL_NOT_ACKNOWLEDGED"},
		},
		{
			name: "hpo enabled without acknowledgeExperimental fails",
			overrides: map[string]any{
				"vizier":     map[string]any{"enabled": true, "acknowledgeExperimental": true},
				"clickhouse": map[string]any{"enabled": true},
				"hpo":        map[string]any{"enabled": true},
			},
			wantRules: []string{"EXPERIMENTAL_NOT_ACKNOWLEDGED"},
		},
		{
			name: "hpo enabled with disabled vizier fails DAG and compatibility matrix",
			overrides: map[string]any{
				"vizier":     map[string]any{"enabled": false},
				"clickhouse": map[string]any{"enabled": true},
				"hpo":        map[string]any{"enabled": true, "acknowledgeExperimental": true},
			},
			wantRules: []string{"DISABLED_DEPENDENCY", "REQUIRED_COMPONENT_MISSING"},
		},
		{
			name: "vizier clickhouse and hpo enabled with acknowledgeExperimental passes without stability inversion",
			overrides: map[string]any{
				"vizier":     map[string]any{"enabled": true, "acknowledgeExperimental": true},
				"clickhouse": map[string]any{"enabled": true},
				"hpo":        map[string]any{"enabled": true, "acknowledgeExperimental": true},
			},
			wantValid: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeTestManifest(t, dir, "rendered.yaml", renderStable2026aProfile(t, profileDir, tt.overrides))
			got, err := linter.NewLinter(rules).ValidateDirectory(dir)
			if err != nil {
				t.Fatalf("ValidateDirectory() error: %v", err)
			}
			if got.Valid != tt.wantValid {
				t.Fatalf("ValidateDirectory() valid = %v, want %v (errors: %+v)", got.Valid, tt.wantValid, got.Errors)
			}
			if !tt.wantValid {
				assertDiagnosticRules(t, got, tt.wantRules)
			}
		})
	}
}

// renderStable2026aProfile renders the stable-2026a profile meta-chart with the given
// components.<name> overrides, as an admin would with helm template -f values.yaml.
func renderStable2026aProfile(t *testing.T, profileDir string, components map[string]any) string {
	t.Helper()
	rendered, err := renderChartWithValues(profileDir, "xmc-profile", map[string]any{"components": components})
	if err != nil {
		t.Fatalf("failed to render stable-2026a profile with components %v: %v", components, err)
	}
	return rendered["xmc-profile-stable-2026a/templates/components.yaml"]
}

func renderChartWithValues(chartRoot, releaseName string, overrides map[string]any) (map[string]string, error) {
	return renderChartWithNamespace(chartRoot, releaseName, "xmc", overrides)
}

func renderChartWithNamespace(chartRoot, releaseName, namespace string, overrides map[string]any) (map[string]string, error) {
	return renderChartWithClient(chartRoot, releaseName, namespace, overrides, nil)
}

func renderChartWithClient(chartRoot, releaseName, namespace string, overrides map[string]any, provider engine.ClientProvider) (map[string]string, error) {
	ch, err := loader.LoadDir(chartRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to load Helm chart in %s: %w", chartRoot, err)
	}
	if releaseName == "" {
		releaseName = ch.Name()
	}
	releaseOpts := chartutil.ReleaseOptions{Name: releaseName, Namespace: namespace, Revision: 1, IsInstall: true}
	renderValues, err := chartutil.ToRenderValues(ch, chartutil.CoalesceTables(overrides, ch.Values), releaseOpts, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build Helm render values in %s: %w", chartRoot, err)
	}
	if provider != nil {
		return engine.RenderWithClientProvider(ch, renderValues, provider)
	}
	return engine.Render(ch, renderValues)
}

type fakeLookupProvider struct{ client dynamic.Interface }

func (p fakeLookupProvider) GetClientFor(apiVersion, kind string) (dynamic.NamespaceableResourceInterface, bool, error) {
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return nil, false, err
	}
	return p.client.Resource(gv.WithResource(strings.ToLower(kind) + "s")), true, nil
}

func newGatewayDeploymentObj(annotations map[string]string) runtime.Object {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion("apps/v1")
	u.SetKind("Deployment")
	u.SetNamespace("xmc")
	u.SetName("ingestion-gateway-deployment")
	u.SetAnnotations(annotations)
	return u
}

func verifyUmbrellaLegacyGatewayLookupGuard(t *testing.T, helmDir, wantDeployManifest string) {
	t.Helper()
	cases := []struct {
		name        string
		annotations map[string]string
		wantErr     string
	}{
		{name: "absent gateway deployment renders cleanly"},
		{name: "component-owned gateway renders cleanly", annotations: map[string]string{"meta.helm.sh/release-name": "xmc-cmp-ingestion-gateway"}},
		{name: "umbrella-owned gateway with keep policy renders cleanly", annotations: map[string]string{"meta.helm.sh/release-name": "xmanager-cloud", "helm.sh/resource-policy": "keep"}},
		{name: "unmigrated umbrella-owned gateway fails closed", annotations: map[string]string{"meta.helm.sh/release-name": "xmanager-cloud"}, wantErr: "Release still owns legacy Deployment/ingestion-gateway-deployment"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var objs []runtime.Object
			if tc.annotations != nil {
				objs = append(objs, newGatewayDeploymentObj(tc.annotations))
			}
			provider := fakeLookupProvider{client: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), objs...)}
			got, err := renderChartWithClient(helmDir, "xmanager-cloud", "xmc", map[string]any{}, provider)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) || !strings.Contains(err.Error(), "migrate.py") {
					t.Fatalf("render error = %v, want substrings %q and %q", err, tc.wantErr, "migrate.py")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected render error: %v", err)
			}
			if diff := cmp.Diff(wantDeployManifest, got["xmanager-cloud/templates/components/deployment.yaml"]); diff != "" {
				t.Errorf("components/deployment.yaml mismatch under lookup (-want +got):\n%s", diff)
			}
		})
	}
}

func TestIngestionGatewayFlatScalarParameterParity(t *testing.T) {
	t.Parallel()
	igDir := resolveRepoPath(t, filepath.Join("deploy", "profiles", "stable-2026a", "components", "ingestion-gateway"))

	t.Run("sasl tls hpa resources and custom namespace via bridge values", func(t *testing.T) {
		t.Parallel()
		rendered, err := renderChartWithNamespace(igDir, "xmc-cmp-ingestion-gateway", "custom-ns", map[string]any{
			"namespace": "custom-ns",
			"ingestionGatewayDeployment": map[string]any{
				"enabled":            true,
				"serviceAccountName": "custom-ig-sa",
				"hpaEnabled":         true,
				"hpaMinReplicas":     int64(3),
				"hpaMaxReplicas":     int64(12),
				"saslEnabled":        true,
				"saslMechanism":      "SCRAM-SHA-512",
				"saslSecretName":     "kafka-sasl-sec",
				"tlsEnabled":         true,
				"tlsSecretName":      "kafka-tls-sec",
				"tlsCaFile":          "/etc/kafka/tls/ca.crt",
				"tlsCertFile":        "/etc/kafka/tls/tls.crt",
				"tlsKeyFile":         "/etc/kafka/tls/tls.key",
				"cpuRequest":         "250m",
				"memoryRequest":      "512Mi",
				"cpuLimit":           "1000m",
				"memoryLimit":        "2Gi",
			},
		})
		if err != nil {
			t.Fatalf("renderChartWithNamespace failed: %v", err)
		}
		dep := rendered["ingestion-gateway/templates/deployment.yaml"]
		assertWellFormedManifests(t, dep)
		for _, want := range []string{
			`serviceAccountName: "custom-ig-sa"`,
			"runAsNonRoot: true",
			`"-components_namespace=custom-ns"`,
			`"-sasl_enabled=true"`,
			`"-sasl_mechanism=SCRAM-SHA-512"`,
			`"-sasl_credentials_dir=/etc/kafka/credentials"`,
			`secretName: "kafka-sasl-sec"`,
			`"-tls_enabled=true"`,
			`"-tls_ca_file=/etc/kafka/tls/ca.crt"`,
			`"-tls_cert_file=/etc/kafka/tls/tls.crt"`,
			`"-tls_key_file=/etc/kafka/tls/tls.key"`,
			`secretName: "kafka-tls-sec"`,
			"minReplicas: 3",
			"maxReplicas: 12",
			"cpu: 250m",
			"memory: 512Mi",
			"cpu: 1000m",
			"memory: 2Gi",
			`namespace: "custom-ns"`,
		} {
			if !strings.Contains(dep, want) {
				t.Errorf("expected %q in rendered workload:\n%s", want, dep)
			}
		}
		depSpec := manifestSpecByKind(t, dep, "Deployment")
		if _, hasReplicas := depSpec["replicas"]; hasReplicas {
			t.Errorf("expected Deployment.spec.replicas omitted when hpaEnabled=true, got %v", depSpec["replicas"])
		}
	})

	t.Run("hpa disabled scale to zero and standalone parameters.image parity", func(t *testing.T) {
		t.Parallel()
		rendered, err := renderChartWithNamespace(igDir, "ingestion-gateway", "xmc", map[string]any{
			"deployWorkload": true,
			"parameters": map[string]any{
				"image":                         "gcr.io/custom/ig@sha256:abcdef0123456789",
				"hpaEnabled":                    "FALSE",
				"replicas":                      0,
				"tlsEnabled":                    "TRUE",
				"projectId":                     "custom-proj",
				"terminationGracePeriodSeconds": 40,
			},
		})
		if err != nil {
			t.Fatalf("renderChartWithNamespace failed: %v", err)
		}
		dep := rendered["ingestion-gateway/templates/deployment.yaml"]
		assertWellFormedManifests(t, dep)
		if strings.Contains(dep, "kind: HorizontalPodAutoscaler") {
			t.Errorf("expected HorizontalPodAutoscaler omitted when hpaEnabled=FALSE:\n%s", dep)
		}
		if !strings.Contains(dep, "replicas: 0") {
			t.Errorf("expected static replicas: 0 preserved when hpaEnabled=FALSE:\n%s", dep)
		}
		if !strings.Contains(dep, `image: "gcr.io/custom/ig@sha256:abcdef0123456789"`) {
			t.Errorf("expected standalone parameters.image to resolve container image:\n%s", dep)
		}
		if !strings.Contains(dep, `"-project_id=custom-proj"`) || !strings.Contains(dep, "terminationGracePeriodSeconds: 40") {
			t.Errorf("expected standalone parameters.projectId and terminationGracePeriodSeconds applied:\n%s", dep)
		}
		if !strings.Contains(dep, `"-tls_enabled=true"`) || strings.Contains(dep, "kafka-tls-credentials") {
			t.Errorf("expected -tls_enabled=true without kafka-tls-credentials volume when tlsSecretName is empty:\n%s", dep)
		}
	})

	t.Run("empty scalar overrides keep hpa and replicas defaults and tls disabled omits cert flags", func(t *testing.T) {
		t.Parallel()
		rendered, err := renderChartWithNamespace(igDir, "xmc-cmp-ingestion-gateway", "xmc", map[string]any{
			"sasl": map[string]any{"enabled": true, "secretName": "fallback-sasl-sec"},
			"ingestionGatewayDeployment": map[string]any{
				"enabled":     true,
				"hpaEnabled":  "",
				"replicas":    "",
				"saslEnabled": "",
				"tlsEnabled":  false,
				"tlsCaFile":   "/etc/kafka/tls/ca.crt",
			},
		})
		if err != nil {
			t.Fatalf("renderChartWithNamespace failed: %v", err)
		}
		dep := rendered["ingestion-gateway/templates/deployment.yaml"]
		assertWellFormedManifests(t, dep)
		if !strings.Contains(dep, "kind: HorizontalPodAutoscaler") {
			t.Errorf("expected empty hpaEnabled override to preserve HPA default=true:\n%s", dep)
		}
		if !strings.Contains(dep, `"-sasl_enabled=true"`) || !strings.Contains(dep, `secretName: "fallback-sasl-sec"`) {
			t.Errorf("expected empty saslEnabled override to fall back to sasl.enabled=true:\n%s", dep)
		}
		if strings.Contains(dep, "-tls_ca_file=") {
			t.Errorf("expected -tls_ca_file omitted when tlsEnabled=false:\n%s", dep)
		}
	})

	t.Run("empty tlsEnabled override falls back to tls.enabled", func(t *testing.T) {
		t.Parallel()
		rendered, err := renderChartWithNamespace(igDir, "xmc-cmp-ingestion-gateway", "xmc", map[string]any{
			"tls": map[string]any{"enabled": true, "secretName": "fallback-tls-sec"},
			"ingestionGatewayDeployment": map[string]any{
				"enabled":    true,
				"tlsEnabled": "",
			},
		})
		if err != nil {
			t.Fatalf("renderChartWithNamespace failed: %v", err)
		}
		dep := rendered["ingestion-gateway/templates/deployment.yaml"]
		assertWellFormedManifests(t, dep)
		if !strings.Contains(dep, `"-tls_enabled=true"`) || !strings.Contains(dep, `secretName: "fallback-tls-sec"`) {
			t.Errorf("expected empty tlsEnabled override to fall back to tls.enabled=true:\n%s", dep)
		}
	})
}

func TestStable2026aCustomReleaseNamespace(t *testing.T) {
	t.Parallel()
	profileDir := resolveRepoPath(t, filepath.Join("deploy", "profiles", "stable-2026a"))

	for _, tc := range []struct {
		releaseNS string
		wantNS    string
	}{
		{releaseNS: "default", wantNS: "xmc"},
		{releaseNS: "tenant-prod", wantNS: "tenant-prod"},
	} {
		t.Run(tc.releaseNS, func(t *testing.T) {
			t.Parallel()
			rendered, err := renderChartWithNamespace(profileDir, "xmc-profile", tc.releaseNS, nil)
			if err != nil {
				t.Fatalf("renderChartWithNamespace(%q) failed: %v", tc.releaseNS, err)
			}
			dec := yaml.NewDecoder(strings.NewReader(rendered["xmc-profile-stable-2026a/templates/components.yaml"]))
			count := 0
			for {
				var cm linter.ComponentManifest
				err := dec.Decode(&cm)
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatalf("decode failed: %v", err)
				}
				count++
				if cm.Metadata.Namespace != tc.wantNS {
					t.Errorf("%s metadata.namespace = %q, want %q", cm.Metadata.Name, cm.Metadata.Namespace, tc.wantNS)
				}
			}
			if count != 5 {
				t.Errorf("decoded %d Component CRs, want 5", count)
			}
		})
	}
}
