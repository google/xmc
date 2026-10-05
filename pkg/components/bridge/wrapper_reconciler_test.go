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

package bridge

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

func TestReconcileComponent_3PWrapperAdvanced(t *testing.T) {
	ctx := context.Background()
	spec3P := &componentspb.ComponentSpec{
		Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
		WrapperRef: &componentspb.WrapperRef{
			CrdGroup: "ray.io", CrdVersion: "v1", CrdKind: "RayCluster", TargetName: "ray-1", TargetNamespace: "xmc",
		},
	}

	t.Run("preserves component observed generation and does not overwrite with 3p resource gen", func(t *testing.T) {
		detector := NewFakeStatusDetector()
		detector.SetStatus("xmc/ray-1", &ResourceStatus{
			Found:              true,
			Ready:              true,
			ObservedGeneration: 999,
			Endpoint:           "http://ray-head.xmc.svc.cluster.local:8265",
			ConnectionSecret:   "ray-secret",
		})
		prober := NewFakeHealthProber()
		prober.SetResult("http://ray-head.xmc.svc.cluster.local:8265", &HealthProbeResult{Healthy: true, Message: "HTTP 200"})
		bp := NewBridgeProber(detector, prober, time.Second)

		cms := &mockCMS{}
		r, _ := NewReconciler(cms, bp, nil)

		res, err := r.ReconcileComponent(ctx, ReconcileComponentInput{
			Namespace: "xmc", Name: "ray-1", Generation: 5, Spec: spec3P, Enabled: true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.ObservedGeneration != 5 {
			t.Errorf("expected ObservedGeneration 5 (Component gen), got %d", res.ObservedGeneration)
		}
		wantAccess := &AccessStatus{
			EnvVars: map[string]string{
				"ENDPOINT":    "http://ray-head.xmc.svc.cluster.local:8265",
				"HOST":        "ray-head.xmc.svc.cluster.local",
				"PORT":        "8265",
				"SCHEME":      "http",
				"SECRET_NAME": "ray-secret",
			},
		}
		if !reflect.DeepEqual(res.Access, wantAccess) {
			t.Errorf("ReconcileComponent() access = %+v, want %+v", res.Access, wantAccess)
		}
		for _, c := range res.Conditions {
			if c.ObservedGeneration != 5 {
				t.Errorf("condition %s has observedGeneration %d, want 5", c.Type, c.ObservedGeneration)
			}
		}
	})

	t.Run("handles resource not found vs crd not found", func(t *testing.T) {
		detector := NewFakeStatusDetector()
		detector.SetStatus("xmc/missing-res", &ResourceStatus{
			Found:   false,
			Ready:   false,
			Message: "resource xmc/missing-res not found",
		})
		detector.SetStatus("xmc/missing-crd", &ResourceStatus{
			Found:   false,
			Ready:   false,
			Message: "CRD not installed: no matches for kind \"Foo\"",
		})
		bp := NewBridgeProber(detector, nil, time.Second)
		r, _ := NewReconciler(&mockCMS{}, bp, nil)

		// Case 1: ResourceNotFound
		res1, err := r.ReconcileComponent(ctx, ReconcileComponentInput{
			Namespace: "xmc", Name: "missing-res", Generation: 1,
			Spec: &componentspb.ComponentSpec{
				Type:       componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
				WrapperRef: &componentspb.WrapperRef{TargetName: "missing-res", TargetNamespace: "xmc"},
			},
			Enabled: true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		foundReason := false
		for _, c := range res1.Conditions {
			if c.Type == ConditionTypeReady && c.Reason == "ResourceNotFound" {
				foundReason = true
			}
		}
		if !foundReason {
			t.Errorf("expected Condition Ready reason ResourceNotFound, got: %+v", res1.Conditions)
		}

		// Case 2: CRDNotFound
		res2, err := r.ReconcileComponent(ctx, ReconcileComponentInput{
			Namespace: "xmc", Name: "missing-crd", Generation: 1,
			Spec: &componentspb.ComponentSpec{
				Type:       componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
				WrapperRef: &componentspb.WrapperRef{TargetName: "missing-crd", TargetNamespace: "xmc"},
			},
			Enabled: true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		foundCRDReason := false
		for _, c := range res2.Conditions {
			if c.Type == ConditionTypeReady && c.Reason == "CRDNotFound" {
				foundCRDReason = true
			}
		}
		if !foundCRDReason {
			t.Errorf("expected Condition Ready reason CRDNotFound, got: %+v", res2.Conditions)
		}
	})
}

func TestReconcile3PWrapper_CrossNamespaceForbidden(t *testing.T) {
	ctx := context.Background()
	rec := &Reconciler{
		cmsClient: &mockCMS{},
		prober:    &mockProber{},
	}
	in := ReconcileComponentInput{
		Name:      "my-3p-comp",
		Namespace: "tenant-a",
		Enabled:   true,
		Spec: &componentspb.ComponentSpec{
			Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
			WrapperRef: &componentspb.WrapperRef{
				CrdGroup:        "clickhouse.altinity.com",
				CrdVersion:      "v1",
				CrdKind:         "ClickHouseInstallation",
				TargetName:      "chi-cluster",
				TargetNamespace: "kube-system",
			},
		},
	}
	res, err := rec.ReconcileComponent(ctx, in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.State != componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION {
		t.Errorf("expected PENDING_RECONCILIATION, got %v", res.State)
	}
	var readyCond *Condition
	for i := range res.Conditions {
		if res.Conditions[i].Type == ConditionTypeReady {
			readyCond = &res.Conditions[i]
			break
		}
	}
	if readyCond == nil || readyCond.Status != ConditionStatusFalse || readyCond.Reason != "CrossNamespaceForbidden" {
		t.Errorf("expected Ready condition False with reason CrossNamespaceForbidden, got %+v", readyCond)
	}
}

func TestPopulateAccessEnvVars(t *testing.T) {
	t.Run("nil result or nil status does not panic", func(t *testing.T) {
		populateAccessEnvVars(nil, &ResourceStatus{})
		res := &ReconcileResult{}
		populateAccessEnvVars(res, nil)
		if res.Access != nil {
			t.Errorf("expected Access to remain nil on nil status")
		}
	})

	t.Run("populates access with uninitialized EnvVars map", func(t *testing.T) {
		res := &ReconcileResult{}
		status := &ResourceStatus{
			Endpoint:         "http://service.prod:8080",
			ConnectionSecret: "db-secret",
		}
		populateAccessEnvVars(res, status)
		wantAccess := &AccessStatus{
			EnvVars: map[string]string{
				"ENDPOINT":    "http://service.prod:8080",
				"HOST":        "service.prod",
				"PORT":        "8080",
				"SCHEME":      "http",
				"SECRET_NAME": "db-secret",
			},
		}
		if !reflect.DeepEqual(res.Access, wantAccess) {
			t.Errorf("populateAccessEnvVars() access = %+v, want %+v", res.Access, wantAccess)
		}
	})
}

func TestReconcile_OperatorlessProfile3(t *testing.T) {
	ctx := context.Background()
	cms := &mockCMS{}
	prober := &mockProber{
		probeRes: &HealthProbeResult{Healthy: true, Message: "Service operational"},
	}
	r, err := NewReconciler(cms, prober, nil)
	if err != nil {
		t.Fatalf("NewReconciler failed: %v", err)
	}

	specProfile3 := &componentspb.ComponentSpec{
		Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
	}

	// Case 1: Operator-less component with static endpoint probes healthy
	in := ReconcileComponentInput{
		Namespace:          "xmc",
		Name:               "external-saas-db",
		Generation:         1,
		Spec:               specProfile3,
		HasFinalizer:       true,
		DeletionProtection: true,
		Enabled:            true,
		WrapperOpts: WrapperRef{
			EndpointJSONPath: "http://external-db.example.com:5432",
		},
	}

	res, err := r.ReconcileComponent(ctx, in)
	if err != nil {
		t.Fatalf("ReconcileComponent failed: %v", err)
	}
	if res.State != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
		t.Errorf("expected COMPONENT_STATE_ENABLED, got %v", res.State)
	}
	if res.Access == nil || res.Access.EnvVars["ENDPOINT"] != "http://external-db.example.com:5432" {
		t.Errorf("expected endpoint published in access env vars: %+v", res.Access)
	}

	// Case 2: Operator-less component with failing endpoint probes unready
	unhealthyProber := &mockProber{
		probeRes: &HealthProbeResult{Healthy: false, Message: "Connection timed out"},
	}
	rUnhealthy, _ := NewReconciler(cms, unhealthyProber, nil)
	resUnhealthy, err := rUnhealthy.ReconcileComponent(ctx, in)
	if err != nil {
		t.Fatalf("unexpected error on unready probe: %v", err)
	}
	if resUnhealthy.State != componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION {
		t.Errorf("expected COMPONENT_STATE_PENDING_RECONCILIATION, got %v", resUnhealthy.State)
	}
}

func TestVerifyEndpointHealth(t *testing.T) {
	const endpoint = "http://svc.xmc:8080"
	type verdict struct {
		OK      bool
		Reason  string
		Message string
	}
	tests := []struct {
		name     string
		endpoint string
		prober   *mockProber
		want     verdict
	}{
		{name: "empty endpoint skips probe", prober: &mockProber{probeErr: errors.New("unused")}, want: verdict{OK: true}},
		{name: "healthy discards probe message", endpoint: endpoint, prober: &mockProber{probeRes: &HealthProbeResult{Healthy: true, Message: "HTTP 200"}}, want: verdict{OK: true}},
		{
			name:     "probe error",
			endpoint: endpoint,
			prober:   &mockProber{probeErr: errors.New("connection refused")},
			want:     verdict{Reason: "ProbeFailed", Message: "Health probe failed for http://svc.xmc:8080: connection refused"},
		},
		{
			name:     "nil probe result",
			endpoint: endpoint,
			prober:   &mockProber{hasProbeRes: true},
			want:     verdict{Reason: "ProbeFailed", Message: "Health probe returned nil result for http://svc.xmc:8080"},
		},
		{
			name:     "unhealthy without message",
			endpoint: endpoint,
			prober:   &mockProber{probeRes: &HealthProbeResult{}},
			want:     verdict{Reason: "ProbeUnhealthy", Message: "Health probe unhealthy for http://svc.xmc:8080"},
		},
		{
			name:     "unhealthy with message",
			endpoint: endpoint,
			prober:   &mockProber{probeRes: &HealthProbeResult{Message: "HTTP 503 (unhealthy status)"}},
			want:     verdict{Reason: "ProbeUnhealthy", Message: "HTTP 503 (unhealthy status)"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Reconciler{prober: tt.prober}
			ok, reason, msg := r.verifyEndpointHealth(context.Background(), tt.endpoint)
			if diff := cmp.Diff(tt.want, verdict{OK: ok, Reason: reason, Message: msg}); diff != "" {
				t.Errorf("verifyEndpointHealth() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestResolveOperatorlessEndpointAndIPv6(t *testing.T) {
	tests := []struct {
		name string
		in   ReconcileComponentInput
		want string
	}{
		{
			name: "JSONPath EndpointJSONPath is ignored when wrapperRef is nil",
			in: ReconcileComponentInput{
				WrapperOpts: WrapperRef{EndpointJSONPath: "$.status.endpoint"},
				Spec: &componentspb.ComponentSpec{
					Type:       componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
					Parameters: map[string]string{"endpoint": "http://clickhouse:8123"},
				},
			},
			want: "http://clickhouse:8123",
		},
		{
			name: "Static EndpointJSONPath override is returned",
			in: ReconcileComponentInput{
				WrapperOpts: WrapperRef{EndpointJSONPath: "http://static:8123"},
			},
			want: "http://static:8123",
		},
		{
			name: "Non-nil WrapperRef does not fall through to parameters",
			in: ReconcileComponentInput{
				Spec: &componentspb.ComponentSpec{
					Type:       componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
					WrapperRef: &componentspb.WrapperRef{CrdKind: "ClickHouseInstallation"},
					Parameters: map[string]string{"host": "clickhouse", "httpPort": "8123"},
				},
			},
			want: "",
		},
		{
			name: "IPv6 host is bracketed when combined with port",
			in: ReconcileComponentInput{
				Spec: &componentspb.ComponentSpec{
					Type:       componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
					Parameters: map[string]string{"host": " , 2001:db8::1 , ::1 ", "httpPort": "8123"},
				},
			},
			want: "[2001:db8::1]:8123",
		},
		{
			name: "host already containing port is returned directly",
			in: ReconcileComponentInput{
				Spec: &componentspb.ComponentSpec{
					Type:       componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
					Parameters: map[string]string{"host": "my-service:8080", "httpPort": "8123"},
				},
			},
			want: "my-service:8080",
		},
		{
			name: "bracketed IPv6 host with port is returned directly",
			in: ReconcileComponentInput{
				Spec: &componentspb.ComponentSpec{
					Type:       componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
					Parameters: map[string]string{"host": "[2001:db8::1]:8080", "httpPort": "8123"},
				},
			},
			want: "[2001:db8::1]:8080",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveOperatorlessEndpoint(tc.in); got != tc.want {
				t.Errorf("resolveOperatorlessEndpoint() = %q, want %q", got, tc.want)
			}
		})
	}
	params := map[string]string{
		"host":                "clickhouse.xmc-metrics.svc.cluster.local",
		"nativePort":          "9000",
		"httpPort":            "8123",
		"healthCheckEndpoint": "http://clickhouse.xmc-metrics.svc.cluster.local:8123/ping",
	}
	if got := resolveProbeEndpoint(params); got != "http://clickhouse.xmc-metrics.svc.cluster.local:8123/ping" {
		t.Errorf("resolveProbeEndpoint() = %q, want healthCheckEndpoint", got)
	}
	if got := resolveConnectionEndpoint(params); got != "clickhouse.xmc-metrics.svc.cluster.local:9000" {
		t.Errorf("resolveConnectionEndpoint() = %q, want clickhouse.xmc-metrics.svc.cluster.local:9000", got)
	}
}
