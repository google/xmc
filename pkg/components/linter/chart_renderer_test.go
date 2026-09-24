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
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"gopkg.in/yaml.v3"

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
	writeTestManifest(t, chartDir, "values.yaml", customValues)
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
  clusterEndpoints: "clickhouse-custom.xmc-metrics.svc.cluster.local:9440"
  nativePort: 9440
  httpPort: 8443
  defaultDatabase: "analytics"
  user: "xmc_reader"
  secretName: "clickhouse-custom-secret"
  secretPasswordKey: "password"
  healthCheckEndpoint: "https://clickhouse-custom.xmc-metrics.svc.cluster.local:8443/ping"
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
						"secretPasswordKey":   "password",
						"healthCheckEndpoint": "https://clickhouse-custom.xmc-metrics.svc.cluster.local:8443/ping",
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
