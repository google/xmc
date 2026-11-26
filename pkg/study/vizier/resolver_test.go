// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package vizier

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/pkg/components/crreader"
	"xmanager_cloud/pkg/components/crreader/crreadertest"
)

func vizierComponent(enabled bool, state, endpoint string, withParams bool) *unstructured.Unstructured {
	spec := map[string]any{"enabled": enabled}
	if withParams {
		params := map[string]any{}
		if endpoint != "" {
			params["endpoint"] = endpoint
		}
		spec["parameters"] = params
	}
	obj := map[string]any{"apiVersion": "xmc.dev/v1alpha1", "kind": "Component", "metadata": map[string]any{"name": "vizier", "namespace": "xmc"}, "spec": spec}
	if state != "" {
		obj["status"] = map[string]any{"state": state}
	}
	return &unstructured.Unstructured{Object: obj}
}

func TestValidateEndpoint(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"vizier-service.xmc.svc.cluster.local:7077", "unix:7077", "10.0.0.5:7077", "[::1]:7077"} {
		t.Run("valid/"+raw, func(t *testing.T) {
			t.Parallel()
			if got, err := validateEndpoint(raw); err != nil || got != raw {
				t.Fatalf("validateEndpoint(%q) = (%q, %v), want (%q, nil)", raw, got, err, raw)
			}
		})
	}
	invalid := []string{
		"", "   \t ", " vizier:7077", "vizier:7077 ", "vizier-service", "vizier-service:", ":7077",
		"vizier-service:0", "vizier-service:65536", "vizier:07077", "vizier-service:grpc", "vizier-service:+7077",
		"::1:7077", "[not:ip]:7077", "[vizier-service]:7077", "vizier,bad:7077", "vizier!bad:7077",
		"http://vizier-service:7077", "https://vizier-service:7077", "grpc://vizier-service:7077",
		"user:s3cr3t@vizier-service:7077", "s3cr3t%zz:7077", "vizier-service:7077/v1",
		"vizier-service:7077?foo=bar", "vizier-service:7077#frag", "viziér:7077", "vizier:７０７７", "vizier\x00:7077",
	}
	for _, raw := range invalid {
		t.Run("invalid/"+raw, func(t *testing.T) {
			t.Parallel()
			_, err := validateEndpoint(raw)
			if err == nil {
				t.Fatalf("validateEndpoint(%q) = nil, want error", raw)
			}
			if raw != "" && strings.Contains(err.Error(), raw) {
				t.Errorf("validateEndpoint error %q leaked raw input %q", err.Error(), raw)
			}
		})
	}
}

func TestResolveEndpoint(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		getter  *crreadertest.FakeGetter
		want    string
		wantErr bool
	}{
		{name: "present and enabled", getter: &crreadertest.FakeGetter{Components: map[string]*unstructured.Unstructured{"xmc/vizier": vizierComponent(true, "COMPONENT_STATE_ENABLED", "vizier-service.xmc.svc.cluster.local:7077", true)}}, want: "vizier-service.xmc.svc.cluster.local:7077"},
		{name: "absent component", getter: &crreadertest.FakeGetter{}, wantErr: true},
		{name: "disabled via spec.enabled=false", getter: &crreadertest.FakeGetter{Component: vizierComponent(false, "COMPONENT_STATE_ENABLED", "vizier-service:7077", true)}, wantErr: true},
		{name: "disabled via status.state=COMPONENT_STATE_DISABLED", getter: &crreadertest.FakeGetter{Component: vizierComponent(true, crreader.StateDisabled, "vizier-service:7077", true)}, wantErr: true},
		{name: "disabling via status.state=COMPONENT_STATE_DISABLING", getter: &crreadertest.FakeGetter{Component: vizierComponent(true, crreader.StateDisabling, "vizier-service:7077", true)}, wantErr: true},
		{name: "deleted via status.state=COMPONENT_STATE_DELETED", getter: &crreadertest.FakeGetter{Component: vizierComponent(true, crreader.StateDeleted, "vizier-service:7077", true)}, wantErr: true},
		{name: "missing parameters", getter: &crreadertest.FakeGetter{Component: vizierComponent(true, "COMPONENT_STATE_ENABLED", "", false)}, wantErr: true},
		{name: "missing parameters.endpoint", getter: &crreadertest.FakeGetter{Component: vizierComponent(true, "COMPONENT_STATE_ENABLED", "", true)}, wantErr: true},
		{name: "non-bool spec.enabled", getter: &crreadertest.FakeGetter{Component: &unstructured.Unstructured{Object: map[string]any{"apiVersion": "xmc.dev/v1alpha1", "kind": "Component", "metadata": map[string]any{"name": "vizier", "namespace": "xmc"}, "spec": map[string]any{"enabled": "true", "parameters": map[string]any{"endpoint": "vizier-service:7077"}}}}}, wantErr: true},
		{name: "non-string status.state", getter: &crreadertest.FakeGetter{Component: &unstructured.Unstructured{Object: map[string]any{"apiVersion": "xmc.dev/v1alpha1", "kind": "Component", "metadata": map[string]any{"name": "vizier", "namespace": "xmc"}, "spec": map[string]any{"enabled": true, "parameters": map[string]any{"endpoint": "vizier-service:7077"}}, "status": map[string]any{"state": int64(123)}}}}, wantErr: true},
		{name: "non-map spec.parameters", getter: &crreadertest.FakeGetter{Component: &unstructured.Unstructured{Object: map[string]any{"apiVersion": "xmc.dev/v1alpha1", "kind": "Component", "metadata": map[string]any{"name": "vizier", "namespace": "xmc"}, "spec": map[string]any{"enabled": true, "parameters": "invalid"}}}}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := resolveEndpoint(context.Background(), tc.getter, "xmc")
			if (err != nil) != tc.wantErr {
				t.Fatalf("resolveEndpoint() error = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("resolveEndpoint() = %q, want %q", got, tc.want)
			}
		})
	}
}

type scriptedGetter struct {
	mu    sync.Mutex
	steps []func() (*unstructured.Unstructured, error)
	idx   int
}

func (g *scriptedGetter) GetComponent(ctx context.Context, ns, name string) (*unstructured.Unstructured, error) {
	if _, ok := ctx.Deadline(); !ok {
		return nil, errors.New("missing GetComponent deadline")
	}
	if ns != "xmc" || name != "vizier" {
		return nil, errors.New("unexpected component lookup: " + ns + "/" + name)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	idx := min(g.idx, len(g.steps)-1)
	g.idx++
	return g.steps[idx]()
}

var _ crreader.Getter = (*scriptedGetter)(nil)

func TestWaitForEndpoint_TransitionsAndSecretFreeLogs(t *testing.T) {
	t.Parallel()
	const secretEndpoint = "https://user:s3cr3t@bad-host/path"
	var logs bytes.Buffer
	ctx := logging.WithLogger(context.Background(), slog.New(slog.NewTextHandler(&logs, nil)))

	absent := &crreadertest.FakeGetter{}
	ret := func(c *unstructured.Unstructured, err error) func() (*unstructured.Unstructured, error) {
		return func() (*unstructured.Unstructured, error) { return c, err }
	}
	getter := &scriptedGetter{steps: []func() (*unstructured.Unstructured, error){
		func() (*unstructured.Unstructured, error) { return absent.GetComponent(ctx, "xmc", "vizier") },
		ret(nil, errors.New("transient apiserver error")),
		ret(vizierComponent(false, "COMPONENT_STATE_ENABLED", "vizier-service:7077", true), nil),
		ret(vizierComponent(true, "COMPONENT_STATE_ENABLED", secretEndpoint, true), nil),
		ret(vizierComponent(true, "COMPONENT_STATE_ENABLED", "vizier-service.xmc.svc.cluster.local:7077", true), nil),
	}}

	got, err := waitForEndpoint(ctx, getter, "", time.Millisecond, 4*time.Millisecond)
	if err != nil {
		t.Fatalf("waitForEndpoint() error = %v, want nil", err)
	}
	if got != "vizier-service.xmc.svc.cluster.local:7077" {
		t.Errorf("waitForEndpoint() = %q, want vizier-service.xmc.svc.cluster.local:7077", got)
	}
	logged := logs.String()
	if !strings.Contains(logged, "Waiting for valid Vizier") || !strings.Contains(logged, "Resolved Vizier endpoint") {
		t.Errorf("expected transition logs, got:\n%s", logged)
	}
	for _, secret := range []string{secretEndpoint, "s3cr3t", "user:s3cr3t", "bad-host"} {
		if strings.Contains(logged, secret) {
			t.Errorf("logs leaked secret fragment %q:\n%s", secret, logged)
		}
	}
}

func TestWaitForEndpoint_DeadlineAndNilGetter(t *testing.T) {
	t.Parallel()
	if _, err := WaitForEndpoint(context.Background(), nil, "xmc"); err == nil {
		t.Fatal("WaitForEndpoint(nil) error = nil, want non-nil")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, err := waitForEndpoint(ctx, &crreadertest.FakeGetter{}, "xmc", 10*time.Millisecond, 10*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("waitForEndpoint() error = %v, want context.DeadlineExceeded", err)
	}
}
