// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
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
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	kubefake "k8s.io/client-go/kubernetes/fake"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/cms"
	"xmanager_cloud/pkg/components/store"
)

// stubStore implements store.Store for testing server client adapter delegation.
type stubStore struct{}

func (s *stubStore) SetComponentIntent(ctx context.Context, name string, spec *componentspb.ComponentSpec, labels map[string]string, desiredGen int64) (*store.ComponentRecord, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}
func (s *stubStore) GetComponent(ctx context.Context, name string) (*store.ComponentRecord, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}
func (s *stubStore) GetComponentByID(ctx context.Context, componentID int64) (*store.ComponentRecord, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}
func (s *stubStore) ListComponents(ctx context.Context) ([]*store.ComponentRecord, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}
func (s *stubStore) PrepareDeactivation(ctx context.Context, name string, requestCascade bool) (*store.PrepareDeactivationResult, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}
func (s *stubStore) CompleteDeactivation(ctx context.Context, name string) error {
	return status.Error(codes.Unimplemented, "not implemented")
}
func (s *stubStore) UpdateObservedGeneration(ctx context.Context, name string, observedGen int64, access *componentspb.ComponentAccessStatus, state componentspb.ComponentState, statusMsg string) error {
	return status.Error(codes.Unimplemented, "not implemented")
}
func (s *stubStore) ExecuteDeletion(ctx context.Context, name string, policy componentspb.DeletionPolicy, force bool) error {
	return status.Error(codes.Unimplemented, "not implemented")
}
func (s *stubStore) ResolveComponentSnapshot(ctx context.Context, name string, generation int64) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
	return nil, nil, status.Error(codes.Unimplemented, "not implemented")
}
func (s *stubStore) ValidateComponentRequirement(ctx context.Context, name string, reqType componentspb.ComponentType, minVersion string) (*store.RequirementValidationResult, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}
func (s *stubStore) GetPlatformHealth(ctx context.Context, filter string, pageSize int32, pageToken string) (*componentspb.GetPlatformHealthResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func TestNewReconciler(t *testing.T) {
	fakeCMS := NewFakeCMSClient()
	fakeProber := NewFakeProber(true, "OK")
	fakeWM := NewFakeWorkloadManager()

	t.Run("SuccessWithAllDependencies", func(t *testing.T) {
		r, err := NewReconciler(fakeCMS, fakeProber, fakeWM)
		if err != nil {
			t.Fatalf("NewReconciler failed: %v", err)
		}
		if r == nil {
			t.Fatal("expected non-nil reconciler")
		}
	})

	t.Run("NilCMSClientError", func(t *testing.T) {
		_, err := NewReconciler(nil, fakeProber, fakeWM)
		if err == nil {
			t.Fatal("expected error when cmsClient is nil")
		}
	})

	t.Run("NilProberFallback", func(t *testing.T) {
		r, err := NewReconciler(fakeCMS, nil, fakeWM)
		if err != nil {
			t.Fatalf("NewReconciler with nil prober failed: %v", err)
		}
		if r.prober == nil {
			t.Fatal("expected fallback prober to be initialized")
		}
	})
}

func TestReconciler_TableDriven(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name                string
		req                 ReconcileRequest
		setupFakeCMS        func(c *FakeCMSClient)
		setupFakeProber     func(p *FakeProber)
		setupFakeWM         func(w *FakeWorkloadManager)
		wantErr             bool
		wantState           componentspb.ComponentState
		wantAddFinalizer    bool
		wantRemoveFinalizer bool
		wantWorkloadActive  bool
		wantReadyStatus     string
		wantDegradedStatus  string
		wantBlockedStatus   string
	}{
		{
			name: "InvalidComponentName_Empty",
			req: ReconcileRequest{
				Name: "",
				Spec: &componentspb.ComponentSpec{},
			},
			wantErr: true,
		},
		{
			name: "InvalidComponentName_BadCharacters",
			req: ReconcileRequest{
				Name: "-bad_name-",
				Spec: &componentspb.ComponentSpec{},
			},
			wantErr: true,
		},
		{
			name: "NilSpec",
			req: ReconcileRequest{
				Name: "valid-comp",
				Spec: nil,
			},
			wantErr: true,
		},
		{
			name: "1PComponent_Enable_Success",
			req: ReconcileRequest{
				Namespace:    "xmc",
				Name:         "ingestion-gateway",
				Generation:   1,
				Enabled:      true,
				HasFinalizer: false,
				Spec: &componentspb.ComponentSpec{
					Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
					Version: "v1.0.0",
				},
			},
			setupFakeProber: func(p *FakeProber) {
				p.SetResult("ingestion-gateway", true, "Gateway ready")
			},
			wantErr:            false,
			wantState:          componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			wantAddFinalizer:   true,
			wantWorkloadActive: true,
			wantReadyStatus:    "True",
			wantDegradedStatus: "False",
		},
		{
			name: "1PComponent_WorkloadProvisionFailure",
			req: ReconcileRequest{
				Namespace:    "xmc",
				Name:         "ingestion-gateway",
				Generation:   1,
				Enabled:      true,
				HasFinalizer: true,
				Spec: &componentspb.ComponentSpec{
					Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
					Version: "v1.0.0",
				},
			},
			setupFakeWM: func(w *FakeWorkloadManager) {
				w.SetEnsureErr(errors.New("k8s quota exceeded"))
			},
			wantErr:            false,
			wantState:          componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantAddFinalizer:   false,
			wantWorkloadActive: false,
			wantReadyStatus:    "False",
			wantDegradedStatus: "True",
		},
		{
			name: "1PComponent_ProbeUnhealthy",
			req: ReconcileRequest{
				Namespace:    "xmc",
				Name:         "ingestion-gateway",
				Generation:   1,
				Enabled:      true,
				HasFinalizer: true,
				Spec: &componentspb.ComponentSpec{
					Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
					Version: "v1.0.0",
				},
			},
			setupFakeProber: func(p *FakeProber) {
				p.SetResult("ingestion-gateway", false, "HTTP 503 Service Unavailable")
			},
			wantErr:            false,
			wantState:          componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantWorkloadActive: true,
			wantReadyStatus:    "False",
			wantDegradedStatus: "True",
		},
		{
			name: "3PWrapper_Enable_Success",
			req: ReconcileRequest{
				Namespace:    "xmc",
				Name:         "clickhouse",
				Generation:   1,
				Enabled:      true,
				HasFinalizer: true,
				Spec: &componentspb.ComponentSpec{
					Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
					Version: "v23.8",
					WrapperRef: &componentspb.WrapperRef{
						CrdGroup:   "clickhouse.altinity.com",
						CrdVersion: "v1",
						CrdKind:    "ClickHouseInstallation",
						TargetName: "chi-clickhouse",
					},
				},
			},
			setupFakeProber: func(p *FakeProber) {
				p.SetResult("clickhouse", true, "Cluster healthy")
			},
			wantErr:            false,
			wantState:          componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			wantWorkloadActive: false,
			wantReadyStatus:    "True",
			wantDegradedStatus: "False",
		},
		{
			name: "1PComponent_Disable_Success",
			req: ReconcileRequest{
				Namespace:    "xmc",
				Name:         "ingestion-gateway",
				Generation:   2,
				Enabled:      false,
				HasFinalizer: true,
				Spec: &componentspb.ComponentSpec{
					Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
					Version: "v1.0.0",
				},
			},
			setupFakeWM: func(w *FakeWorkloadManager) {
				w.ActiveWorkloads["ingestion-gateway"] = true
			},
			wantErr:            false,
			wantState:          componentspb.ComponentState_COMPONENT_STATE_DISABLED,
			wantWorkloadActive: false,
			wantReadyStatus:    "False",
			wantDegradedStatus: "False",
		},
		{
			name: "Deletion_BlockedByDeletionProtection",
			req: ReconcileRequest{
				Namespace:          "xmc",
				Name:               "clickhouse",
				Generation:         1,
				Enabled:            true,
				DeletionProtection: true,
				IsDeleting:         true,
				HasFinalizer:       true,
				Spec: &componentspb.ComponentSpec{
					Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
					Version: "v23.8",
				},
			},
			wantErr:             false,
			wantRemoveFinalizer: false,
			wantBlockedStatus:   "True",
			wantReadyStatus:     "False",
		},
		{
			name: "1PComponent_Deletion_DeletePolicy_Success",
			req: ReconcileRequest{
				Namespace:          "xmc",
				Name:               "ingestion-gateway",
				Generation:         3,
				DeletionProtection: false,
				IsDeleting:         true,
				HasFinalizer:       true,
				Spec: &componentspb.ComponentSpec{
					Type:           componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
					Version:        "v1.0.0",
					DeletionPolicy: componentspb.DeletionPolicy_DELETION_POLICY_DELETE,
				},
			},
			setupFakeWM: func(w *FakeWorkloadManager) {
				w.ActiveWorkloads["ingestion-gateway"] = true
			},
			wantErr:             false,
			wantState:           componentspb.ComponentState_COMPONENT_STATE_DELETED,
			wantRemoveFinalizer: true,
			wantWorkloadActive:  false,
		},
		{
			name: "1PComponent_Deletion_OrphanPolicy_Success",
			req: ReconcileRequest{
				Namespace:          "xmc",
				Name:               "ingestion-gateway",
				Generation:         3,
				DeletionProtection: false,
				IsDeleting:         true,
				HasFinalizer:       true,
				Spec: &componentspb.ComponentSpec{
					Type:           componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
					Version:        "v1.0.0",
					DeletionPolicy: componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN,
				},
			},
			setupFakeWM: func(w *FakeWorkloadManager) {
				w.ActiveWorkloads["ingestion-gateway"] = true
			},
			wantErr:             false,
			wantState:           componentspb.ComponentState_COMPONENT_STATE_DELETED,
			wantRemoveFinalizer: true,
			wantWorkloadActive:  true,
		},
		{
			name: "CMS_SetComponentIntent_Failure",
			req: ReconcileRequest{
				Namespace:    "xmc",
				Name:         "ingestion-gateway",
				Generation:   1,
				Enabled:      true,
				HasFinalizer: true,
				Spec: &componentspb.ComponentSpec{
					Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
					Version: "v1.0.0",
				},
			},
			setupFakeCMS: func(c *FakeCMSClient) {
				c.SetIntentFunc = func(ctx context.Context, in *componentspb.SetComponentIntentRequest) (*componentspb.SetComponentIntentResponse, error) {
					return nil, status.Error(codes.FailedPrecondition, "dependency cycle detected")
				}
			},
			wantErr:            false,
			wantReadyStatus:    "False",
			wantDegradedStatus: "True",
		},
		{
			name: "CMS_CompleteDeactivation_Failure",
			req: ReconcileRequest{
				Namespace:    "xmc",
				Name:         "ingestion-gateway",
				Generation:   2,
				Enabled:      false,
				HasFinalizer: true,
				Spec: &componentspb.ComponentSpec{
					Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
					Version: "v1.0.0",
				},
			},
			setupFakeCMS: func(c *FakeCMSClient) {
				c.CompleteDeactivationFunc = func(ctx context.Context, in *componentspb.CompleteDeactivationRequest) (*componentspb.CompleteDeactivationResponse, error) {
					return nil, status.Error(codes.Internal, "database error")
				}
			},
			wantErr: true,
		},
		{
			name: "CMS_ExecuteDeletion_Failure",
			req: ReconcileRequest{
				Namespace:          "xmc",
				Name:               "ingestion-gateway",
				Generation:         3,
				DeletionProtection: false,
				IsDeleting:         true,
				HasFinalizer:       true,
				Spec: &componentspb.ComponentSpec{
					Type:           componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
					Version:        "v1.0.0",
					DeletionPolicy: componentspb.DeletionPolicy_DELETION_POLICY_DELETE,
				},
			},
			setupFakeCMS: func(c *FakeCMSClient) {
				c.ExecuteDeletionFunc = func(ctx context.Context, in *componentspb.ExecuteDeletionRequest) (*componentspb.ExecuteDeletionResponse, error) {
					return nil, status.Error(codes.Internal, "database error")
				}
			},
			wantErr: true,
		},
		{
			name: "CMS_UpdateObservedGen_Failure",
			req: ReconcileRequest{
				Namespace:    "xmc",
				Name:         "ingestion-gateway",
				Generation:   1,
				Enabled:      true,
				HasFinalizer: true,
				Spec: &componentspb.ComponentSpec{
					Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
					Version: "v1.0.0",
				},
			},
			setupFakeCMS: func(c *FakeCMSClient) {
				c.UpdateObservedGenFunc = func(ctx context.Context, in *componentspb.UpdateObservedGenerationRequest) (*componentspb.UpdateObservedGenerationResponse, error) {
					return nil, status.Error(codes.Unavailable, "spanner unreachable")
				}
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fakeCMS := NewFakeCMSClient()
			fakeProber := NewFakeProber(true, "Healthy")
			fakeWM := NewFakeWorkloadManager()

			if tc.setupFakeCMS != nil {
				tc.setupFakeCMS(fakeCMS)
			}
			if tc.setupFakeProber != nil {
				tc.setupFakeProber(fakeProber)
			}
			if tc.setupFakeWM != nil {
				tc.setupFakeWM(fakeWM)
			}

			reconciler, err := NewReconciler(fakeCMS, fakeProber, fakeWM)
			if err != nil {
				t.Fatalf("NewReconciler failed: %v", err)
			}

			res, err := reconciler.ReconcileComponent(ctx, tc.req)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ReconcileComponent err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}

			if res.State != tc.wantState && tc.wantState != 0 {
				t.Errorf("got state %v, want %v", res.State, tc.wantState)
			}
			if res.AddFinalizer != tc.wantAddFinalizer {
				t.Errorf("got AddFinalizer = %v, want %v", res.AddFinalizer, tc.wantAddFinalizer)
			}
			if res.RemoveFinalizer != tc.wantRemoveFinalizer {
				t.Errorf("got RemoveFinalizer = %v, want %v", res.RemoveFinalizer, tc.wantRemoveFinalizer)
			}
			if fakeWM.IsWorkloadActive(tc.req.Name) != tc.wantWorkloadActive {
				t.Errorf("got workload active = %v, want %v", fakeWM.IsWorkloadActive(tc.req.Name), tc.wantWorkloadActive)
			}

			if tc.wantReadyStatus != "" {
				cond := findCondition(res.Conditions, ConditionReady)
				if cond == nil || cond.Status != tc.wantReadyStatus {
					t.Errorf("Ready condition = %+v, want status %s", cond, tc.wantReadyStatus)
				}
			}
			if tc.wantDegradedStatus != "" {
				cond := findCondition(res.Conditions, ConditionDegraded)
				if cond == nil || cond.Status != tc.wantDegradedStatus {
					t.Errorf("Degraded condition = %+v, want status %s", cond, tc.wantDegradedStatus)
				}
			}
			if tc.wantBlockedStatus != "" {
				cond := findCondition(res.Conditions, ConditionDeletionBlocked)
				if cond == nil || cond.Status != tc.wantBlockedStatus {
					t.Errorf("DeletionBlocked condition = %+v, want status %s", cond, tc.wantBlockedStatus)
				}
			}
		})
	}
}

func TestConditionTransitionPreservation(t *testing.T) {
	origTime := time.Now().Add(-10 * time.Minute)
	existingConditions := []Condition{
		{
			Type:               ConditionReady,
			Status:             "True",
			ObservedGeneration: 1,
			LastTransitionTime: origTime,
			Reason:             "ComponentHealthy",
			Message:            "Initial",
		},
	}

	var conditions []Condition
	setCondition(&conditions, existingConditions, ConditionReady, "True", 2, "ComponentHealthy", "Still healthy")

	if len(conditions) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(conditions))
	}
	if !conditions[0].LastTransitionTime.Equal(origTime) {
		t.Errorf("expected transition time %v, got %v", origTime, conditions[0].LastTransitionTime)
	}

	// Status changes -> Transition time should be updated
	setCondition(&conditions, conditions, ConditionReady, "False", 3, "Degraded", "Failed")
	if conditions[0].LastTransitionTime.Equal(origTime) {
		t.Errorf("expected transition time to update when status changes")
	}
}

func TestCMSClientAdapter_Delegations(t *testing.T) {
	ctx := context.Background()
	cmsServer := cms.NewServer(&stubStore{})
	adapter := NewCMSClientAdapter(cmsServer)

	// Verify all methods delegate to the underlying server without panic
	_, _ = adapter.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{})
	_, _ = adapter.UpdateObservedGeneration(ctx, &componentspb.UpdateObservedGenerationRequest{})
	_, _ = adapter.PrepareDeactivation(ctx, &componentspb.PrepareDeactivationRequest{})
	_, _ = adapter.CompleteDeactivation(ctx, &componentspb.CompleteDeactivationRequest{})
	_, _ = adapter.ExecuteDeletion(ctx, &componentspb.ExecuteDeletionRequest{})
	_, _ = adapter.ResolveComponentSnapshot(ctx, &componentspb.ResolveComponentSnapshotRequest{})
	_, _ = adapter.ValidateComponentRequirement(ctx, &componentspb.ValidateComponentRequirementRequest{})
	_, _ = adapter.GetPlatformHealth(ctx, &componentspb.GetPlatformHealthRequest{})
}

func TestFakeDoubles_Helpers(t *testing.T) {
	ctx := context.Background()

	// 1. FakeWorkloadManager
	wm := NewFakeWorkloadManager()
	spec := &componentspb.ComponentSpec{}
	_ = wm.Ensure1PWorkload(ctx, "xmc", "test-1", spec)
	if !wm.IsWorkloadActive("test-1") {
		t.Error("expected test-1 active")
	}
	_ = wm.Delete1PWorkload(ctx, "xmc", "test-1")
	if wm.IsWorkloadActive("test-1") {
		t.Error("expected test-1 inactive")
	}
	wm.SetEnsureErr(errors.New("ensure err"))
	wm.SetDeleteErr(errors.New("delete err"))
	if err := wm.Ensure1PWorkload(ctx, "xmc", "test-2", spec); err == nil {
		t.Error("expected ensure err")
	}
	if err := wm.Delete1PWorkload(ctx, "xmc", "test-2"); err == nil {
		t.Error("expected delete err")
	}
	wm.Reset()
	if wm.IsWorkloadActive("test-1") {
		t.Error("expected active workloads empty after reset")
	}

	// 2. FakeProber
	prober := NewFakeProber(true, "OK")
	_, _ = prober.ProbeHealth(ctx, spec)
	prober.SetResult("comp-1", false, "Failed")
	ok, msg := prober.ProbeComponentStatus(ctx, "comp-1", spec)
	if ok || msg != "Failed" {
		t.Errorf("ProbeComponentStatus = (%v, %q), want (false, Failed)", ok, msg)
	}
	if prober.GetCallCount("comp-1") != 1 {
		t.Errorf("GetCallCount = %d, want 1", prober.GetCallCount("comp-1"))
	}
	prober.Reset()
	if prober.GetCallCount("comp-1") != 0 {
		t.Errorf("GetCallCount after reset = %d, want 0", prober.GetCallCount("comp-1"))
	}

	// 3. NetworkProber
	np := NewNetworkProber(0)
	ok, _ = np.ProbeHealth(ctx, nil)
	if ok {
		t.Error("expected nil spec probe to be false")
	}
	ok, _ = np.ProbeHealth(ctx, &componentspb.ComponentSpec{
		Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
	})
	if ok {
		t.Error("expected missing wrapperRef to fail")
	}
	ok, _ = np.ProbeHealth(ctx, &componentspb.ComponentSpec{
		Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
		WrapperRef: &componentspb.WrapperRef{
			TargetName: "",
		},
	})
	if ok {
		t.Error("expected empty targetName to fail")
	}
	ok, _ = np.ProbeHealth(ctx, &componentspb.ComponentSpec{
		Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
		WrapperRef: &componentspb.WrapperRef{
			TargetName: "wrapper-target",
		},
	})
	if !ok {
		t.Error("expected valid wrapperRef to succeed")
	}

	// HTTP probing tests
	_, _ = np.ProbeHTTP(ctx, "")
	_, _ = np.ProbeHTTP(ctx, "::invalid-url::")

	// Successful HTTP server
	okServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer okServer.Close()
	ok, _ = np.ProbeHTTP(ctx, okServer.URL)
	if !ok {
		t.Error("expected successful HTTP probe to return true")
	}

	// Failing HTTP server (500)
	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failServer.Close()
	ok, _ = np.ProbeHTTP(ctx, failServer.URL)
	if ok {
		t.Error("expected 500 HTTP probe to return false")
	}

	// TCP probing tests
	_, _ = np.ProbeTCP(ctx, []string{""})
	_, _ = np.ProbeTCP(ctx, []string{"127.0.0.1:0"})

	// Successful TCP listener
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err == nil {
		defer listener.Close()
		ok, _ = np.ProbeTCP(ctx, []string{listener.Addr().String()})
		if !ok {
			t.Errorf("expected reachable TCP probe for %s", listener.Addr().String())
		}
	}

	_, _ = np.ProbeComponentStatus(ctx, "test", spec)

	// 4. FakeCMSClient
	cmsClient := NewFakeCMSClient()
	cmsClient.PrepareDeactivationFunc = func(ctx context.Context, in *componentspb.PrepareDeactivationRequest) (*componentspb.PrepareDeactivationResponse, error) {
		return &componentspb.PrepareDeactivationResponse{State: componentspb.ComponentState_COMPONENT_STATE_DISABLING}, nil
	}
	_, _ = cmsClient.PrepareDeactivation(ctx, &componentspb.PrepareDeactivationRequest{Name: "test"})
	_, _ = cmsClient.ResolveComponentSnapshot(ctx, &componentspb.ResolveComponentSnapshotRequest{})
	_, _ = cmsClient.ValidateComponentRequirement(ctx, &componentspb.ValidateComponentRequirementRequest{})
	_, _ = cmsClient.GetPlatformHealth(ctx, &componentspb.GetPlatformHealthRequest{})
}

func TestK8sWorkloadManager(t *testing.T) {
	ctx := context.Background()
	clientset := kubefake.NewSimpleClientset()
	wm := NewK8sWorkloadManager(clientset)

	spec := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v1.0.0",
	}

	t.Run("ValidationErrors", func(t *testing.T) {
		nilWM := NewK8sWorkloadManager(nil)
		if err := nilWM.Ensure1PWorkload(ctx, "xmc", "comp-1", spec); err == nil {
			t.Error("expected error for nil clientset")
		}
		if err := nilWM.Delete1PWorkload(ctx, "xmc", "comp-1"); err == nil {
			t.Error("expected error for nil clientset")
		}
		if err := wm.Ensure1PWorkload(ctx, "xmc", "", spec); err == nil {
			t.Error("expected error for empty name")
		}
		if err := wm.Delete1PWorkload(ctx, "xmc", ""); err == nil {
			t.Error("expected error for empty name")
		}
		if err := wm.Ensure1PWorkload(ctx, "xmc", "comp-1", nil); err == nil {
			t.Error("expected error for nil spec")
		}
	})

	t.Run("CreateAndUpdateLifecycle", func(t *testing.T) {
		// Empty version defaults to latest
		emptyVersionSpec := &componentspb.ComponentSpec{
			Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		}
		err := wm.Ensure1PWorkload(ctx, "xmc", "comp-empty-ver", emptyVersionSpec)
		if err != nil {
			t.Fatalf("Ensure1PWorkload with empty version failed: %v", err)
		}

		err = wm.Ensure1PWorkload(ctx, "xmc", "comp-1", spec)
		if err != nil {
			t.Fatalf("Ensure1PWorkload failed: %v", err)
		}

		updatedSpec := &componentspb.ComponentSpec{
			Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
			Version: "v2.0.0",
		}
		err = wm.Ensure1PWorkload(ctx, "xmc", "comp-1", updatedSpec)
		if err != nil {
			t.Fatalf("Ensure1PWorkload update failed: %v", err)
		}

		err = wm.Delete1PWorkload(ctx, "xmc", "comp-1")
		if err != nil {
			t.Fatalf("Delete1PWorkload failed: %v", err)
		}

		err = wm.Delete1PWorkload(ctx, "xmc", "comp-1")
		if err != nil {
			t.Fatalf("Delete1PWorkload second call failed: %v", err)
		}
	})
}

func TestReconciler_EdgeCases(t *testing.T) {
	ctx := context.Background()
	fakeCMS := NewFakeCMSClient()
	fakeProber := NewFakeProber(true, "OK")
	fakeWM := NewFakeWorkloadManager()

	r, err := NewReconciler(fakeCMS, fakeProber, fakeWM)
	if err != nil {
		t.Fatalf("NewReconciler failed: %v", err)
	}

	t.Run("CopyConditions_Nil", func(t *testing.T) {
		if res := copyConditions(nil); res != nil {
			t.Errorf("copyConditions(nil) = %v, want nil", res)
		}
	})

	t.Run("SetCondition_ExistingUpdate", func(t *testing.T) {
		existing := []Condition{
			{
				Type:    ConditionReady,
				Status:  "False",
				Reason:  "OldReason",
				Message: "OldMsg",
			},
		}
		conds := copyConditions(existing)
		setCondition(&conds, existing, ConditionReady, "True", 2, "NewReason", "NewMsg")
		if len(conds) != 1 {
			t.Fatalf("expected 1 condition, got %d", len(conds))
		}
		if conds[0].Status != "True" || conds[0].Reason != "NewReason" {
			t.Errorf("unexpected updated condition: %+v", conds[0])
		}
	})

	t.Run("Reconcile_SetIntentStatusPropagation", func(t *testing.T) {
		fakeCMS.SetIntentFunc = func(ctx context.Context, in *componentspb.SetComponentIntentRequest) (*componentspb.SetComponentIntentResponse, error) {
			return &componentspb.SetComponentIntentResponse{
				Component: &componentspb.Component{
					Name: in.GetName(),
					Status: &componentspb.ComponentAccessStatus{
						State: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
					},
				},
			}, nil
		}

		res, err := r.ReconcileComponent(ctx, ReconcileRequest{
			Name:    "comp-status-prop",
			Enabled: true,
			Spec: &componentspb.ComponentSpec{
				Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
			},
		})
		if err != nil {
			t.Fatalf("ReconcileComponent failed: %v", err)
		}
		if res.State != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
			t.Errorf("got state %v, want ENABLED", res.State)
		}
	})
}

func findCondition(conditions []Condition, condType string) *Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}
