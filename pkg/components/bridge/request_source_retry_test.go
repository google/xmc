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

// Production target: reconciler.go (callWithOwnership and applyIntentResponse).
//
// Test type: Hermetic unit test suite in package bridge using a fake
// sourceCallRecorder to script CMS ErrorInfo responses without a gRPC server
// or Kubernetes API server.
//
// Why separate from lifecycle_test.go: Isolates the RequestSource retry state
// machine (CLUSTER_NOT_DECLARED auto-declaration, STALE_GENERATION handling,
// and SOURCE_INCARNATION_MISMATCH live-UID takeover) from the end-to-end
// Reconciler lifecycle tests in lifecycle_test.go.
package bridge

import (
	"context"
	"errors"
	"maps"
	"testing"

	"github.com/google/go-cmp/cmp"
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
			if diff := cmp.Diff(tt.wantRes, res); diff != "" {
				t.Errorf("applyIntentResponse() mismatch (-want +got):\n%s", diff)
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
	ownedBy := func(meta map[string]string) error {
		return errorInfoStatusWithMetadata(t, codes.FailedPrecondition, store.ReasonSourceIncarnationMismatch, store.ErrorInfoDomain, "owned by another incarnation", meta)
	}
	sameClusterOwner := map[string]string{"owner_uid": retryTestOwnerUID, "owner_cluster_uid": testPrimaryClusterUID, "owner_namespace": "xmc"}
	withMeta := func(key, value string) map[string]string {
		m := maps.Clone(sameClusterOwner)
		m[key] = value
		return m
	}
	notDeclared := errorInfoStatus(t, codes.FailedPrecondition, store.ReasonClusterNotDeclared, store.ErrorInfoDomain, "epoch not declared")
	notDeclaredNewerEpoch := errorInfoStatusWithMetadata(t, codes.FailedPrecondition, store.ReasonClusterNotDeclared, store.ErrorInfoDomain, "epoch not declared",
		map[string]string{"active_cluster_uid": testPrimaryClusterUID, "active_epoch": "1"})
	ownerGone := ownedBy(nil)
	staleGen := errorInfoStatus(t, codes.FailedPrecondition, store.ReasonStaleGeneration, store.ErrorInfoDomain, "stale generation")
	declareErr := errors.New("declare unavailable")
	readerErr := errors.New("api server unavailable")
	liveIs := func(uid string, err error) LiveUIDReader {
		return func(context.Context, string, string) (string, error) { return uid, err }
	}

	tests := []struct {
		name          string
		script        []error
		reader        LiveUIDReader
		declareErr    error
		wantErrIs     error
		wantErr       bool
		wantOwners    []string
		wantDeclareCt int
	}{
		{name: "not declared then accepted", script: []error{notDeclared}, wantOwners: []string{"", ""}, wantDeclareCt: 1},
		{name: "not declared at a newer epoch then accepted", script: []error{notDeclaredNewerEpoch}, wantOwners: []string{"", ""}, wantDeclareCt: 1},
		{name: "not declared twice is returned", script: []error{notDeclared, notDeclared}, wantErrIs: notDeclared, wantOwners: []string{"", ""}, wantDeclareCt: 1},
		{name: "not declared and redeclare fails", script: []error{notDeclared}, declareErr: declareErr, wantErrIs: declareErr, wantOwners: []string{""}, wantDeclareCt: 1},
		{name: "not declared then same-cluster takeover", script: []error{notDeclared, ownedBy(sameClusterOwner)}, reader: liveIs(retryTestLocalUID, nil), wantOwners: []string{"", "", retryTestOwnerUID}, wantDeclareCt: 1},
		{name: "same-cluster takeover", script: []error{ownedBy(sameClusterOwner)}, reader: liveIs(retryTestLocalUID, nil), wantOwners: []string{"", retryTestOwnerUID}},
		{name: "owner deleted before takeover retries without expected owner", script: []error{ownedBy(sameClusterOwner), ownerGone}, reader: liveIs(retryTestLocalUID, nil), wantOwners: []string{"", retryTestOwnerUID, ""}},
		{name: "owner deleted then plain retry rejected", script: []error{ownedBy(sameClusterOwner), ownerGone, ownedBy(sameClusterOwner)}, reader: liveIs(retryTestLocalUID, nil), wantErr: true, wantOwners: []string{"", retryTestOwnerUID, ""}},
		{name: "takeover rejected with an owner is returned", script: []error{ownedBy(sameClusterOwner), ownedBy(withMeta("owner_uid", "cccccccc-cccc-cccc-cccc-cccccccccccc"))}, reader: liveIs(retryTestLocalUID, nil), wantErr: true, wantOwners: []string{"", retryTestOwnerUID}},
		{name: "no live UID reader", script: []error{ownedBy(sameClusterOwner)}, wantErr: true, wantOwners: []string{""}},
		{name: "owner UID missing", script: []error{ownedBy(withMeta("owner_uid", ""))}, reader: liveIs(retryTestLocalUID, nil), wantErr: true, wantOwners: []string{""}},
		{name: "owner in another cluster", script: []error{ownedBy(withMeta("owner_cluster_uid", "22222222-2222-2222-2222-222222222222"))}, reader: liveIs(retryTestLocalUID, nil), wantErr: true, wantOwners: []string{""}},
		{name: "owner in another namespace", script: []error{ownedBy(withMeta("owner_namespace", "team-b"))}, reader: liveIs(retryTestLocalUID, nil), wantErr: true, wantOwners: []string{""}},
		{name: "live UID read fails", script: []error{ownedBy(sameClusterOwner)}, reader: liveIs("", readerErr), wantErrIs: readerErr, wantOwners: []string{""}},
		{name: "local incarnation is stale", script: []error{ownedBy(sameClusterOwner)}, reader: liveIs(retryTestOwnerUID, nil), wantErrIs: errStaleLocalIncarnation, wantOwners: []string{""}},
		{name: "stale generation without live UID reader", script: []error{staleGen}, wantErrIs: errStaleSnapshot, wantOwners: []string{""}},
		{name: "stale generation of the live incarnation", script: []error{staleGen}, reader: liveIs(retryTestLocalUID, nil), wantErrIs: errStaleSnapshot, wantOwners: []string{""}},
		{name: "stale generation of a replaced incarnation", script: []error{staleGen}, reader: liveIs(retryTestOwnerUID, nil), wantErrIs: errStaleLocalIncarnation, wantOwners: []string{""}},
		{name: "stale generation and live UID read fails", script: []error{staleGen}, reader: liveIs("", readerErr), wantErrIs: readerErr, wantOwners: []string{""}},
		{name: "not declared then stale generation", script: []error{notDeclared, staleGen}, reader: liveIs(retryTestLocalUID, nil), wantErrIs: errStaleSnapshot, wantOwners: []string{"", ""}, wantDeclareCt: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cms := &mockCMS{declareErr: tt.declareErr}
			fence, err := NewClusterFence(cms, &componentspb.ClusterIdentity{Uid: testPrimaryClusterUID, Name: "primary", Epoch: 1}, ClusterFenceOptions{})
			if err != nil {
				t.Fatalf("NewClusterFence() error = %v", err)
			}
			opts := []ReconcilerOption{WithClusterFence(fence)}
			if tt.reader != nil {
				opts = append(opts, WithLiveUIDReader(tt.reader))
			}
			r, err := NewReconciler(cms, nil, nil, opts...)
			if err != nil {
				t.Fatalf("NewReconciler() error = %v", err)
			}
			rec := &sourceCallRecorder{script: tt.script}
			in := ReconcileComponentInput{Namespace: "xmc", Name: "redpanda", Generation: 1, UID: retryTestLocalUID}

			_, err = callWithOwnership(context.Background(), r, in, rec.call)

			switch {
			case tt.wantErrIs != nil:
				if !errors.Is(err, tt.wantErrIs) {
					t.Errorf("callWithOwnership() error = %v, want errors.Is %v", err, tt.wantErrIs)
				}
			case tt.wantErr:
				if err == nil {
					t.Error("callWithOwnership() error = nil, want the original rejection")
				}
			default:
				if err != nil {
					t.Errorf("callWithOwnership() error = %v, want nil", err)
				}
			}
			if diff := cmp.Diff(tt.wantOwners, rec.expectedOwners); diff != "" {
				t.Errorf("ExpectedOwnerUid per call mismatch (-want +got):\n%s", diff)
			}
			if cms.declareCalls != tt.wantDeclareCt {
				t.Errorf("DeclareClusterEpoch calls = %d, want %d", cms.declareCalls, tt.wantDeclareCt)
			}
		})
	}
}
