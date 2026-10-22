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

package helm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/cli-runtime/pkg/resource"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage/driver"
)

// newFakeAPIServerGetter returns a RESTClientGetter pointing at an in-process API server that only serves /version,
// which is sufficient for Helm's reachability check when using the in-memory storage driver.
func newFakeAPIServerGetter(t *testing.T) genericclioptions.RESTClientGetter {
	t.Helper()
	return newHandlerGetter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/version" {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, `{"major":"1","minor":"30","gitVersion":"v1.30.0"}`)
	}))
}

// newHandlerGetter returns a RESTClientGetter whose kubeconfig points at an in-process API server backed by handler.
func newHandlerGetter(t *testing.T, handler http.Handler) genericclioptions.RESTClientGetter {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")
	content := "apiVersion: v1\nkind: Config\nclusters:\n- name: test\n  cluster:\n    server: " + srv.URL +
		"\ncontexts:\n- name: test\n  context:\n    cluster: test\n    user: test\ncurrent-context: test\nusers:\n- name: test\n  user: {}\n"
	if err := os.WriteFile(kubeconfig, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write kubeconfig: %v", err)
	}
	cacheDir := t.TempDir()
	flags := genericclioptions.NewConfigFlags(false)
	flags.KubeConfig = &kubeconfig
	flags.CacheDir = &cacheDir
	return flags
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// newConfigMapAPIServerGetter serves discovery for core/v1 ConfigMaps and answers ConfigMap GETs (in any namespace,
// since Helm's KubeClient resolves it from the getter) by name: names in present return 200, "broken" returns 500,
// and every other name returns 404.
func newConfigMapAPIServerGetter(t *testing.T, present ...string) genericclioptions.RESTClientGetter {
	t.Helper()
	return newHandlerGetter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, name, isConfigMap := strings.Cut(r.URL.Path, "/configmaps/")
		switch {
		case r.URL.Path == "/version":
			writeJSON(w, http.StatusOK, `{"major":"1","minor":"30","gitVersion":"v1.30.0"}`)
		case r.URL.Path == "/api":
			writeJSON(w, http.StatusOK, `{"kind":"APIVersions","versions":["v1"]}`)
		case r.URL.Path == "/apis":
			writeJSON(w, http.StatusOK, `{"kind":"APIGroupList","apiVersion":"v1","groups":[]}`)
		case r.URL.Path == "/api/v1":
			writeJSON(w, http.StatusOK, `{"kind":"APIResourceList","groupVersion":"v1","resources":[{"name":"configmaps","singularName":"configmap","namespaced":true,"kind":"ConfigMap","verbs":["get"]}]}`)
		case isConfigMap && slices.Contains(present, name):
			writeJSON(w, http.StatusOK, `{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"`+name+`"}}`)
		case isConfigMap && name == "broken":
			writeJSON(w, http.StatusInternalServerError, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"InternalError","code":500}`)
		default:
			writeJSON(w, http.StatusNotFound, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404}`)
		}
	}))
}

func configMapManifest(names ...string) string {
	var b strings.Builder
	for _, name := range names {
		b.WriteString("---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: " + name + "\n")
	}
	return b.String()
}

func TestSDKHelmActionRunner_MissingResources(t *testing.T) {
	tests := []struct {
		name    string
		runner  func(t *testing.T) *SDKHelmActionRunner
		rel     *release.Release
		want    []string
		wantErr bool
	}{
		{
			name:    "nil release",
			runner:  func(t *testing.T) *SDKHelmActionRunner { return NewSDKHelmActionRunner(newConfigMapAPIServerGetter(t)) },
			wantErr: true,
		},
		{
			name:    "nil restClientGetter",
			runner:  func(t *testing.T) *SDKHelmActionRunner { return NewSDKHelmActionRunner(nil) },
			rel:     &release.Release{Name: "xmc-cmp-a", Namespace: "xmc"},
			wantErr: true,
		},
		{
			name: "all objects present",
			runner: func(t *testing.T) *SDKHelmActionRunner {
				return NewSDKHelmActionRunner(newConfigMapAPIServerGetter(t, "a", "b"), WithDriver("memory"))
			},
			rel: &release.Release{Name: "xmc-cmp-a", Namespace: "xmc", Manifest: configMapManifest("a", "b")},
		},
		{
			name: "deleted objects are reported",
			runner: func(t *testing.T) *SDKHelmActionRunner {
				return NewSDKHelmActionRunner(newConfigMapAPIServerGetter(t, "a"), WithDriver("memory"))
			},
			rel:  &release.Release{Name: "xmc-cmp-a", Namespace: "xmc", Manifest: configMapManifest("a", "gone")},
			want: []string{"ConfigMap/gone"},
		},
		{
			name: "non-NotFound lookup error aborts the check",
			runner: func(t *testing.T) *SDKHelmActionRunner {
				return NewSDKHelmActionRunner(newConfigMapAPIServerGetter(t, "a"), WithDriver("memory"))
			},
			rel:     &release.Release{Name: "xmc-cmp-a", Namespace: "xmc", Manifest: configMapManifest("gone", "broken")},
			wantErr: true,
		},
		{
			name: "unparseable manifest",
			runner: func(t *testing.T) *SDKHelmActionRunner {
				return NewSDKHelmActionRunner(newConfigMapAPIServerGetter(t), WithDriver("memory"))
			},
			rel:     &release.Release{Name: "xmc-cmp-a", Namespace: "xmc", Manifest: "kind: [unterminated"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.runner(t).MissingResources(context.Background(), tt.rel)
			if (err != nil) != tt.wantErr {
				t.Fatalf("MissingResources() error = %v, wantErr %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("MissingResources() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestFindMissingResources_AbortsBeforeGet verifies the checks that must stop the loop before info.Get() runs. The
// infos have no Client, so reaching Get() would panic.
func TestFindMissingResources_AbortsBeforeGet(t *testing.T) {
	configMap := &unstructured.Unstructured{}
	configMap.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"})
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name    string
		ctx     context.Context
		info    *resource.Info
		wantErr string
	}{
		{
			name:    "cancelled context",
			ctx:     cancelled,
			info:    &resource.Info{Name: "a", Mapping: &meta.RESTMapping{GroupVersionKind: configMap.GroupVersionKind()}},
			wantErr: context.Canceled.Error(),
		},
		{
			name:    "nil REST mapping",
			ctx:     context.Background(),
			info:    &resource.Info{Name: "a", Object: configMap},
			wantErr: `ConfigMap "a" of Helm release "xmc-cmp-a" has no REST mapping`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := findMissingResources(tt.ctx, "xmc-cmp-a", []*resource.Info{tt.info})
			if err == nil || err.Error() != tt.wantErr {
				t.Errorf("findMissingResources() = (%v, %v), want error %q", got, err, tt.wantErr)
			}
		})
	}
}

func TestResourceKind(t *testing.T) {
	configMap := &unstructured.Unstructured{}
	configMap.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"})
	tests := []struct {
		name string
		info *resource.Info
		want string
	}{
		{name: "from mapping", info: &resource.Info{Mapping: &meta.RESTMapping{GroupVersionKind: schema.GroupVersionKind{Kind: "Role"}}, Object: configMap}, want: "Role"},
		{name: "from object", info: &resource.Info{Object: configMap}, want: "ConfigMap"},
		{name: "object without kind", info: &resource.Info{Object: &unstructured.Unstructured{}}, want: "Unknown"},
		{name: "neither", info: &resource.Info{}, want: "Unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resourceKind(tt.info); got != tt.want {
				t.Errorf("resourceKind() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSDKHelmActionRunner_Status(t *testing.T) {
	tests := []struct {
		name           string
		runner         func(t *testing.T) *SDKHelmActionRunner
		wantErr        bool
		wantNotFoundIs bool
	}{
		{
			name:    "nil runner",
			runner:  func(t *testing.T) *SDKHelmActionRunner { return nil },
			wantErr: true,
		},
		{
			name:    "nil restClientGetter",
			runner:  func(t *testing.T) *SDKHelmActionRunner { return NewSDKHelmActionRunner(nil) },
			wantErr: true,
		},
		{
			name: "missing release maps to driver.ErrReleaseNotFound",
			runner: func(t *testing.T) *SDKHelmActionRunner {
				return NewSDKHelmActionRunner(newFakeAPIServerGetter(t), WithDriver("memory"))
			},
			wantErr:        true,
			wantNotFoundIs: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rel, err := tt.runner(t).Status(context.Background(), "xmc-cmp-ingestion-gateway", "xmc")
			if (err != nil) != tt.wantErr {
				t.Fatalf("Status() error = %v, wantErr %v", err, tt.wantErr)
			}
			if rel != nil {
				t.Errorf("Status() release = %v, want nil", rel)
			}
			if got := errors.Is(err, driver.ErrReleaseNotFound); got != tt.wantNotFoundIs {
				t.Errorf("errors.Is(err, driver.ErrReleaseNotFound) = %v, want %v (err: %v)", got, tt.wantNotFoundIs, err)
			}
		})
	}
}

func TestSDKHelmActionRunner_Options(t *testing.T) {
	var nilRunner *SDKHelmActionRunner
	rel, err := nilRunner.Upgrade(context.Background(), "test", "xmc", nil, nil)
	if err == nil || rel != nil {
		t.Errorf("expected error on nil runner Upgrade, got rel=%v, err=%v", rel, err)
	}

	dummyChart := &chart.Chart{
		Metadata: &chart.Metadata{Name: "custom-chart", Version: "1.0.0"},
	}
	runner := NewSDKHelmActionRunner(nil,
		WithDriver("memory"),
		WithRunnerChartPath("/tmp/charts"),
		WithRunnerChartObj(dummyChart),
	)
	if runner.helmDriver != "memory" {
		t.Errorf("expected driver memory, got %q", runner.helmDriver)
	}
	if runner.chartPath != "/tmp/charts" {
		t.Errorf("expected chart path /tmp/charts, got %q", runner.chartPath)
	}
	if runner.chartObj != dummyChart {
		t.Errorf("expected custom chart object, got %v", runner.chartObj)
	}

	// Calling Upgrade with nil restClientGetter should fail at actionConfig.Init
	_, err = runner.Upgrade(context.Background(), "test", "xmc", nil, map[string]any{})
	if err == nil {
		t.Error("expected error from Upgrade when restClientGetter is nil")
	}

	// Calling Uninstall on nil runner returns error
	resp, err := nilRunner.Uninstall(context.Background(), "test", "xmc")
	if err == nil || resp != nil {
		t.Errorf("expected error on nil runner Uninstall, got resp=%v, err=%v", resp, err)
	}

	// Calling Uninstall with nil restClientGetter should fail at actionConfig.Init
	_, err = runner.Uninstall(context.Background(), "test", "xmc")
	if err == nil {
		t.Error("expected error from Uninstall when restClientGetter is nil")
	}
}

func TestSDKHelmActionRunner_ResolveChart_RWMutex(t *testing.T) {
	runner := NewSDKHelmActionRunner(nil, WithRunnerChartPath("/invalid/path/to/chart"))

	// First load fails
	_, err := runner.resolveChart("test-release", nil)
	if err == nil {
		t.Fatal("expected error loading from invalid path, got nil")
	}

	// Dynamic override of chartObj should be thread-safe and succeed immediately
	expectedChart := &chart.Chart{
		Metadata: &chart.Metadata{Name: "test-chart"},
	}
	resolved, err := runner.resolveChart("test-release", expectedChart)
	if err != nil {
		t.Fatalf("unexpected error with explicit chart: %v", err)
	}
	if resolved.Name() != "test-chart" {
		t.Errorf("expected resolved chart name %q, got %q", "test-chart", resolved.Name())
	}
}

func TestSDKHelmActionRunner_Uninstall(t *testing.T) {
	var nilRunner *SDKHelmActionRunner
	rel, err := nilRunner.Uninstall(context.Background(), "test", "xmc")
	if err == nil || rel != nil {
		t.Errorf("expected error on nil runner Uninstall, got rel=%v, err=%v", rel, err)
	}

	runner := NewSDKHelmActionRunner(nil)
	_, err = runner.Uninstall(context.Background(), "test", "xmc")
	if err == nil {
		t.Error("expected error from Uninstall when restClientGetter is nil")
	}
}

func TestSDKHelmActionRunner_ResolveChart_DefaultFallback(t *testing.T) {
	runner := NewSDKHelmActionRunner(nil)
	resolved, err := runner.resolveChart("xmc-cmp-ray-operator", nil)
	if err != nil {
		t.Fatalf("unexpected error resolving default chart: %v", err)
	}
	if resolved == nil {
		t.Fatal("expected non-nil default chart")
	}
	if resolved.Name() != "xmc-cmp-ray-operator" {
		t.Errorf("expected chart name %q, got %q", "xmc-cmp-ray-operator", resolved.Name())
	}
	if resolved.Metadata == nil || resolved.Metadata.APIVersion != "v2" {
		t.Errorf("expected v2 metadata, got %v", resolved.Metadata)
	}
}

// TestNewUpgradeAction_CapsReleaseHistory guards against component release Secrets accumulating without bound when
// a release is upgraded repeatedly (e.g. by drift repairs): the SDK default MaxHistory of 0 means unlimited.
func TestNewUpgradeAction_CapsReleaseHistory(t *testing.T) {
	upgrade := newUpgradeAction(&action.Configuration{}, "xmc")
	if upgrade.MaxHistory != componentReleaseMaxHistory {
		t.Errorf("MaxHistory = %d, want %d", upgrade.MaxHistory, componentReleaseMaxHistory)
	}
	if upgrade.Namespace != "xmc" {
		t.Errorf("Namespace = %q, want %q", upgrade.Namespace, "xmc")
	}
}
