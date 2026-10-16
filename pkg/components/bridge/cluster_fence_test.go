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
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/pkg/components/cms"
	"xmanager_cloud/pkg/components/store"
)

func TestReadClusterUID(t *testing.T) {
	tests := []struct {
		name    string
		client  kubernetes.Interface
		wantUID string
		wantErr bool
	}{
		{name: "nil client", client: nil, wantErr: true},
		{name: "missing kube-system", client: fake.NewSimpleClientset(), wantErr: true},
		{
			name:    "empty UID",
			client:  fake.NewSimpleClientset(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kube-system"}}),
			wantErr: true,
		},
		{
			name: "valid kube-system UID",
			client: fake.NewSimpleClientset(&corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: "kube-system", UID: types.UID("cluster-uid-123")},
			}),
			wantUID: "cluster-uid-123",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ReadClusterUID(context.Background(), tt.client)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ReadClusterUID() = (%q, nil), want error", got)
				}
				return
			}
			if err != nil || got != tt.wantUID {
				t.Fatalf("ReadClusterUID() = (%q, %v), want (%q, nil)", got, err, tt.wantUID)
			}
		})
	}
}

func TestNewClusterFence_Validation(t *testing.T) {
	valid := &componentspb.ClusterIdentity{Uid: "uid-1", Name: "prod", Epoch: 1}
	tests := []struct {
		name string
		cms  CMSClient
		id   *componentspb.ClusterIdentity
	}{
		{name: "nil CMS client", cms: nil, id: valid},
		{name: "nil identity", cms: &mockCMS{}, id: nil},
		{name: "missing UID", cms: &mockCMS{}, id: &componentspb.ClusterIdentity{Name: "prod", Epoch: 1}},
		{name: "missing name", cms: &mockCMS{}, id: &componentspb.ClusterIdentity{Uid: "uid-1", Epoch: 1}},
		{name: "invalid DNS label name", cms: &mockCMS{}, id: &componentspb.ClusterIdentity{Uid: "uid-1", Name: "INVALID_DNS_LABEL", Epoch: 1}},
		{name: "zero epoch", cms: &mockCMS{}, id: &componentspb.ClusterIdentity{Uid: "uid-1", Name: "prod", Epoch: 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewClusterFence(tt.cms, tt.id, ClusterFenceOptions{}); err == nil {
				t.Errorf("NewClusterFence(%+v) = nil, want error", tt.id)
			}
		})
	}
}

const (
	testPrimaryClusterUID = "11111111-1111-1111-1111-111111111111"
	testStandbyClusterUID = "22222222-2222-2222-2222-222222222222"
)

func TestReconciler_ClusterFenceColdStandby(t *testing.T) {
	ctx := context.Background()
	cmsStore := store.NewMemoryStore()
	cmsClient := NewCMSClientAdapter(cms.NewServer(cmsStore, cms.WithAuthorizer(cms.NewDisabledAuthorizer())))

	// Primary cluster claims epoch 2.
	if _, err := cmsClient.DeclareClusterEpoch(ctx, &componentspb.DeclareClusterEpochRequest{
		Cluster: &componentspb.ClusterIdentity{Uid: testPrimaryClusterUID, Name: "primary-cluster", Epoch: 2},
	}); err != nil {
		t.Fatalf("primary DeclareClusterEpoch() failed: %v", err)
	}

	tests := []struct {
		name         string
		identity     *componentspb.ClusterIdentity
		enabled      bool
		wantConflict string
	}{
		{
			name:         "fenced standby cluster on enabled component",
			identity:     &componentspb.ClusterIdentity{Uid: testStandbyClusterUID, Name: "standby-cluster", Epoch: 2},
			enabled:      true,
			wantConflict: ReasonClusterFenced,
		},
		{
			name:         "fenced standby cluster on disabled component",
			identity:     &componentspb.ClusterIdentity{Uid: testStandbyClusterUID, Name: "standby-cluster", Epoch: 2},
			enabled:      false,
			wantConflict: ReasonClusterFenced,
		},
		{
			name:         "regressed epoch on primary cluster",
			identity:     &componentspb.ClusterIdentity{Uid: testPrimaryClusterUID, Name: "primary-cluster", Epoch: 1},
			enabled:      true,
			wantConflict: ReasonClusterEpochRegressed,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			helm := &mockHelm{}
			fence, err := NewClusterFence(cmsClient, tc.identity, ClusterFenceOptions{})
			if err != nil {
				t.Fatalf("NewClusterFence() failed: %v", err)
			}
			r, err := NewReconciler(cmsClient, &mockProber{healthy: true}, helm, WithClusterFence(fence))
			if err != nil {
				t.Fatalf("NewReconciler() failed: %v", err)
			}
			res, err := r.ReconcileComponent(ctx, ReconcileComponentInput{
				Namespace:      "xmc",
				Name:           "redpanda",
				Generation:     3,
				Enabled:        tc.enabled,
				ExistingAccess: &AccessStatus{EnvVars: map[string]string{"PORT": "9092"}},
				Spec: &componentspb.ComponentSpec{
					Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
					Version: "1.0.0",
				},
			})
			if err != nil {
				t.Fatalf("ReconcileComponent() unexpected error: %v", err)
			}
			if helm.ensureCalls != 0 || helm.deleteCalled {
				t.Errorf("Helm actuation while fenced: ensureCalls=%d deleteCalled=%v, want 0/false", helm.ensureCalls, helm.deleteCalled)
			}
			recs, err := cmsStore.ListComponents(ctx)
			if err != nil || len(recs) != 0 {
				t.Errorf("CMS records created while fenced = %v (err=%v), want none", recs, err)
			}
			if res.ObservedGeneration != 3 {
				t.Errorf("ObservedGeneration = %d, want 3", res.ObservedGeneration)
			}
			if res.Access != nil {
				t.Errorf("Access = %+v, want nil when fenced", res.Access)
			}
			if res.RequeueAfter != conflictRecheckInterval {
				t.Errorf("RequeueAfter = %v, want %v", res.RequeueAfter, conflictRecheckInterval)
			}
			assertSourceConflictConditions(t, res.Conditions, tc.wantConflict)
		})
	}
}

func TestReconciler_ClusterFenceTransientError(t *testing.T) {
	ctx := context.Background()
	cmsMock := &mockCMS{declareErr: status.Error(codes.Unavailable, "cms unreachable")}
	fence, err := NewClusterFence(cmsMock, &componentspb.ClusterIdentity{Uid: testPrimaryClusterUID, Name: "prod", Epoch: 1}, ClusterFenceOptions{})
	if err != nil {
		t.Fatalf("NewClusterFence() failed: %v", err)
	}
	r, err := NewReconciler(cmsMock, &mockProber{healthy: true}, &mockHelm{}, WithClusterFence(fence))
	if err != nil {
		t.Fatalf("NewReconciler() failed: %v", err)
	}
	res, err := r.ReconcileComponent(ctx, ReconcileComponentInput{
		Namespace:      "xmc",
		Name:           "redpanda",
		Generation:     2,
		Enabled:        true,
		ExistingAccess: &AccessStatus{EnvVars: map[string]string{"PORT": "9092"}},
		ExistingConditions: []Condition{{
			Type: ConditionTypeReady, Status: ConditionStatusTrue, ObservedGeneration: 1, Reason: ReasonComponentHealthy,
		}},
		Spec: &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, Version: "1.0.0"},
	})
	if err == nil {
		t.Fatal("ReconcileComponent() = nil, want transient declaration error")
	}
	if synced := findCondition(res.Conditions, ConditionTypeSynced); synced == nil || synced.Status != ConditionStatusFalse || synced.Reason != ReasonCMSSyncFailed {
		t.Errorf("Synced condition = %+v, want False/CMSSyncFailed", synced)
	}
	if ready := findCondition(res.Conditions, ConditionTypeReady); ready == nil || ready.Status != ConditionStatusTrue {
		t.Errorf("Ready condition = %+v, want preserved True", ready)
	}
	if res.Access == nil || res.Access.EnvVars["PORT"] != "9092" {
		t.Errorf("Access = %+v, want preserved PORT=9092", res.Access)
	}

	cmsMock.declareErr = nil
	if err := fence.Declare(ctx); err != nil {
		t.Fatalf("fence.Declare() after transient recovery error = %v", err)
	}
	select {
	case <-fence.Changes():
	default:
		t.Error("expected notification on fence.Changes() when initial Declare recovers from transient error")
	}
}

// TestReconciler_FencedThenUnfencedDisabledUninstalls checks that a component whose workload ran
// before the fence engaged is still uninstalled when it is disabled after unfencing, even with cold
// in-memory Helm state (for example after a bridge restart while fenced).
func TestReconciler_FencedThenUnfencedDisabledUninstalls(t *testing.T) {
	ctx := context.Background()
	cmsMock := &mockCMS{declareErr: fenceRejection(t, store.ReasonClusterFenced)}
	fence, err := NewClusterFence(cmsMock, &componentspb.ClusterIdentity{Uid: testPrimaryClusterUID, Name: "prod", Epoch: 1}, ClusterFenceOptions{})
	if err != nil {
		t.Fatalf("NewClusterFence() failed: %v", err)
	}
	helm := &mockHelm{active: false}
	r, err := NewReconciler(cmsMock, &mockProber{healthy: true}, helm, WithClusterFence(fence))
	if err != nil {
		t.Fatalf("NewReconciler() failed: %v", err)
	}
	in := ReconcileComponentInput{
		Namespace:  "xmc",
		Name:       "redpanda",
		Generation: 1,
		Enabled:    true,
		ExistingConditions: []Condition{{
			Type: ConditionTypeReady, Status: ConditionStatusTrue, ObservedGeneration: 1, Reason: ReasonComponentHealthy,
		}},
		Spec: &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, Version: "1.0.0"},
	}
	fencedRes, err := r.ReconcileComponent(ctx, in)
	if err != nil {
		t.Fatalf("fenced ReconcileComponent() error = %v", err)
	}
	assertSourceConflictConditions(t, fencedRes.Conditions, ReasonClusterFenced)

	cmsMock.declareErr = nil
	if err := fence.Declare(ctx); err != nil {
		t.Fatalf("fence.Declare() after unfencing = %v", err)
	}
	in.Generation, in.Enabled, in.ExistingConditions = 2, false, fencedRes.Conditions
	res, err := r.ReconcileComponent(ctx, in)
	if err != nil {
		t.Fatalf("disabled ReconcileComponent() after unfencing error = %v", err)
	}
	if !helm.deleteCalled {
		t.Error("Delete1PComponent not called: a workload running before the fence must be uninstalled when disabled after unfencing")
	}
	if res.State != componentspb.ComponentState_COMPONENT_STATE_DISABLED {
		t.Errorf("State = %v, want COMPONENT_STATE_DISABLED", res.State)
	}
}

func TestClusterFence_StartAndTransitionNotification(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmsStore := store.NewMemoryStore()
	cmsClient := NewCMSClientAdapter(cms.NewServer(cmsStore, cms.WithAuthorizer(cms.NewDisabledAuthorizer())))
	if _, err := cmsClient.DeclareClusterEpoch(ctx, &componentspb.DeclareClusterEpochRequest{
		Cluster: &componentspb.ClusterIdentity{Uid: testPrimaryClusterUID, Name: "primary", Epoch: 2},
	}); err != nil {
		t.Fatalf("primary DeclareClusterEpoch() failed: %v", err)
	}

	fence, err := NewClusterFence(cmsClient, &componentspb.ClusterIdentity{
		Uid: testStandbyClusterUID, Name: "standby", Epoch: 2,
	}, ClusterFenceOptions{HeartbeatInterval: 20 * time.Millisecond, RetryInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatalf("NewClusterFence() failed: %v", err)
	}
	if !fence.NeedLeaderElection() {
		t.Error("NeedLeaderElection() = false, want true")
	}

	done := make(chan error, 1)
	go func() { done <- fence.Start(ctx) }()

	select {
	case <-fence.Changes():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial fenced notification on Changes()")
	}
	reason, _, err := fence.Check(ctx)
	if err != nil || reason != ReasonClusterFenced {
		t.Fatalf("fence.Check() = (%q, %v), want (%q, nil)", reason, err, ReasonClusterFenced)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("fence.Start() returned error: %v", err)
	}
}

func fenceRejection(t *testing.T, reason string) error {
	t.Helper()
	st, err := status.New(codes.FailedPrecondition, "rejected by test fence").WithDetails(&errdetails.ErrorInfo{Reason: reason, Domain: store.ErrorInfoDomain})
	if err != nil {
		t.Fatalf("status.WithDetails() failed: %v", err)
	}
	return st.Err()
}

func TestFenceConflictFromError_WrappedKeepsCMSMessage(t *testing.T) {
	err := fmt.Errorf("failed to declare: %w", fenceRejection(t, store.ReasonClusterFenced))
	reason, msg, fenced := fenceConflictFromError(err)
	if !fenced || reason != ReasonClusterFenced || msg != "rejected by test fence" {
		t.Errorf("fenceConflictFromError() = (%q, %q, %t), want (%q, %q, true)", reason, msg, fenced, ReasonClusterFenced, "rejected by test fence")
	}
}

func TestClusterFence_TransitionNotifications(t *testing.T) {
	ctx := context.Background()
	fenced := fenceRejection(t, store.ReasonClusterFenced)
	regressed := fenceRejection(t, store.ReasonClusterEpochRegressed)
	tests := []struct {
		name          string
		first, second error
		wantReason    string
		wantNotify    bool
		// wantLog is a fragment of the one log line the second declaration writes; empty means none.
		wantLog string
	}{
		{name: "active to fenced", first: nil, second: fenced, wantReason: ReasonClusterFenced, wantNotify: true, wantLog: "level=ERROR msg=\"Cluster is fenced out of CMS"},
		{name: "fenced to active", first: fenced, second: nil, wantReason: "", wantNotify: true, wantLog: "level=INFO msg=\"Cluster holds the CMS ownership fence\""},
		{name: "fenced to regressed", first: fenced, second: regressed, wantReason: ReasonClusterEpochRegressed, wantNotify: true, wantLog: "reason=" + ReasonClusterEpochRegressed},
		{name: "active unchanged", first: nil, second: nil, wantReason: "", wantNotify: false},
		{name: "fenced unchanged", first: fenced, second: fenced, wantReason: ReasonClusterFenced, wantNotify: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmsMock := &mockCMS{declareErr: tc.first}
			fence, err := NewClusterFence(cmsMock, &componentspb.ClusterIdentity{Uid: testPrimaryClusterUID, Name: "prod", Epoch: 1}, ClusterFenceOptions{})
			if err != nil {
				t.Fatalf("NewClusterFence() failed: %v", err)
			}
			if err := fence.Declare(ctx); err != nil {
				t.Fatalf("first Declare() = %v, want nil", err)
			}
			select {
			case <-fence.Changes():
			default:
			}

			cmsMock.declareErr = tc.second
			var logs bytes.Buffer
			logCtx := logging.WithLogger(ctx, slog.New(slog.NewTextHandler(&logs, nil)))
			if err := fence.Declare(logCtx); err != nil {
				t.Fatalf("second Declare() = %v, want nil", err)
			}
			switch got := logs.String(); {
			case tc.wantLog == "" && got != "":
				t.Errorf("second Declare() logged %q, want no log", got)
			case tc.wantLog != "" && (strings.Count(got, "msg=") != 1 || !strings.Contains(got, tc.wantLog)):
				t.Errorf("second Declare() logged %q, want one line containing %q", got, tc.wantLog)
			}
			gotNotify := false
			select {
			case <-fence.Changes():
				gotNotify = true
			default:
			}
			if gotNotify != tc.wantNotify {
				t.Errorf("Changes() notified = %v, want %v", gotNotify, tc.wantNotify)
			}
			if reason, _, err := fence.Check(ctx); err != nil || reason != tc.wantReason {
				t.Errorf("Check() = (%q, %v), want (%q, nil)", reason, err, tc.wantReason)
			}
		})
	}
}

// hungDeclareCMS blocks DeclareClusterEpoch calls until their context ends once hang is set;
// before that, declarations succeed.
type hungDeclareCMS struct {
	mockCMS
	hang  atomic.Bool
	calls atomic.Int32
}

func (h *hungDeclareCMS) DeclareClusterEpoch(ctx context.Context, in *componentspb.DeclareClusterEpochRequest, _ ...grpc.CallOption) (*componentspb.DeclareClusterEpochResponse, error) {
	h.calls.Add(1)
	if !h.hang.Load() {
		return &componentspb.DeclareClusterEpochResponse{ActiveCluster: in.GetCluster()}, nil
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestClusterFence_StartBoundsHungDeclare(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmsHung := &hungDeclareCMS{}
	fence, err := NewClusterFence(cmsHung, &componentspb.ClusterIdentity{Uid: testPrimaryClusterUID, Name: "prod", Epoch: 1}, ClusterFenceOptions{
		HeartbeatInterval: time.Hour, RetryInterval: 5 * time.Millisecond, DeclareTimeout: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewClusterFence() failed: %v", err)
	}
	if err := fence.Declare(ctx); err != nil {
		t.Fatalf("initial Declare() = %v, want nil", err)
	}
	cmsHung.hang.Store(true)
	done := make(chan error, 1)
	go func() { done <- fence.Start(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	for cmsHung.calls.Load() < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("DeclareClusterEpoch calls = %d after 5s, want >= 3 (hung declaration not bounded)", cmsHung.calls.Load())
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Check must answer from the last applied status without waiting on the in-flight CMS call.
	checkCtx, checkCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer checkCancel()
	if reason, _, err := fence.Check(checkCtx); err != nil || reason != "" {
		t.Errorf("Check() during hung heartbeat = (%q, %v), want (\"\", nil)", reason, err)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("fence.Start() returned error: %v", err)
	}
}

// TestClusterFence_RetryDelay checks that a fenced or regressed declaration, which CMS keeps
// rejecting until the epoch is raised, waits the heartbeat interval, while transient failures are
// retried quickly.
func TestClusterFence_RetryDelay(t *testing.T) {
	const heartbeat, retry = time.Hour, time.Second
	fence, err := NewClusterFence(&mockCMS{}, &componentspb.ClusterIdentity{Uid: testPrimaryClusterUID, Name: "prod", Epoch: 1},
		ClusterFenceOptions{HeartbeatInterval: heartbeat, RetryInterval: retry})
	if err != nil {
		t.Fatalf("NewClusterFence() failed: %v", err)
	}
	fenced := status.Error(codes.FailedPrecondition, "cluster \"dr\" holds the fence at epoch 2")
	tests := []struct {
		name string
		err  error
		want time.Duration
	}{
		{name: "fenced", err: fenced, want: heartbeat},
		{name: "wrapped fenced", err: fmt.Errorf("failed to declare cluster epoch in CMS: %w", fenced), want: heartbeat},
		{name: "unavailable", err: status.Error(codes.Unavailable, "cms unreachable"), want: retry},
		{name: "deadline exceeded", err: context.DeadlineExceeded, want: retry},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fence.retryDelay(context.Background(), tt.err); got != tt.want {
				t.Errorf("retryDelay(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// scriptedDeclareCMS hands each DeclareClusterEpoch call's reply channel to the test via calls
// and blocks until the test replies on it.
type scriptedDeclareCMS struct {
	mockCMS
	calls chan chan error
}

func (s *scriptedDeclareCMS) DeclareClusterEpoch(ctx context.Context, in *componentspb.DeclareClusterEpochRequest, _ ...grpc.CallOption) (*componentspb.DeclareClusterEpochResponse, error) {
	reply := make(chan error)
	s.calls <- reply
	if err := <-reply; err != nil {
		return nil, err
	}
	return &componentspb.DeclareClusterEpochResponse{ActiveCluster: in.GetCluster()}, nil
}

// TestClusterFence_CheckSharesOneDeclaration checks that concurrent Check calls before the first
// successful declaration send a single DeclareClusterEpoch, which is not cancelled with any one
// caller's context.
func TestClusterFence_CheckSharesOneDeclaration(t *testing.T) {
	const callers = 5
	cmsScripted := &scriptedDeclareCMS{calls: make(chan chan error, callers)}
	fence, err := NewClusterFence(cmsScripted, &componentspb.ClusterIdentity{Uid: testPrimaryClusterUID, Name: "prod", Epoch: 1}, ClusterFenceOptions{})
	if err != nil {
		t.Fatalf("NewClusterFence() failed: %v", err)
	}
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	started := make(chan struct{}, callers)
	errs := make(chan error, callers)
	for i := range callers {
		ctx := context.Background()
		if i == 0 {
			ctx = cancelledCtx // The shared declaration must not inherit this caller's cancellation.
		}
		go func() {
			started <- struct{}{}
			_, _, err := fence.Check(ctx)
			errs <- err
		}()
	}
	for range callers {
		<-started
	}
	reply := <-cmsScripted.calls
	time.Sleep(50 * time.Millisecond) // Let the other callers join the in-flight declaration.
	reply <- nil
	for range callers {
		if err := <-errs; err != nil {
			t.Errorf("Check() = %v, want nil", err)
		}
	}
	if extra := len(cmsScripted.calls); extra != 0 {
		t.Errorf("DeclareClusterEpoch calls = %d, want 1 shared declaration", 1+extra)
	}
}

// TestClusterFence_CheckBoundsHungDeclare checks that Check bounds its declaration by
// DeclareTimeout even when the caller's context has no deadline.
func TestClusterFence_CheckBoundsHungDeclare(t *testing.T) {
	cmsHung := &hungDeclareCMS{}
	cmsHung.hang.Store(true)
	fence, err := NewClusterFence(cmsHung, &componentspb.ClusterIdentity{Uid: testPrimaryClusterUID, Name: "prod", Epoch: 1}, ClusterFenceOptions{DeclareTimeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatalf("NewClusterFence() failed: %v", err)
	}
	if _, _, err := fence.Check(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Check() with a hung CMS = %v, want context.DeadlineExceeded", err)
	}
}

func TestClusterFence_StaleDeclarationDoesNotOverwriteNewer(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name       string
		staleReply error
	}{
		{name: "stale success", staleReply: nil},
		{name: "stale transient error", staleReply: status.Error(codes.Unavailable, "cms unreachable")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmsScripted := &scriptedDeclareCMS{calls: make(chan chan error)}
			fence, err := NewClusterFence(cmsScripted, &componentspb.ClusterIdentity{Uid: testPrimaryClusterUID, Name: "prod", Epoch: 1}, ClusterFenceOptions{})
			if err != nil {
				t.Fatalf("NewClusterFence() failed: %v", err)
			}

			staleDone := make(chan error, 1)
			go func() { staleDone <- fence.Declare(ctx) }()
			staleReply := <-cmsScripted.calls
			newerDone := make(chan error, 1)
			go func() { newerDone <- fence.Declare(ctx) }()
			newerReply := <-cmsScripted.calls

			// The newer declaration observes the fence and applies first; the older one finishes later.
			newerReply <- fenceRejection(t, store.ReasonClusterFenced)
			if err := <-newerDone; err != nil {
				t.Fatalf("newer Declare() = %v, want nil", err)
			}
			staleReply <- tc.staleReply
			if err := <-staleDone; err != nil {
				t.Fatalf("stale Declare() = %v, want nil (obsolete outcome ignored)", err)
			}
			if reason, _, err := fence.Check(ctx); err != nil || reason != ReasonClusterFenced {
				t.Errorf("Check() = (%q, %v), want (%q, nil): stale result must not overwrite newer status", reason, err, ReasonClusterFenced)
			}
		})
	}
}

func assertSourceConflictConditions(t *testing.T, conds []Condition, wantConflictReason string) {
	t.Helper()
	sc := findCondition(conds, ConditionTypeSourceConflict)
	if sc == nil || sc.Status != ConditionStatusTrue || sc.Reason != wantConflictReason {
		t.Errorf("SourceConflict = %+v, want True/%s", sc, wantConflictReason)
	}
	ready := findCondition(conds, ConditionTypeReady)
	if ready == nil || ready.Status != ConditionStatusFalse || ready.Reason != ReasonSourceConflict {
		t.Errorf("Ready = %+v, want False/%s", ready, ReasonSourceConflict)
	}
	stalled := findCondition(conds, ConditionTypeStalled)
	if stalled == nil || stalled.Status != ConditionStatusTrue || stalled.Reason != ReasonSourceConflict {
		t.Errorf("Stalled = %+v, want True/%s", stalled, ReasonSourceConflict)
	}
	if rec := findCondition(conds, ConditionTypeReconciling); rec != nil {
		t.Errorf("Reconciling = %+v, want omitted when Stalled=True", rec)
	}
	synced := findCondition(conds, ConditionTypeSynced)
	if synced == nil || synced.Status != ConditionStatusTrue || synced.Reason != ReasonCMSSynced {
		t.Errorf("Synced = %+v, want True/%s", synced, ReasonCMSSynced)
	}
}
