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
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
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

// countingDeclareCMS counts successful DeclareClusterEpoch calls to the wrapped CMSClient.
type countingDeclareCMS struct {
	CMSClient
	declared atomic.Int32
}

func (c *countingDeclareCMS) DeclareClusterEpoch(ctx context.Context, in *componentspb.DeclareClusterEpochRequest, opts ...grpc.CallOption) (*componentspb.DeclareClusterEpochResponse, error) {
	resp, err := c.CMSClient.DeclareClusterEpoch(ctx, in, opts...)
	if err == nil {
		c.declared.Add(1)
	}
	return resp, err
}

// TestClusterFence_StartDeclaresToCMS checks that a started fence declares its identity to a real
// CMS, so a second cluster at the same epoch is fenced out, and that a fenced fence's Declare
// returns the CMS FailedPrecondition rejection.
func TestClusterFence_StartDeclaresToCMS(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmsClient := &countingDeclareCMS{CMSClient: NewCMSClientAdapter(cms.NewServer(store.NewMemoryStore(), cms.WithAuthorizer(cms.NewDisabledAuthorizer())))}

	primary, err := NewClusterFence(cmsClient, &componentspb.ClusterIdentity{Uid: testPrimaryClusterUID, Name: "primary", Epoch: 2},
		ClusterFenceOptions{HeartbeatInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatalf("NewClusterFence(primary) failed: %v", err)
	}
	if !primary.NeedLeaderElection() {
		t.Error("NeedLeaderElection() = false, want true")
	}
	done := make(chan error, 1)
	go func() { done <- primary.Start(ctx) }()

	// Wait for the primary's first heartbeat: the first fence row goes to whichever cluster
	// declares first.
	deadline := time.Now().Add(5 * time.Second)
	for cmsClient.declared.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("primary Start() did not declare to CMS within 5s")
		}
		time.Sleep(5 * time.Millisecond)
	}

	standby, err := NewClusterFence(cmsClient, &componentspb.ClusterIdentity{Uid: testStandbyClusterUID, Name: "standby", Epoch: 2}, ClusterFenceOptions{})
	if err != nil {
		t.Fatalf("NewClusterFence(standby) failed: %v", err)
	}
	if err := standby.Declare(ctx); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("standby Declare() = %v, want FailedPrecondition while the primary holds the fence", err)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("primary.Start() returned error: %v", err)
	}
}

func TestClusterFence_DeclareSendsIdentity(t *testing.T) {
	cmsMock := &mockCMS{}
	identity := &componentspb.ClusterIdentity{Uid: testPrimaryClusterUID, Name: "prod", Epoch: 3}
	fence, err := NewClusterFence(cmsMock, identity, ClusterFenceOptions{})
	if err != nil {
		t.Fatalf("NewClusterFence() failed: %v", err)
	}
	if err := fence.Declare(context.Background()); err != nil {
		t.Fatalf("Declare() = %v, want nil", err)
	}
	got := cmsMock.lastDeclareReq.GetCluster()
	if got.GetUid() != identity.GetUid() || got.GetName() != identity.GetName() || got.GetEpoch() != identity.GetEpoch() {
		t.Errorf("declared identity = %+v, want %+v", got, identity)
	}

	cmsMock.declareErr = status.Error(codes.Unavailable, "cms unreachable")
	if err := fence.Declare(context.Background()); status.Code(err) != codes.Unavailable {
		t.Errorf("Declare() with CMS down = %v, want wrapped Unavailable", err)
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

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("fence.Start() returned error: %v", err)
	}
}
