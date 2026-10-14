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

	"google.golang.org/grpc/codes"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/store"
)

const (
	retryTestLocalUID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	retryTestOwnerUID = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
)

// sourceCallRecorder replays scripted CMS errors (nil entries and calls past the script succeed)
// and records the ExpectedOwnerUid of each call made by callWithOwnership.
type sourceCallRecorder struct {
	script         []error
	expectedOwners []string
}

func (s *sourceCallRecorder) call(_ context.Context, src *componentspb.RequestSource) (string, error) {
	i := len(s.expectedOwners)
	s.expectedOwners = append(s.expectedOwners, src.GetExpectedOwnerUid())
	if i < len(s.script) && s.script[i] != nil {
		return "", s.script[i]
	}
	return "ok", nil
}

func TestApplyIntentResponse(t *testing.T) {
	enabled := componentspb.ComponentState_COMPONENT_STATE_ENABLED
	pending := componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
	tests := []struct {
		name    string
		resp    *componentspb.SetComponentIntentResponse
		wantRes ReconcileResult
	}{
		{name: "nil response", wantRes: ReconcileResult{State: pending, ComponentGeneration: 2}},
		{name: "no component", resp: &componentspb.SetComponentIntentResponse{}, wantRes: ReconcileResult{State: pending, ComponentGeneration: 2}},
		{name: "status and generation", resp: &componentspb.SetComponentIntentResponse{Component: &componentspb.Component{
			Spec:   &componentspb.ComponentSpec{Generation: 5},
			Status: &componentspb.ComponentAccessStatus{State: enabled},
		}}, wantRes: ReconcileResult{State: enabled, ComponentGeneration: 5}},
		{name: "unknown generation keeps previous", resp: &componentspb.SetComponentIntentResponse{Component: &componentspb.Component{
			Status: &componentspb.ComponentAccessStatus{State: enabled},
		}}, wantRes: ReconcileResult{State: enabled, ComponentGeneration: 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := ReconcileResult{State: pending, ComponentGeneration: 2}
			applyIntentResponse(&res, tt.resp)
			if res.State != tt.wantRes.State || res.ComponentGeneration != tt.wantRes.ComponentGeneration {
				t.Errorf("applyIntentResponse() = (state %v, componentGeneration %d), want (%v, %d)", res.State, res.ComponentGeneration, tt.wantRes.State, tt.wantRes.ComponentGeneration)
			}
		})
	}
}

func TestCallWithOwnership_WithoutFence(t *testing.T) {
	notDeclared := errorInfoStatus(t, codes.FailedPrecondition, store.ReasonClusterNotDeclared, store.ErrorInfoDomain, "epoch not declared")
	r, err := NewReconciler(&mockCMS{}, nil, nil)
	if err != nil {
		t.Fatalf("NewReconciler() error = %v", err)
	}
	rec := &sourceCallRecorder{script: []error{notDeclared}}
	in := ReconcileComponentInput{
		Namespace: "xmc", Name: "redpanda", Generation: 1, UID: retryTestLocalUID,
		Cluster: &componentspb.ClusterIdentity{Uid: testPrimaryClusterUID, Name: "primary", Epoch: 1},
	}

	_, err = callWithOwnership(context.Background(), r, in, rec.call)

	if !errors.Is(err, notDeclared) {
		t.Errorf("callWithOwnership() error = %v, want the CLUSTER_NOT_DECLARED rejection without a fence to redeclare", err)
	}
	if len(rec.expectedOwners) != 1 {
		t.Errorf("CMS calls = %d, want 1 (no retry without a fence)", len(rec.expectedOwners))
	}
}

func TestCallWithOwnership_Retries(t *testing.T) {
	notDeclared := errorInfoStatus(t, codes.FailedPrecondition, store.ReasonClusterNotDeclared, store.ErrorInfoDomain, "epoch not declared")
	notDeclaredNewerEpoch := errorInfoStatusWithMetadata(t, codes.FailedPrecondition, store.ReasonClusterNotDeclared, store.ErrorInfoDomain, "epoch not declared",
		map[string]string{"active_cluster_uid": testPrimaryClusterUID, "active_epoch": "1"})
	ownedBy := errorInfoStatusWithMetadata(t, codes.FailedPrecondition, store.ReasonSourceIncarnationMismatch, store.ErrorInfoDomain, "owned by another incarnation",
		map[string]string{"owner_uid": retryTestOwnerUID, "owner_cluster_uid": testPrimaryClusterUID, "owner_namespace": "xmc"})
	staleGen := errorInfoStatus(t, codes.FailedPrecondition, store.ReasonStaleGeneration, store.ErrorInfoDomain, "stale generation")
	declareErr := errors.New("declare unavailable")

	tests := []struct {
		name          string
		script        []error
		declareErr    error
		wantErrIs     error
		wantCalls     int
		wantDeclareCt int
	}{
		{name: "not declared then accepted", script: []error{notDeclared}, wantCalls: 2, wantDeclareCt: 1},
		{name: "not declared at a newer epoch then accepted", script: []error{notDeclaredNewerEpoch}, wantCalls: 2, wantDeclareCt: 1},
		{name: "not declared twice is returned", script: []error{notDeclared, notDeclared}, wantErrIs: notDeclared, wantCalls: 2, wantDeclareCt: 1},
		{name: "not declared and redeclare fails", script: []error{notDeclared}, declareErr: declareErr, wantErrIs: declareErr, wantCalls: 1, wantDeclareCt: 1},
		{name: "incarnation mismatch is returned", script: []error{ownedBy}, wantErrIs: ownedBy, wantCalls: 1},
		{name: "stale generation", script: []error{staleGen}, wantErrIs: errStaleSnapshot, wantCalls: 1},
		{name: "not declared then stale generation", script: []error{notDeclared, staleGen}, wantErrIs: errStaleSnapshot, wantCalls: 2, wantDeclareCt: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cms := &mockCMS{declareErr: tt.declareErr}
			fence, err := NewClusterFence(cms, &componentspb.ClusterIdentity{Uid: testPrimaryClusterUID, Name: "primary", Epoch: 1}, ClusterFenceOptions{})
			if err != nil {
				t.Fatalf("NewClusterFence() error = %v", err)
			}
			r, err := NewReconciler(cms, nil, nil, WithClusterFence(fence))
			if err != nil {
				t.Fatalf("NewReconciler() error = %v", err)
			}
			rec := &sourceCallRecorder{script: tt.script}
			in := ReconcileComponentInput{Namespace: "xmc", Name: "redpanda", Generation: 1, UID: retryTestLocalUID}

			_, err = callWithOwnership(context.Background(), r, in, rec.call)

			if tt.wantErrIs == nil && err != nil {
				t.Errorf("callWithOwnership() error = %v, want nil", err)
			}
			if tt.wantErrIs != nil && !errors.Is(err, tt.wantErrIs) {
				t.Errorf("callWithOwnership() error = %v, want errors.Is %v", err, tt.wantErrIs)
			}
			if len(rec.expectedOwners) != tt.wantCalls {
				t.Errorf("CMS calls = %d, want %d", len(rec.expectedOwners), tt.wantCalls)
			}
			if cms.declareCalls != tt.wantDeclareCt {
				t.Errorf("DeclareClusterEpoch calls = %d, want %d", cms.declareCalls, tt.wantDeclareCt)
			}
		})
	}
}
