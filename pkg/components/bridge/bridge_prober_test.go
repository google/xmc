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
	"testing"
	"time"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

func TestBridgeProber(t *testing.T) {
	fakeDetector := NewFakeStatusDetector()
	fakeDetector.SetStatus("xmc/my-crd", &ResourceStatus{Found: true, Ready: true, Endpoint: "http://my-crd.xmc:8080"})
	fakeProber := NewFakeHealthProber()
	fakeProber.SetResult("http://my-crd.xmc:8080", &HealthProbeResult{Healthy: true, Message: "HTTP 200"})

	bp := NewBridgeProber(fakeDetector, fakeProber, time.Second)
	ctx := context.Background()

	// DetectStatus
	st, err := bp.DetectStatus(ctx, WrapperRef{Name: "my-crd", Namespace: "xmc"})
	if err != nil || !st.Found || !st.Ready {
		t.Errorf("DetectStatus() = %+v, %v", st, err)
	}

	// Probe
	pr, err := bp.Probe(ctx, "http://my-crd.xmc:8080", time.Second)
	if err != nil || !pr.Healthy {
		t.Errorf("Probe() = %+v, %v", pr, err)
	}
	prZeroTimeout, err := bp.Probe(ctx, "http://my-crd.xmc:8080", 0)
	if err != nil || !prZeroTimeout.Healthy {
		t.Errorf("Probe() with zero timeout = %+v, %v", prZeroTimeout, err)
	}

	// ProbeHealth for 1P
	if ok, _ := bp.ProbeHealth(ctx, &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT}, "xmc"); !ok {
		t.Error("expected 1P component healthy")
	}

	// ProbeHealth for 3P
	spec3P := &componentspb.ComponentSpec{
		Type:       componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
		WrapperRef: &componentspb.WrapperRef{CrdGroup: "custom.io", CrdVersion: "v1", CrdKind: "Custom", TargetName: "my-crd", TargetNamespace: "xmc"},
	}
	if ok, msg := bp.ProbeHealth(ctx, spec3P, "xmc"); !ok || msg != "HTTP 200" {
		t.Errorf("expected 3P healthy: ok=%v, msg=%s", ok, msg)
	}

	// ProbeHealth with uninitialized detector
	emptyBP := &BridgeProber{}
	if ok, _ := emptyBP.ProbeHealth(ctx, spec3P, "xmc"); ok {
		t.Error("expected false for uninitialized BridgeProber")
	}
	if _, err := emptyBP.DetectStatus(ctx, WrapperRef{}); err == nil {
		t.Error("expected error for uninitialized detector")
	}
	if _, err := emptyBP.Probe(ctx, "http://foo", time.Second); err == nil {
		t.Error("expected error for uninitialized health prober")
	}

	// ProbeHealth with nil status returned from detector
	nilStatusDetector := &mockDetector{status: nil}
	bpWithNilStatus := NewBridgeProber(nilStatusDetector, fakeProber, time.Second)
	if ok, msg := bpWithNilStatus.ProbeHealth(ctx, spec3P, "xmc"); ok || msg != "status detector returned nil status" {
		t.Errorf("expected false for nil status from detector: ok=%v, msg=%s", ok, msg)
	}

	// ProbeHealth with nil result returned from prober
	nilResultProber := &mockProberDirect{probeRes: nil}
	bpWithNilResult := NewBridgeProber(fakeDetector, nilResultProber, time.Second)
	if ok, msg := bpWithNilResult.ProbeHealth(ctx, spec3P, "xmc"); ok || msg != "health prober returned nil result" {
		t.Errorf("expected false for nil result from prober: ok=%v, msg=%s", ok, msg)
	}

	// ProbeHealth for 3P without wrapperRef and without endpoint parameters must fail
	spec3PEmpty := &componentspb.ComponentSpec{
		Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
	}
	if ok, msg := bp.ProbeHealth(ctx, spec3PEmpty, "xmc"); ok || msg != "no wrapperRef or connection endpoint parameters configured" {
		t.Errorf("expected false for 3P without wrapperRef or endpoint parameters: ok=%v, msg=%s", ok, msg)
	}

	// ProbeHealth for 3P without wrapperRef with comma-prefixed / fallback endpoint parameters
	spec3PParams := &componentspb.ComponentSpec{
		Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
		Parameters: map[string]string{
			"healthCheckEndpoint": " , ",
			"clusterEndpoints":    " , http://my-crd.xmc:8080 , ",
		},
	}
	if ok, msg := bp.ProbeHealth(ctx, spec3PParams, "xmc"); !ok || msg != "HTTP 200" {
		t.Errorf("expected true for 3P with endpoint parameters: ok=%v, msg=%s", ok, msg)
	}
}

func TestBridgeProber_ProbeHealthMessageFallbacks(t *testing.T) {
	const endpoint = "http://my-crd.xmc:8080"
	detector := NewFakeStatusDetector()
	detector.SetStatus("xmc/my-crd", &ResourceStatus{Found: true, Ready: true, Endpoint: endpoint, Message: "CR is ready"})
	crSpec := &componentspb.ComponentSpec{
		Type:       componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
		WrapperRef: &componentspb.WrapperRef{CrdGroup: "custom.io", CrdVersion: "v1", CrdKind: "Custom", TargetName: "my-crd", TargetNamespace: "xmc"},
	}
	operatorlessSpec := &componentspb.ComponentSpec{
		Type:       componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
		Parameters: map[string]string{"endpoint": endpoint},
	}

	tests := []struct {
		name     string
		spec     *componentspb.ComponentSpec
		probeRes *HealthProbeResult
		probeErr error
		wantOK   bool
		wantMsg  string
	}{
		{name: "CR probe error", spec: crSpec, probeErr: errors.New("connection refused"), wantMsg: "health probe error: connection refused"},
		{name: "CR unhealthy without message", spec: crSpec, probeRes: &HealthProbeResult{}, wantMsg: "health probe failed"},
		{name: "CR unhealthy with message", spec: crSpec, probeRes: &HealthProbeResult{Message: "HTTP 503"}, wantMsg: "HTTP 503"},
		{name: "CR healthy without message", spec: crSpec, probeRes: &HealthProbeResult{Healthy: true}, wantOK: true, wantMsg: "CR is ready"},
		{name: "operator-less unhealthy without message", spec: operatorlessSpec, probeRes: &HealthProbeResult{}, wantMsg: "health probe failed"},
		{name: "operator-less healthy without message", spec: operatorlessSpec, probeRes: &HealthProbeResult{Healthy: true}, wantOK: true, wantMsg: "Component healthy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bp := NewBridgeProber(detector, &mockProberDirect{probeRes: tt.probeRes, err: tt.probeErr}, time.Second)
			ok, msg := bp.ProbeHealth(context.Background(), tt.spec, "xmc")
			if ok != tt.wantOK || msg != tt.wantMsg {
				t.Errorf("ProbeHealth() = (%v, %q), want (%v, %q)", ok, msg, tt.wantOK, tt.wantMsg)
			}
		})
	}
}

type mockDetector struct {
	status *ResourceStatus
	err    error
}

func (m *mockDetector) DetectStatus(ctx context.Context, ref WrapperRef) (*ResourceStatus, error) {
	return m.status, m.err
}

type mockProberDirect struct {
	probeRes *HealthProbeResult
	err      error
}

func (m *mockProberDirect) Probe(ctx context.Context, target string, timeout time.Duration) (*HealthProbeResult, error) {
	return m.probeRes, m.err
}
func (m *mockProberDirect) ProbeHTTP(ctx context.Context, targetURL string, timeout time.Duration) (*HealthProbeResult, error) {
	return m.probeRes, m.err
}
func (m *mockProberDirect) ProbeTCP(ctx context.Context, address string, timeout time.Duration) (*HealthProbeResult, error) {
	return m.probeRes, m.err
}
