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
	"testing"
	"time"

	"google.golang.org/grpc"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

type mockCMS struct {
	setErr, delErr, obsErr  error
	prepareErr, completeErr error
	state                   componentspb.ComponentState
	lastObsReq              *componentspb.UpdateObservedGenerationRequest
	setCalls                int
	prepareCalls            int
	completeCalls           int
}

func (m *mockCMS) SetComponentIntent(ctx context.Context, in *componentspb.SetComponentIntentRequest, opts ...grpc.CallOption) (*componentspb.SetComponentIntentResponse, error) {
	m.setCalls++
	if m.setErr != nil {
		return nil, m.setErr
	}
	s := m.state
	if s == componentspb.ComponentState_COMPONENT_STATE_UNSPECIFIED {
		s = componentspb.ComponentState_COMPONENT_STATE_ENABLED
	}
	return &componentspb.SetComponentIntentResponse{
		Component: &componentspb.Component{Name: in.GetName(), Status: &componentspb.ComponentAccessStatus{State: s}},
	}, nil
}

func (m *mockCMS) UpdateObservedGeneration(ctx context.Context, in *componentspb.UpdateObservedGenerationRequest, opts ...grpc.CallOption) (*componentspb.UpdateObservedGenerationResponse, error) {
	m.lastObsReq = in
	return &componentspb.UpdateObservedGenerationResponse{}, m.obsErr
}

func (m *mockCMS) ExecuteDeletion(ctx context.Context, in *componentspb.ExecuteDeletionRequest, opts ...grpc.CallOption) (*componentspb.ExecuteDeletionResponse, error) {
	return &componentspb.ExecuteDeletionResponse{State: componentspb.ComponentState_COMPONENT_STATE_DELETED}, m.delErr
}

func (m *mockCMS) PrepareDeactivation(ctx context.Context, in *componentspb.PrepareDeactivationRequest, opts ...grpc.CallOption) (*componentspb.PrepareDeactivationResponse, error) {
	m.prepareCalls++
	return &componentspb.PrepareDeactivationResponse{State: componentspb.ComponentState_COMPONENT_STATE_DISABLING}, m.prepareErr
}

func (m *mockCMS) CompleteDeactivation(ctx context.Context, in *componentspb.CompleteDeactivationRequest, opts ...grpc.CallOption) (*componentspb.CompleteDeactivationResponse, error) {
	m.completeCalls++
	return &componentspb.CompleteDeactivationResponse{State: componentspb.ComponentState_COMPONENT_STATE_DISABLED}, m.completeErr
}

type mockProber struct {
	healthy     bool
	msg         string
	resStatus   *ResourceStatus
	status      *ResourceStatus
	hasStatus   bool
	detectErr   error
	nilStatus   bool
	probeErr    error
	probeRes    *HealthProbeResult
	hasProbeRes bool
}

func (m *mockProber) DetectStatus(ctx context.Context, ref WrapperRef) (*ResourceStatus, error) {
	if m == nil {
		return &ResourceStatus{Found: true, Ready: true}, nil
	}
	if m.detectErr != nil {
		return nil, m.detectErr
	}
	if m.nilStatus {
		return nil, nil
	}
	if m.resStatus != nil {
		return m.resStatus, nil
	}
	if m.hasStatus || m.status != nil {
		return m.status, nil
	}
	if m.resStatus != nil {
		return m.resStatus, nil
	}
	return &ResourceStatus{Found: true, Ready: m.healthy, Message: m.msg}, nil
}

func (m *mockProber) Probe(ctx context.Context, target string, timeout time.Duration) (*HealthProbeResult, error) {
	if m == nil {
		return &HealthProbeResult{Healthy: true}, nil
	}
	if m.probeErr != nil {
		return nil, m.probeErr
	}
	if m.hasProbeRes || m.probeRes != nil {
		return m.probeRes, nil
	}
	return &HealthProbeResult{Healthy: true}, nil
}

func (m *mockProber) ProbeHealth(ctx context.Context, spec *componentspb.ComponentSpec, defaultNamespace string) (bool, string) {
	if m == nil {
		return true, "Component is active"
	}
	if m.msg != "" {
		return m.healthy, m.msg
	}
	return m.healthy, "Component is active"
}

type mockHelm struct {
	ensureErr, deleteErr, orphanErr error
	active                          bool
	purgeCalled                     bool
	orphanCalled                    bool
	deleteCalled                    bool
	orphaned                        bool
	ensureCalls                     int
	lastRef                         ComponentRef
	lastOpts                        HelmComponentOptions
	actuatedGenerations             map[string]int64
	// unhealthyReason, when non-empty, makes ComponentHealth report unhealthy with this reason. Defaults to healthy.
	unhealthyReason string
}

func (m *mockHelm) Ensure1PComponent(ctx context.Context, ref ComponentRef, opts HelmComponentOptions) error {
	if m == nil {
		return nil
	}
	m.ensureCalls++
	m.lastRef = ref
	m.lastOpts = opts
	if m.ensureErr == nil {
		m.active = true
	}
	return m.ensureErr
}

func (m *mockHelm) Delete1PComponent(ctx context.Context, ref ComponentRef) error {
	if m == nil {
		return nil
	}
	m.deleteCalled = true
	m.lastRef = ref
	if m.deleteErr == nil {
		m.active = false
	}
	return m.deleteErr
}

func (m *mockHelm) Purge1PComponent(ctx context.Context, ref ComponentRef) error {
	if m == nil {
		return nil
	}
	m.purgeCalled = true
	m.lastRef = ref
	if m.deleteErr == nil {
		m.active = false
	}
	return m.deleteErr
}

func (m *mockHelm) Orphan1PComponent(ctx context.Context, ref ComponentRef) error {
	if m == nil {
		return nil
	}
	m.orphanCalled = true
	m.lastRef = ref
	if m.orphanErr == nil {
		m.orphaned = true
		m.active = false
	}
	return m.orphanErr
}

func (m *mockHelm) IsComponentActive(ref ComponentRef) bool {
	if m == nil {
		return false
	}
	return m.active
}

func (m *mockHelm) IsComponentActuated(ref ComponentRef, generation int64) bool {
	if m == nil || m.actuatedGenerations == nil {
		return false
	}
	return m.active && m.actuatedGenerations[ref.String()] == generation
}

func (m *mockHelm) RecordActuation(ref ComponentRef, generation int64) {
	if m == nil {
		return
	}
	if m.actuatedGenerations == nil {
		m.actuatedGenerations = make(map[string]int64)
	}
	m.actuatedGenerations[ref.String()] = generation
}

func (m *mockHelm) HydrateComponentState(ctx context.Context, ref ComponentRef, observedGeneration int64, crReady bool) error {
	if m == nil || !crReady {
		return nil
	}
	m.active = true
	if observedGeneration <= 0 {
		return nil
	}
	if m.actuatedGenerations == nil {
		m.actuatedGenerations = make(map[string]int64)
	}
	if m.actuatedGenerations[ref.String()] == 0 {
		m.actuatedGenerations[ref.String()] = observedGeneration
	}
	return nil
}

func (m *mockHelm) ComponentHealth(ctx context.Context, ref ComponentRef) (bool, string, string) {
	if m.unhealthyReason != "" {
		return false, m.unhealthyReason, "mock Helm release unhealthy"
	}
	return true, ReasonComponentHealthy, "mock Helm release deployed"
}

// gateOutcome summarizes the observable effects of a reconciliation pass for gating tests.
type gateOutcome struct {
	State               componentspb.ComponentState
	ReadyStatus         string
	ReadyReason         string
	DegradedStatus      string
	DependenciesReady   string
	DeactivationBlocked string
	SetCalls            int
	EnsureCalls         int
	PrepareCalls        int
	CompleteCalls       int
	HelmDeleted         bool
}

func summarizeGate(res *ReconcileResult, cms *mockCMS, helm *mockHelm) gateOutcome {
	got := gateOutcome{
		State: res.State, SetCalls: cms.setCalls, PrepareCalls: cms.prepareCalls, CompleteCalls: cms.completeCalls,
		EnsureCalls: helm.ensureCalls, HelmDeleted: helm.deleteCalled,
	}
	if c := findCondition(res.Conditions, ConditionTypeReady); c != nil {
		got.ReadyStatus, got.ReadyReason = c.Status, c.Reason
	}
	if c := findCondition(res.Conditions, ConditionTypeDegraded); c != nil {
		got.DegradedStatus = c.Status
	}
	if c := findCondition(res.Conditions, ConditionTypeDependenciesReady); c != nil {
		got.DependenciesReady = c.Status
	}
	if c := findCondition(res.Conditions, ConditionTypeDeactivationBlocked); c != nil {
		got.DeactivationBlocked = c.Status
	}
	return got
}

// checkReadyMessage fails the test if want is non-empty and differs from the Ready message.
func checkReadyMessage(t *testing.T, res *ReconcileResult, want string) {
	t.Helper()
	if want == "" {
		return
	}
	if c := findCondition(res.Conditions, ConditionTypeReady); c == nil || c.Message != want {
		t.Errorf("Ready condition = %+v, want message %q", c, want)
	}
}
