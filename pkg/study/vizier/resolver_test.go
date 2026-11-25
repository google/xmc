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
	obj := map[string]any{
		"apiVersion": "xmc.dev/v1alpha1",
		"kind":       "Component",
		"metadata":   map[string]any{"name": "vizier", "namespace": "xmc"},
		"spec":       spec,
	}
	if state != "" {
		obj["status"] = map[string]any{"state": state}
	}
	return &unstructured.Unstructured{Object: obj}
}

func TestValidateEndpoint(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{name: "dns host and port", raw: "vizier-service.xmc.svc.cluster.local:7077"},
		{name: "ipv4 and port", raw: "10.0.0.5:7077"},
		{name: "bracketed ipv6 and port", raw: "[::1]:7077"},
		{name: "empty", raw: "", wantErr: true},
		{name: "whitespace only", raw: "   \t ", wantErr: true},
		{name: "leading whitespace", raw: " vizier:7077", wantErr: true},
		{name: "trailing whitespace", raw: "vizier:7077 ", wantErr: true},
		{name: "missing port", raw: "vizier-service", wantErr: true},
		{name: "empty port", raw: "vizier-service:", wantErr: true},
		{name: "empty host", raw: ":7077", wantErr: true},
		{name: "port zero", raw: "vizier-service:0", wantErr: true},
		{name: "port 65536", raw: "vizier-service:65536", wantErr: true},
		{name: "non-numeric port", raw: "vizier-service:grpc", wantErr: true},
		{name: "signed port", raw: "vizier-service:+7077", wantErr: true},
		{name: "unbracketed ipv6", raw: "::1:7077", wantErr: true},
		{name: "malformed bracketed ipv6", raw: "[not:ip]:7077", wantErr: true},
		{name: "http scheme", raw: "http://vizier-service:7077", wantErr: true},
		{name: "https scheme", raw: "https://vizier-service:7077", wantErr: true},
		{name: "grpc scheme", raw: "grpc://vizier-service:7077", wantErr: true},
		{name: "userinfo credentials", raw: "user:s3cr3t@vizier-service:7077", wantErr: true},
		{name: "path", raw: "vizier-service:7077/v1", wantErr: true},
		{name: "query", raw: "vizier-service:7077?foo=bar", wantErr: true},
		{name: "fragment", raw: "vizier-service:7077#frag", wantErr: true},
		{name: "multibyte host", raw: "viziér:7077", wantErr: true},
		{name: "multibyte digits", raw: "vizier:７０７７", wantErr: true},
		{name: "control character", raw: "vizier\x00:7077", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := validateEndpoint(tc.raw)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateEndpoint(%q) error = %v, wantErr %v", tc.raw, err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.raw {
				t.Errorf("validateEndpoint(%q) = %q, want %q", tc.raw, got, tc.raw)
			}
			if err != nil && tc.raw != "" && strings.Contains(err.Error(), tc.raw) {
				t.Errorf("validateEndpoint error %q leaked raw input %q", err.Error(), tc.raw)
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
		{
			name:   "present and enabled",
			getter: &crreadertest.FakeGetter{Component: vizierComponent(true, "READY", "vizier-service.xmc.svc.cluster.local:7077", true)},
			want:   "vizier-service.xmc.svc.cluster.local:7077",
		},
		{
			name:    "absent component",
			getter:  &crreadertest.FakeGetter{},
			wantErr: true,
		},
		{
			name:    "disabled via spec.enabled=false",
			getter:  &crreadertest.FakeGetter{Component: vizierComponent(false, "READY", "vizier-service:7077", true)},
			wantErr: true,
		},
		{
			name:    "disabled via status.state=DISABLED",
			getter:  &crreadertest.FakeGetter{Component: vizierComponent(true, "DISABLED", "vizier-service:7077", true)},
			wantErr: true,
		},
		{
			name:    "missing parameters",
			getter:  &crreadertest.FakeGetter{Component: vizierComponent(true, "READY", "", false)},
			wantErr: true,
		},
		{
			name:    "missing parameters.endpoint",
			getter:  &crreadertest.FakeGetter{Component: vizierComponent(true, "READY", "", true)},
			wantErr: true,
		},
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

func (g *scriptedGetter) GetComponent(_ context.Context, _, _ string) (*unstructured.Unstructured, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.idx >= len(g.steps) {
		return g.steps[len(g.steps)-1]()
	}
	fn := g.steps[g.idx]
	g.idx++
	return fn()
}

var _ crreader.Getter = (*scriptedGetter)(nil)

func TestWaitForEndpoint_TransitionsAndSecretFreeLogs(t *testing.T) {
	t.Parallel()
	const secretEndpoint = "https://user:s3cr3t@bad-host/path"
	var logs bytes.Buffer
	ctx := logging.WithLogger(context.Background(), slog.New(slog.NewTextHandler(&logs, nil)))

	absent := &crreadertest.FakeGetter{}
	getter := &scriptedGetter{
		steps: []func() (*unstructured.Unstructured, error){
			func() (*unstructured.Unstructured, error) { return absent.GetComponent(ctx, "xmc", "vizier") },
			func() (*unstructured.Unstructured, error) { return nil, errors.New("transient apiserver error") },
			func() (*unstructured.Unstructured, error) {
				return vizierComponent(false, "READY", "vizier-service:7077", true), nil
			},
			func() (*unstructured.Unstructured, error) {
				return vizierComponent(true, "READY", secretEndpoint, true), nil
			},
			func() (*unstructured.Unstructured, error) {
				return vizierComponent(true, "READY", "vizier-service.xmc.svc.cluster.local:7077", true), nil
			},
		},
	}

	got, err := waitForEndpoint(ctx, getter, "", time.Millisecond, 4*time.Millisecond)
	if err != nil {
		t.Fatalf("waitForEndpoint() error = %v, want nil", err)
	}
	if got != "vizier-service.xmc.svc.cluster.local:7077" {
		t.Errorf("waitForEndpoint() = %q, want vizier-service.xmc.svc.cluster.local:7077", got)
	}
	logged := logs.String()
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
	_, err := waitForEndpoint(ctx, &crreadertest.FakeGetter{}, "xmc", 10*time.Millisecond, 10*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("waitForEndpoint() error = %v, want context.DeadlineExceeded", err)
	}
}
