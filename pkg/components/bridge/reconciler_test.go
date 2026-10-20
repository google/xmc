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
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/pkg/components/store"
)

func TestNewReconciler(t *testing.T) {
	if _, err := NewReconciler(nil, nil, nil); err == nil {
		t.Fatal("expected error on nil CMS client")
	}
	if r, err := NewReconciler(&mockCMS{}, nil, nil); err != nil || r == nil {
		t.Fatalf("unexpected constructor failure: %v", err)
	}
}

func TestReconcileComponent_Validation(t *testing.T) {
	r, _ := NewReconciler(&mockCMS{}, nil, nil)
	ctx := context.Background()
	if _, err := r.ReconcileComponent(ctx, ReconcileComponentInput{}); err == nil {
		t.Error("expected error for empty name/spec")
	}
	if _, err := r.Reconcile(ctx, nil); err == nil {
		t.Error("expected error for nil unstructured")
	}
}

func TestReconcileComponent_Scenarios(t *testing.T) {
	ctx := context.Background()
	spec1P := &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT}
	spec3P := &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER}

	spec3PWithRef := &componentspb.ComponentSpec{
		Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
		WrapperRef: &componentspb.WrapperRef{
			CrdGroup: "clickhouse.altinity.com", CrdVersion: "v1", CrdKind: "ClickHouseInstallation", TargetName: "my-ch",
		},
	}

	tests := []struct {
		name          string
		cms           *mockCMS
		prober        *mockProber
		helm          *mockHelm
		in            ReconcileComponentInput
		wantErr       bool
		wantState     componentspb.ComponentState
		wantRemoveFin bool
		// wantHealth, when set, asserts the Ready/Reconciling/Stalled condition statuses and Ready reason.
		wantHealth *healthConditions
	}{
		{
			name: "active 3P",
			cms:  &mockCMS{},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3P,
				Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		},
		{
			name:   "active operatorless 3P with endpoint and secret in parameters",
			cms:    &mockCMS{},
			prober: &mockProber{healthy: true},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1,
				Spec: &componentspb.ComponentSpec{
					Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
					Parameters: map[string]string{
						"host":                "clickhouse.xmc-metrics.svc.cluster.local",
						"healthCheckEndpoint": "http://clickhouse.xmc-metrics.svc.cluster.local:8123/ping",
						"secretName":          "clickhouse-secret",
					},
				},
				Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		},
		{
			name: "active 3P with coordinate injection",
			cms:  &mockCMS{},
			prober: &mockProber{status: &ResourceStatus{
				Found:            true,
				Ready:            true,
				Endpoint:         "http://chi-prod.analytics.svc.cluster.local:8123",
				ConnectionSecret: "clickhouse-credentials",
				Message:          "Operational",
			}},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef,
				Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		},
		{
			name:   "active 3P with status detector not ready",
			cms:    &mockCMS{},
			prober: &mockProber{status: &ResourceStatus{Found: true, Ready: false, Message: "Waiting for pods"}},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef,
				Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name:   "active 3P with status detector resource not found",
			cms:    &mockCMS{},
			prober: &mockProber{status: &ResourceStatus{Found: false, Message: "CRD not installed: clickhouses.clickhouse.altinity.com"}},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef,
				Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name:   "active 3P with status detector error",
			cms:    &mockCMS{},
			prober: &mockProber{detectErr: errors.New("k8s api timeout")},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef,
				Enabled: true,
			},
			wantErr:   true,
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name:   "active 3P with status detector nil status",
			cms:    &mockCMS{},
			prober: &mockProber{nilStatus: true},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef,
				Enabled: true,
			},
			wantErr:   true,
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name:   "active 3P with status detector error and CMS failure",
			cms:    &mockCMS{obsErr: errors.New("spanner write failed")},
			prober: &mockProber{detectErr: errors.New("k8s api timeout")},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef,
				Enabled: true,
			},
			wantErr:   true,
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name:   "active 3P with empty target name",
			cms:    &mockCMS{},
			prober: &mockProber{status: &ResourceStatus{Found: true, Ready: true}},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1,
				Spec: &componentspb.ComponentSpec{
					Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
				},
				Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name: "active 1P with nil prober defaults to healthy",
			cms:  &mockCMS{},
			helm: &mockHelm{},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, Spec: spec1P,
				Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		},
		{
			name:   "active 1P reports healthy from deployed Helm release regardless of prober",
			cms:    &mockCMS{},
			prober: &mockProber{healthy: false, msg: "prober must not be consulted for 1P"},
			helm:   &mockHelm{},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, Spec: spec1P,
				Enabled: true,
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			wantHealth: &healthConditions{ReadyStatus: ConditionStatusTrue, Reason: ReasonComponentHealthy},
		},
		{
			name:   "active 1P with missing Helm release is unhealthy even if prober is healthy",
			cms:    &mockCMS{},
			prober: &mockProber{healthy: true},
			helm:   &mockHelm{unhealthyReason: ReasonHelmReleaseNotFound},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, Spec: spec1P,
				Enabled: true,
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantHealth: &healthConditions{ReadyStatus: ConditionStatusFalse, ReconcilingStatus: ConditionStatusTrue, Reason: ReasonHelmReleaseNotFound},
		},
		{
			name: "active 1P with failed async Helm upgrade is unhealthy",
			cms:  &mockCMS{},
			helm: &mockHelm{unhealthyReason: ReasonHelmUpgradeFailed},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, Spec: spec1P,
				Enabled: true,
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantHealth: &healthConditions{ReadyStatus: ConditionStatusFalse, StalledStatus: ConditionStatusTrue, Reason: ReasonHelmUpgradeFailed},
		},
		{
			name: "active 1P with in-flight Helm upgrade is reconciling",
			cms:  &mockCMS{},
			helm: &mockHelm{unhealthyReason: ReasonHelmUpgradePending},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, Spec: spec1P,
				Enabled: true,
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantHealth: &healthConditions{ReadyStatus: ConditionStatusFalse, ReconcilingStatus: ConditionStatusTrue, Reason: ReasonHelmUpgradePending},
		},
		{
			name: "active 1P with stuck Helm release is stalled",
			cms:  &mockCMS{},
			helm: &mockHelm{unhealthyReason: ReasonHelmReleaseStuck},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, Spec: spec1P,
				Enabled: true,
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantHealth: &healthConditions{ReadyStatus: ConditionStatusFalse, StalledStatus: ConditionStatusTrue, Reason: ReasonHelmReleaseStuck},
		},
		{
			name: "active 1P transient Helm status error keeps previous healthy conditions",
			cms:  &mockCMS{},
			helm: &mockHelm{unhealthyReason: ReasonHelmStatusUnavailable},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, Spec: spec1P,
				Enabled: true,
				ExistingConditions: []Condition{
					{Type: ConditionTypeReady, Status: ConditionStatusTrue, ObservedGeneration: 1, Reason: ReasonComponentHealthy},
				},
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			wantHealth: &healthConditions{ReadyStatus: ConditionStatusTrue, Reason: ReasonComponentHealthy},
		},
		{
			name: "active 1P transient Helm status error keeps previous reconciling conditions",
			cms:  &mockCMS{},
			helm: &mockHelm{unhealthyReason: ReasonHelmStatusUnavailable},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, Spec: spec1P,
				Enabled: true,
				ExistingConditions: []Condition{
					{Type: ConditionTypeReady, Status: ConditionStatusFalse, ObservedGeneration: 1, Reason: ReasonHelmReleaseNotFound},
					{Type: ConditionTypeReconciling, Status: ConditionStatusTrue, ObservedGeneration: 1, Reason: ReasonProgressing},
				},
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantHealth: &healthConditions{ReadyStatus: ConditionStatusFalse, ReconcilingStatus: ConditionStatusTrue, Reason: ReasonHelmReleaseNotFound},
		},
		{
			name: "active 1P transient Helm status error without current-generation conditions is reconciling",
			cms:  &mockCMS{},
			helm: &mockHelm{unhealthyReason: ReasonHelmStatusUnavailable},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 2, Spec: spec1P,
				Enabled: true,
				ExistingConditions: []Condition{
					{Type: ConditionTypeReady, Status: ConditionStatusTrue, ObservedGeneration: 1, Reason: ReasonComponentHealthy},
				},
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantHealth: &healthConditions{ReadyStatus: ConditionStatusFalse, ReconcilingStatus: ConditionStatusTrue, Reason: ReasonHelmStatusUnavailable},
		},
		{
			name:   "active 1P without HelmManager falls back to prober",
			cms:    &mockCMS{},
			prober: &mockProber{healthy: false, msg: "probe failed"},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, Spec: spec1P,
				Enabled: true,
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantHealth: &healthConditions{ReadyStatus: ConditionStatusFalse, ReconcilingStatus: ConditionStatusTrue, Reason: ReasonHealthProbeFailed},
		},
		{
			name: "deletion of an enabled 3P cleans up",
			cms:  &mockCMS{},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3P,
				IsDeleting: true, Enabled: true,
			},
			wantState:     componentspb.ComponentState_COMPONENT_STATE_DELETED,
			wantRemoveFin: true,
		},
		{
			name: "deletion successful 1P",
			cms:  &mockCMS{},
			helm: &mockHelm{active: true},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 2, Spec: spec1P,
				IsDeleting: true,
			},
			wantState:     componentspb.ComponentState_COMPONENT_STATE_DELETED,
			wantRemoveFin: true,
		},
		{
			name: "deletion successful 1P orphan policy",
			cms:  &mockCMS{},
			helm: &mockHelm{active: true},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 2,
				Spec: &componentspb.ComponentSpec{
					Type:           componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
					DeletionPolicy: componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN,
				},
				IsDeleting: true,
			},
			wantState:     componentspb.ComponentState_COMPONENT_STATE_DELETED,
			wantRemoveFin: true,
		},
		{
			name: "deletion successful 3P",
			cms:  &mockCMS{},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 2, Spec: spec3P,
				IsDeleting: true,
			},
			wantState:     componentspb.ComponentState_COMPONENT_STATE_DELETED,
			wantRemoveFin: true,
		},
		{
			name: "deletion 1P Helm delete failure",
			cms:  &mockCMS{},
			helm: &mockHelm{deleteErr: errors.New("delete error")},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 2, Spec: spec1P,
				IsDeleting: true,
			},
			wantErr: true,
		},
		{
			name: "deletion CMS failure",
			cms:  &mockCMS{delErr: errors.New("cms spanner error")},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 2, Spec: spec1P,
				IsDeleting: true,
			},
			wantErr: true,
		},
		{
			name: "deletion CMS validation failure blocks 1P teardown",
			cms:  &mockCMS{delErr: status.Error(codes.FailedPrecondition, "active dependents exist")},
			helm: &mockHelm{active: true},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 2, Spec: spec1P,
				IsDeleting: true,
			},
			wantErr: true,
		},
		{
			name: "deletion CMS NotFound idempotent retry proceeds to 1P teardown",
			cms:  &mockCMS{delErr: status.Error(codes.NotFound, "already deleted")},
			helm: &mockHelm{active: true},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 2, Spec: spec1P,
				IsDeleting: true,
			},
			wantState:     componentspb.ComponentState_COMPONENT_STATE_DELETED,
			wantRemoveFin: true,
		},
		{
			name: "disabled component",
			cms:  &mockCMS{},
			helm: &mockHelm{active: true},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 2, Spec: spec1P,
				Enabled: false,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_DISABLED,
		},
		{
			name: "disabled component 1P Helm delete failure",
			cms:  &mockCMS{},
			helm: &mockHelm{active: true, deleteErr: errors.New("cannot stop release")},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 2, Spec: spec1P,
				Enabled: false,
			},
			wantErr:   true,
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name: "disabled component update observed CMS failure",
			cms:  &mockCMS{obsErr: errors.New("cms unreachable")},
			helm: &mockHelm{},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 2, Spec: spec1P,
				Enabled: false,
			},
			wantErr: true,
		},
		{
			name: "disabled component with orphan deletion policy still deletes workload",
			cms:  &mockCMS{},
			helm: &mockHelm{active: true},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 2,
				Spec: &componentspb.ComponentSpec{
					Type:           componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
					DeletionPolicy: componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN,
				},
				Enabled: false,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_DISABLED,
		},
		{
			name:    "disabled component workload deletion failure",
			cms:     &mockCMS{},
			helm:    &mockHelm{active: true, deleteErr: errors.New("cannot delete child pod")},
			wantErr: true,
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 2, Spec: spec1P,
				Enabled: false,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name:    "disabled component update observed CMS error",
			cms:     &mockCMS{obsErr: errors.New("cms update error")},
			helm:    &mockHelm{active: true},
			wantErr: true,
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 2, Spec: spec1P,
				Enabled: false,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_DISABLED,
		},
		{
			name:    "intent application CMS error",
			cms:     &mockCMS{setErr: status.Error(codes.InvalidArgument, "bad spec")},
			helm:    &mockHelm{active: true},
			wantErr: true,
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, Spec: spec1P,
				Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name:    "1P Helm provisioning error",
			cms:     &mockCMS{},
			helm:    &mockHelm{ensureErr: errors.New("upgrade failed")},
			wantErr: true,
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, Spec: spec1P,
				Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name:   "health probe unhealthy",
			cms:    &mockCMS{},
			prober: &mockProber{healthy: false, msg: "Pod CrashLoop"},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef,
				Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name:   "health probe healthy",
			cms:    &mockCMS{},
			prober: &mockProber{healthy: true, msg: "Service active"},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef,
				Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		},
		{
			name:   "3P wrapper status detection error",
			cms:    &mockCMS{},
			prober: &mockProber{detectErr: errors.New("k8s api error")},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef, Enabled: true,
			},
			wantErr: true,
		},
		{
			name:   "3P wrapper resource not found",
			cms:    &mockCMS{},
			prober: &mockProber{status: &ResourceStatus{Found: false, Ready: false, Message: "resource not found"}},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name:   "3P wrapper unready with empty message fallback",
			cms:    &mockCMS{},
			prober: &mockProber{status: &ResourceStatus{Found: false, Ready: false, Message: ""}},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name:   "3P wrapper resource not ready",
			cms:    &mockCMS{},
			prober: &mockProber{status: &ResourceStatus{Found: true, Ready: false, Message: "reconciling pods"}},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name:   "3P wrapper active probe error",
			cms:    &mockCMS{},
			prober: &mockProber{status: &ResourceStatus{Found: true, Ready: true, Endpoint: "http://clickhouse.xmc:8123"}, probeErr: errors.New("connection refused")},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name:   "3P wrapper active probe unhealthy",
			cms:    &mockCMS{},
			prober: &mockProber{status: &ResourceStatus{Found: true, Ready: true, Endpoint: "http://clickhouse.xmc:8123"}, probeRes: &HealthProbeResult{Healthy: false, Message: "503 Service Unavailable"}},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name:   "3P wrapper status detector returns nil status without error",
			cms:    &mockCMS{},
			prober: &mockProber{nilStatus: true},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef, Enabled: true,
			},
			wantErr: true,
		},
		{
			name:   "3P wrapper prober returns nil probe result without error",
			cms:    &mockCMS{},
			prober: &mockProber{status: &ResourceStatus{Found: true, Ready: true, Endpoint: "http://clickhouse.xmc:8123"}, hasProbeRes: true, probeRes: nil},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name:   "3P wrapper with nil prober returns enabled",
			cms:    &mockCMS{},
			prober: nil,
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		},
		{
			name:   "3P wrapper fallback nil wrapperRef with unhealthy prober",
			cms:    &mockCMS{},
			prober: &mockProber{healthy: false, msg: "fallback health probe failed"},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3P, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name:   "3P wrapper handle3PUnreadyStatus CMS error",
			cms:    &mockCMS{obsErr: errors.New("cms update failed")},
			prober: &mockProber{status: &ResourceStatus{Found: false, Ready: false}},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef, Enabled: true,
			},
			wantErr: true,
		},
		{
			name:   "3P wrapper handle3PReadyStatus CMS error",
			cms:    &mockCMS{obsErr: errors.New("cms update failed")},
			prober: &mockProber{status: &ResourceStatus{Found: true, Ready: true}},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef, Enabled: true,
			},
			wantErr: true,
		},
		{
			name: "update observed CMS error",
			cms:  &mockCMS{obsErr: errors.New("timeout")},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3P,
				Enabled: true,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var p StatusDetectorHealthProber
			if tt.prober != nil {
				p = tt.prober
			}
			var helmMgr HelmManager
			if tt.helm != nil {
				helmMgr = tt.helm
			}
			r, _ := NewReconciler(tt.cms, p, helmMgr)
			res, err := r.ReconcileComponent(ctx, tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ReconcileComponent() error = %v, wantErr %v", err, tt.wantErr)
			}
			if res == nil {
				t.Fatal("expected non-nil ReconcileResult")
			}
			if err == nil {
				if res.State != tt.wantState {
					t.Errorf("got state %v, want %v", res.State, tt.wantState)
				}
				if res.RemoveFinalizer != tt.wantRemoveFin {
					t.Errorf("got RemoveFinalizer %v, want %v", res.RemoveFinalizer, tt.wantRemoveFin)
				}
			}
			if tt.name == "intent application CMS error" && tt.helm != nil && !tt.helm.active {
				t.Errorf("expected 1P component to remain active on transient CMS intent failure")
			}
			if (tt.name == "deletion 1P Helm delete failure" || tt.name == "deletion CMS failure") && len(res.Conditions) == 0 {
				t.Errorf("expected conditions to be populated on deletion failure, got empty conditions")
			}
			if tt.name == "active 3P with coordinate injection" {
				assertCoordinateEnvVars(t, res.Access)
			}
			verify3PStatusDetectorAssertions(t, tt.name, tt.cms, tt.in.Generation, res, err)
			if tt.wantHealth != nil {
				if diff := cmp.Diff(*tt.wantHealth, extractHealthConditions(res.Conditions)); diff != "" {
					t.Errorf("health conditions mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}

// TestReconcileComponent_AsyncUpgradeReportsPendingUntilComplete guards against reporting Ready=True from the
// previously deployed revision while an async upgrade for a new generation is still in flight.
func TestReconcileComponent_AsyncUpgradeReportsPendingUntilComplete(t *testing.T) {
	ctx := context.Background()
	gate := make(chan struct{})
	runner := &mockHelmActionRunner{
		upgradeGate:    gate,
		upgradeStarted: make(chan struct{}, 1),
		installed:      map[string]bool{componentKey("xmc", "xmc-cmp-ingestion"): true}, // Generation 1 is deployed.
	}
	mgr := NewK8sHelmManager(fake.NewClientset(), "xmc", "xmanager-cloud", WithActionRunner(runner))
	r, err := NewReconciler(&mockCMS{}, nil, mgr)
	if err != nil {
		t.Fatalf("NewReconciler() failed: %v", err)
	}
	in := ReconcileComponentInput{
		Namespace: "xmc", Name: "ingestion", Generation: 2,
		Spec:    &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT},
		Enabled: true,
		ExistingConditions: []Condition{
			{Type: ConditionTypeReady, Status: ConditionStatusTrue, ObservedGeneration: 1, Reason: ReasonComponentHealthy},
		},
	}

	res, err := r.ReconcileComponent(ctx, in)
	if err != nil {
		t.Fatalf("ReconcileComponent() while upgrade in flight failed: %v", err)
	}
	wantPending := healthConditions{ReadyStatus: ConditionStatusFalse, ReconcilingStatus: ConditionStatusTrue, Reason: ReasonHelmUpgradePending}
	if diff := cmp.Diff(wantPending, extractHealthConditions(res.Conditions)); diff != "" {
		t.Errorf("health conditions while upgrade in flight mismatch (-want +got):\n%s", diff)
	}
	if res.State != componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION {
		t.Errorf("state while upgrade in flight = %v, want PENDING_RECONCILIATION", res.State)
	}

	close(gate)
	<-runner.upgradeStarted // Close drops queued upgrades, so wait until this one is in flight.
	_ = mgr.Close()         // Waits for the worker to finish the in-flight upgrade.
	res, err = r.ReconcileComponent(ctx, in)
	if err != nil {
		t.Fatalf("ReconcileComponent() after upgrade failed: %v", err)
	}
	wantHealthy := healthConditions{ReadyStatus: ConditionStatusTrue, Reason: ReasonComponentHealthy}
	if diff := cmp.Diff(wantHealthy, extractHealthConditions(res.Conditions)); diff != "" {
		t.Errorf("health conditions after upgrade mismatch (-want +got):\n%s", diff)
	}
	if got := runner.upgradeCalls; got != 1 {
		t.Errorf("upgradeCalls = %d, want 1 (second pass must not re-upgrade)", got)
	}
}

// TestReconcileComponent_DriftedReleaseIsRepairedAtSameGeneration guards against a component staying Ready while
// objects of its Helm release were deleted out of band: the bridge must report drift, then re-apply the release
// on the next pass without waiting for a new CR generation.
func TestReconcileComponent_DriftedReleaseIsRepairedAtSameGeneration(t *testing.T) {
	ctx := context.Background()
	runner := &mockHelmActionRunner{
		installed: map[string]bool{componentKey("xmc", "xmc-cmp-ingestion"): true},
		missing:   []string{"Role/ingestion-components-reader"},
	}
	mgr := NewK8sHelmManager(fake.NewClientset(), "xmc", "xmanager-cloud", WithActionRunner(runner), WithSyncExecution(true))
	r, err := NewReconciler(&mockCMS{}, nil, mgr)
	if err != nil {
		t.Fatalf("NewReconciler() failed: %v", err)
	}
	in := ReconcileComponentInput{
		Namespace: "xmc", Name: "ingestion", Generation: 1,
		Spec:    &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT},
		Enabled: true,
		ExistingConditions: []Condition{
			{Type: ConditionTypeReady, Status: ConditionStatusTrue, ObservedGeneration: 1, Reason: ReasonComponentHealthy},
		},
	}

	res, err := r.ReconcileComponent(ctx, in)
	if err != nil {
		t.Fatalf("ReconcileComponent() detecting drift failed: %v", err)
	}
	wantDrifted := healthConditions{ReadyStatus: ConditionStatusFalse, ReconcilingStatus: ConditionStatusTrue, Reason: ReasonHelmReleaseDrifted}
	if diff := cmp.Diff(wantDrifted, extractHealthConditions(res.Conditions)); diff != "" {
		t.Errorf("health conditions on drift detection mismatch (-want +got):\n%s", diff)
	}
	if got := runner.upgradeCalls; got != 0 {
		t.Errorf("upgradeCalls after drift detection = %d, want 0", got)
	}

	in.ExistingConditions = res.Conditions
	res, err = r.ReconcileComponent(ctx, in)
	if err != nil {
		t.Fatalf("ReconcileComponent() repairing drift failed: %v", err)
	}
	wantHealthy := healthConditions{ReadyStatus: ConditionStatusTrue, Reason: ReasonComponentHealthy}
	if diff := cmp.Diff(wantHealthy, extractHealthConditions(res.Conditions)); diff != "" {
		t.Errorf("health conditions after repair mismatch (-want +got):\n%s", diff)
	}
	if got := runner.upgradeCalls; got != 1 {
		t.Errorf("upgradeCalls after repair = %d, want 1 (repair must re-upgrade at the same generation)", got)
	}
}

// TestReconcileComponent_DriftRepairBypassesDependencyHold guards against a drifted component with an unready
// dependency being held as HelmUpgradePending (hiding the drift): repairing the already-actuated generation re-applies
// an approved spec, so it is not held, and DependenciesReady=False is still reported.
func TestReconcileComponent_DriftRepairBypassesDependencyHold(t *testing.T) {
	ctx := context.Background()
	runner := &mockHelmActionRunner{
		installed: map[string]bool{componentKey("xmc", "xmc-cmp-ingestion"): true},
		missing:   []string{"Role/ingestion-components-reader"},
	}
	mgr := NewK8sHelmManager(fake.NewClientset(), "xmc", "xmanager-cloud", WithActionRunner(runner), WithSyncExecution(true))
	defer mgr.Close()
	r, err := NewReconciler(&mockCMS{}, nil, mgr)
	if err != nil {
		t.Fatalf("NewReconciler() failed: %v", err)
	}
	unready := NewDependencySnapshot([]unstructured.Unstructured{readyComponent("redpanda", func(u *unstructured.Unstructured) { setConditionStatus(u, 0, "status", ConditionStatusFalse) })})
	in := ReconcileComponentInput{
		Namespace: "xmc", Name: "ingestion", Generation: 1,
		Spec:    &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, Dependencies: []string{"redpanda"}},
		Enabled: true, Dependencies: unready,
		ExistingConditions: []Condition{
			{Type: ConditionTypeReady, Status: ConditionStatusTrue, ObservedGeneration: 1, Reason: ReasonComponentHealthy},
		},
	}
	type outcome struct {
		Health            healthConditions
		DependenciesReady string
		UpgradeCalls      int
	}
	passes := []outcome{
		{Health: healthConditions{ReadyStatus: ConditionStatusFalse, ReconcilingStatus: ConditionStatusTrue, Reason: ReasonHelmReleaseDrifted}, DependenciesReady: ConditionStatusFalse, UpgradeCalls: 0},
		{Health: healthConditions{ReadyStatus: ConditionStatusTrue, Reason: ReasonComponentHealthy}, DependenciesReady: ConditionStatusFalse, UpgradeCalls: 1},
	}
	for i, want := range passes {
		res, err := r.ReconcileComponent(ctx, in)
		if err != nil {
			t.Fatalf("pass %d: ReconcileComponent() failed: %v", i, err)
		}
		got := outcome{Health: extractHealthConditions(res.Conditions), UpgradeCalls: runner.upgradeCalls}
		if c := findCondition(res.Conditions, ConditionTypeDependenciesReady); c != nil {
			got.DependenciesReady = c.Status
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("pass %d: outcome mismatch (-want +got):\n%s", i, diff)
		}
		in.ExistingConditions = res.Conditions
	}
}

// TestReconcileComponent_RepairOnlyBypassesHoldForActuatedGeneration verifies that a pending repair lets only the
// already-actuated generation through an unready-dependency hold; a new generation is still held.
func TestReconcileComponent_RepairOnlyBypassesHoldForActuatedGeneration(t *testing.T) {
	unready := NewDependencySnapshot([]unstructured.Unstructured{readyComponent("redpanda", func(u *unstructured.Unstructured) { setConditionStatus(u, 0, "status", ConditionStatusFalse) })})
	tests := []struct {
		name       string
		generation int64
		wantReason string
		wantEnsure int
	}{
		{name: "repair of actuated generation is not held", generation: 1, wantReason: ReasonComponentHealthy, wantEnsure: 1},
		{name: "new generation is held", generation: 2, wantReason: ReasonHelmUpgradePending, wantEnsure: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helm := &mockHelm{active: true, repairNeeded: true, actuatedGenerations: map[string]int64{"xmc/ingestion": 1}}
			r, err := NewReconciler(&mockCMS{}, nil, helm)
			if err != nil {
				t.Fatalf("NewReconciler() failed: %v", err)
			}
			res, err := r.ReconcileComponent(context.Background(), ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: tt.generation, Enabled: true,
				Spec:         &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, Dependencies: []string{"redpanda"}},
				Dependencies: unready,
			})
			if err != nil {
				t.Fatalf("ReconcileComponent() failed: %v", err)
			}
			if got := extractHealthConditions(res.Conditions).Reason; got != tt.wantReason {
				t.Errorf("Ready reason = %q, want %q", got, tt.wantReason)
			}
			if helm.ensureCalls != tt.wantEnsure {
				t.Errorf("Ensure1PComponent calls = %d, want %d", helm.ensureCalls, tt.wantEnsure)
			}
		})
	}
}

// healthConditions captures the Ready/Reconciling/Stalled condition statuses and the Ready reason set by evaluateHealth.
type healthConditions struct {
	ReadyStatus       string
	ReconcilingStatus string
	StalledStatus     string
	Reason            string
}

func extractHealthConditions(conds []Condition) healthConditions {
	var got healthConditions
	for _, c := range conds {
		switch c.Type {
		case ConditionTypeReady:
			got.ReadyStatus, got.Reason = c.Status, c.Reason
		case ConditionTypeReconciling:
			got.ReconcilingStatus = c.Status
		case ConditionTypeStalled:
			got.StalledStatus = c.Status
		}
	}
	return got
}

func assertCoordinateEnvVars(t *testing.T, access *AccessStatus) {
	t.Helper()
	if access == nil || access.EnvVars["ENDPOINT"] != "http://chi-prod.analytics.svc.cluster.local:8123" {
		t.Fatalf("expected access.EnvVars[ENDPOINT] populated, got %+v", access)
	}
	if access.EnvVars["HOST"] != "chi-prod.analytics.svc.cluster.local" {
		t.Errorf("expected HOST populated, got %v", access.EnvVars["HOST"])
	}
	if access.EnvVars["PORT"] != "8123" {
		t.Errorf("expected PORT populated, got %v", access.EnvVars["PORT"])
	}
	if access.EnvVars["SCHEME"] != "http" {
		t.Errorf("expected SCHEME populated, got %v", access.EnvVars["SCHEME"])
	}
	if access.EnvVars["SECRET_NAME"] != "clickhouse-credentials" {
		t.Errorf("expected SECRET_NAME populated, got %v", access.EnvVars["SECRET_NAME"])
	}
}

func assertConditionStatus(t *testing.T, conds []Condition, cType string, wantStatus string) {
	t.Helper()
	for _, c := range conds {
		if c.Type == cType {
			if c.Status != wantStatus {
				t.Errorf("condition %v: got status %v, want %v", cType, c.Status, wantStatus)
			}
			return
		}
	}
	t.Errorf("condition %v not found", cType)
}

func verify3PStatusDetectorAssertions(t *testing.T, testName string, cms *mockCMS, gen int64, res *ReconcileResult, err error) {
	t.Helper()
	if !strings.HasPrefix(testName, "active 3P with status detector") {
		return
	}
	if testName == "active 3P with status detector ready" {
		assertConditionStatus(t, res.Conditions, ConditionTypeReady, ConditionStatusTrue)
		return
	}

	assertConditionStatus(t, res.Conditions, ConditionTypeReady, ConditionStatusFalse)
	if testName == "active 3P with status detector resource not found" {
		assertConditionStatus(t, res.Conditions, ConditionTypeStalled, ConditionStatusTrue)
	} else {
		assertConditionStatus(t, res.Conditions, ConditionTypeReconciling, ConditionStatusTrue)
	}
	if res.State != componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION {
		t.Errorf("%s: expected state %v, got %v", testName, componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION, res.State)
	}
	if testName == "active 3P with status detector error and CMS failure" {
		if err == nil || !strings.Contains(err.Error(), "CMS update failed") {
			t.Errorf("expected error to mention CMS update failure, got: %v", err)
		}
		return
	}
	if cms == nil || cms.obsErr != nil {
		return
	}
	if cms.lastObsReq == nil {
		t.Errorf("%s: expected UpdateObservedGeneration to be called on CMS", testName)
		return
	}
	if cms.lastObsReq.ObservedGeneration != gen {
		t.Errorf("%s: expected CMS observed generation %d, got %d", testName, gen, cms.lastObsReq.ObservedGeneration)
	}
	if cms.lastObsReq.State != componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION {
		t.Errorf("%s: expected CMS state %v, got %v", testName, componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION, cms.lastObsReq.State)
	}
}

func TestReconcile_Unstructured(t *testing.T) {
	r, _ := NewReconciler(&mockCMS{}, nil, nil)
	u := &unstructured.Unstructured{
		Object: map[string]any{
			"metadata": map[string]any{
				"name":       "test-comp",
				"namespace":  "xmc",
				"generation": int64(1),
			},
			"spec": map[string]any{
				"enabled":       true,
				"componentType": "COMPONENT_TYPE_CUSTOM_1P_COMPONENT",
			},
		},
	}
	res, err := r.Reconcile(context.Background(), u)
	if err != nil || res == nil || res.State != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
		t.Fatalf("unexpected Reconcile outcome: res=%+v, err=%v", res, err)
	}
}

func TestDropSelfDependency(t *testing.T) {
	tests := []struct {
		name string
		deps []string
		want []string
	}{
		{name: "nil", deps: nil, want: nil},
		{name: "no self reference", deps: []string{"redpanda", "auth"}, want: []string{"redpanda", "auth"}},
		{name: "self reference dropped", deps: []string{"redpanda", "ingestion-gateway", "auth"}, want: []string{"redpanda", "auth"}},
		{name: "only self reference", deps: []string{"ingestion-gateway"}, want: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := dropSelfDependency(context.Background(), tc.deps, "ingestion-gateway")
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("dropSelfDependency() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestReconcileComponent_1P_PopulatesEmptyHelmOptsRef(t *testing.T) {
	ctx := context.Background()
	helm := &mockHelm{}
	r, err := NewReconciler(&mockCMS{}, nil, helm)
	if err != nil {
		t.Fatalf("NewReconciler failed: %v", err)
	}

	in := ReconcileComponentInput{
		Namespace: "custom-ns",
		Name:      "custom-comp",
		Enabled:   true,
		Spec: &componentspb.ComponentSpec{
			Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		},
		HelmOpts: HelmComponentOptions{
			// Ref intentionally empty
		},
	}

	res := &ReconcileResult{}
	if _, err := r.ensure1PComponent(ctx, in, res, false); err != nil {
		t.Fatalf("ensure1PComponent failed: %v", err)
	}

	wantRef := ComponentRef{Namespace: "custom-ns", Name: "custom-comp"}
	if helm.lastRef != wantRef {
		t.Errorf("expected lastRef %v, got %v", wantRef, helm.lastRef)
	}
	if helm.lastOpts.Ref != wantRef {
		t.Errorf("expected lastOpts.Ref %v, got %v", wantRef, helm.lastOpts.Ref)
	}
}

func TestResolveComponentRef(t *testing.T) {
	tests := []struct {
		name    string
		in      ReconcileComponentInput
		wantRef ComponentRef
	}{
		{
			name: "both name and namespace empty in HelmOpts",
			in: ReconcileComponentInput{
				Namespace: "default-ns",
				Name:      "default-name",
				HelmOpts:  HelmComponentOptions{},
			},
			wantRef: ComponentRef{Namespace: "default-ns", Name: "default-name"},
		},
		{
			name: "ref name set but namespace empty in HelmOpts",
			in: ReconcileComponentInput{
				Namespace: "fallback-ns",
				Name:      "original-name",
				HelmOpts: HelmComponentOptions{
					Ref: ComponentRef{Name: "custom-name"},
				},
			},
			wantRef: ComponentRef{Namespace: "fallback-ns", Name: "custom-name"},
		},
		{
			name: "ref namespace set but name empty in HelmOpts",
			in: ReconcileComponentInput{
				Namespace: "original-ns",
				Name:      "fallback-name",
				HelmOpts: HelmComponentOptions{
					Ref: ComponentRef{Namespace: "custom-ns"},
				},
			},
			wantRef: ComponentRef{Namespace: "custom-ns", Name: "fallback-name"},
		},
		{
			name: "both ref name and namespace set in HelmOpts",
			in: ReconcileComponentInput{
				Namespace: "ignored-ns",
				Name:      "ignored-name",
				HelmOpts: HelmComponentOptions{
					Ref: ComponentRef{Namespace: "custom-ns", Name: "custom-name"},
				},
			},
			wantRef: ComponentRef{Namespace: "custom-ns", Name: "custom-name"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveComponentRef(tc.in)
			if got != tc.wantRef {
				t.Errorf("resolveComponentRef() = %+v, want %+v", got, tc.wantRef)
			}
		})
	}
}

func TestReconcileComponent_1P_GenerationGating(t *testing.T) {
	ctx := context.Background()
	cms := &mockCMS{}
	prober := &mockProber{healthy: true}
	helm := &mockHelm{}
	r, err := NewReconciler(cms, prober, helm)
	if err != nil {
		t.Fatalf("failed to create reconciler: %v", err)
	}

	spec1P := &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT}
	in := ReconcileComponentInput{
		Namespace:  "xmc",
		Name:       "ingestion-gateway",
		Generation: 1,
		Spec:       spec1P,
		Enabled:    true,
	}

	// First pass (generation 1): should actuate Helm upgrade
	res1, err := r.ReconcileComponent(ctx, in)
	if err != nil {
		t.Fatalf("first ReconcileComponent failed: %v", err)
	}
	if res1.State != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
		t.Errorf("expected state ENABLED, got %v", res1.State)
	}
	if helm.ensureCalls != 1 {
		t.Errorf("expected 1 ensureCall on initial deploy, got %d", helm.ensureCalls)
	}

	// Periodic pass (generation 1 unchanged): should skip Helm upgrade completely
	res2, err := r.ReconcileComponent(ctx, in)
	if err != nil {
		t.Fatalf("periodic ReconcileComponent failed: %v", err)
	}
	if res2.State != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
		t.Errorf("expected state ENABLED, got %v", res2.State)
	}
	if helm.ensureCalls != 1 {
		t.Errorf("expected ensureCalls to remain 1 (skipped on unchanged generation), got %d", helm.ensureCalls)
	}

	// Spec update pass (generation 2): should actuate Helm upgrade again
	in.Generation = 2
	res3, err := r.ReconcileComponent(ctx, in)
	if err != nil {
		t.Fatalf("spec update ReconcileComponent failed: %v", err)
	}
	if res3.State != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
		t.Errorf("expected state ENABLED, got %v", res3.State)
	}
	if helm.ensureCalls != 2 {
		t.Errorf("expected 2 ensureCalls after generation update, got %d", helm.ensureCalls)
	}

	// Periodic pass (generation 2 unchanged): should skip Helm upgrade again
	res4, err := r.ReconcileComponent(ctx, in)
	if err != nil {
		t.Fatalf("periodic ReconcileComponent at gen 2 failed: %v", err)
	}
	if res4.State != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
		t.Errorf("expected state ENABLED, got %v", res4.State)
	}
	if helm.ensureCalls != 2 {
		t.Errorf("expected ensureCalls to remain 2 (skipped on unchanged generation), got %d", helm.ensureCalls)
	}
}

func TestReconciler_ColdStartHydration_SkipsRedundantUpgrade(t *testing.T) {
	ctx := context.Background()
	helm := &mockHelm{active: false}
	cms := &mockCMS{}
	r, err := NewReconciler(cms, nil, helm)
	if err != nil {
		t.Fatalf("NewReconciler failed: %v", err)
	}

	in := ReconcileComponentInput{
		Namespace:  "xmc",
		Name:       "ingestion-gateway",
		Generation: 4,
		Spec:       &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT},
		Enabled:    true,
		ExistingConditions: []Condition{
			{
				Type:               ConditionTypeReady,
				Status:             ConditionStatusTrue,
				ObservedGeneration: 4,
				Reason:             "ComponentHealthy",
				LastTransitionTime: time.Now().UTC(),
			},
		},
	}

	res, err := r.ReconcileComponent(ctx, in)
	if err != nil {
		t.Fatalf("ReconcileComponent failed: %v", err)
	}
	if res.State != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
		t.Errorf("expected COMPONENT_STATE_ENABLED, got %v", res.State)
	}
	if helm.ensureCalls != 0 {
		t.Errorf("expected 0 Ensure1PComponent calls after cold-start hydration of Ready@gen4, got %d", helm.ensureCalls)
	}
}

func TestPreserveHealthConditions_ReplacesExistingEntries(t *testing.T) {
	ready := Condition{Type: ConditionTypeReady, Status: ConditionStatusFalse, ObservedGeneration: 3, Reason: ReasonHelmReleaseNotReady}
	reconciling := Condition{Type: ConditionTypeReconciling, Status: ConditionStatusTrue, ObservedGeneration: 3, Reason: ReasonProgressing}
	in := ReconcileComponentInput{Generation: 3, ExistingConditions: []Condition{ready, reconciling}}
	res := &ReconcileResult{Conditions: []Condition{{Type: ConditionTypeReady, Status: ConditionStatusFalse, Reason: "Stale"}}}

	if !preserveHealthConditions(in, res) {
		t.Fatal("preserveHealthConditions() = false, want true")
	}
	if diff := cmp.Diff([]Condition{ready, reconciling}, res.Conditions); diff != "" {
		t.Errorf("preserveHealthConditions() conditions mismatch (-want +got):\n%s", diff)
	}
}

func TestReconcileComponent_DependencyActivationGating(t *testing.T) {
	spec1P := func(deps ...string) *componentspb.ComponentSpec {
		return &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, Dependencies: deps}
	}
	spec3P := &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER, Dependencies: []string{"redpanda"}}
	snapshot := func(items ...unstructured.Unstructured) DependencyReadiness { return NewDependencySnapshot(items) }
	notRegisteredErr := dependencyNotRegisteredErr(t, "ingestion", "redpanda")
	// Intent is registered with CMS even while activation is gated.
	gated := gateOutcome{
		State:       componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		ReadyStatus: ConditionStatusFalse, ReadyReason: ReasonDependenciesNotReady, ReconcilingStatus: ConditionStatusTrue,
		SetCalls: 1,
	}
	actuated := gateOutcome{
		State:       componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		ReadyStatus: ConditionStatusTrue, ReadyReason: ReasonComponentHealthy,
		DependenciesReady: ConditionStatusTrue, SetCalls: 1, EnsureCalls: 1,
	}
	withoutDepsCondition := func(o gateOutcome) gateOutcome { o.DependenciesReady = ""; return o }
	// running is a release actuated at gen that passed the gate; the CR is at generation 2.
	running := func(gen int64) *mockHelm {
		return &mockHelm{active: true, actuatedGenerations: map[string]int64{"xmc/ingestion": gen}}
	}
	runningConds := func(gen int64) []Condition {
		return []Condition{
			{Type: ConditionTypeReady, Status: ConditionStatusTrue, ObservedGeneration: gen, Reason: ReasonComponentHealthy},
			{Type: ConditionTypeDependenciesReady, Status: ConditionStatusTrue, ObservedGeneration: gen, Reason: "AllDependenciesReady"},
		}
	}
	// upgradeHeld keeps the running workload but holds the Helm upgrade to the new generation.
	upgradeHeld := gateOutcome{
		State:       componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		ReadyStatus: ConditionStatusFalse, ReadyReason: ReasonHelmUpgradePending, ReconcilingStatus: ConditionStatusTrue,
		DependenciesReady: ConditionStatusFalse, SetCalls: 1,
	}
	// keptReady has nothing to upgrade, so Ready stays health-derived while dependencies are unready.
	keptReady := gateOutcome{
		State:       componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		ReadyStatus: ConditionStatusTrue, ReadyReason: ReasonComponentHealthy,
		DependenciesReady: ConditionStatusFalse, SetCalls: 1,
	}
	tests := []struct {
		name     string
		cms      *mockCMS
		helm     *mockHelm
		spec     *componentspb.ComponentSpec
		deps     DependencyReadiness
		existing []Condition
		want     gateOutcome
		wantMsg  string
		wantErr  bool
	}{
		{name: "all dependencies ready actuates", spec: spec1P("redpanda"), deps: snapshot(readyComponent("redpanda", nil)), want: actuated},
		{name: "nil resolver disables gating", spec: spec1P("redpanda"), deps: nil, want: withoutDepsCondition(actuated)},
		{name: "no dependencies declared", spec: spec1P(), deps: snapshot(), want: withoutDepsCondition(actuated)},
		{name: "dependency missing", spec: spec1P("redpanda"), deps: snapshot(), want: gated, wantMsg: "waiting on dependencies: redpanda (not found)"},
		{
			name:    "dependency disabled",
			spec:    spec1P("redpanda"),
			deps:    snapshot(readyComponent("redpanda", func(u *unstructured.Unstructured) { u.Object["spec"] = map[string]any{"enabled": false} })),
			want:    gated,
			wantMsg: "waiting on dependencies: redpanda (disabled)",
		},
		{
			name:    "dependency Ready=False",
			spec:    spec1P("redpanda"),
			deps:    snapshot(readyComponent("redpanda", func(u *unstructured.Unstructured) { setConditionStatus(u, 0, "status", ConditionStatusFalse) })),
			want:    gated,
			wantMsg: "waiting on dependencies: redpanda (not ready)",
		},
		{
			name:    "dependency Ready at stale generation",
			spec:    spec1P("redpanda"),
			deps:    snapshot(readyComponent("redpanda", func(u *unstructured.Unstructured) { u.SetGeneration(3) })),
			want:    gated,
			wantMsg: "waiting on dependencies: redpanda (not ready at current generation)",
		},
		{
			name: "lists every unready dependency in declaration order",
			spec: spec1P("redpanda", "clickhouse", "foo"),
			deps: snapshot(
				readyComponent("redpanda", func(u *unstructured.Unstructured) { u.Object["spec"] = map[string]any{"enabled": false} }),
				readyComponent("clickhouse", nil),
			),
			want:    gated,
			wantMsg: "waiting on dependencies: redpanda (disabled), foo (not found)",
		},
		{name: "3P wrapper is gated after registering intent", spec: spec3P, deps: snapshot(), want: gated},
		{
			name: "CMS NotFound while gated is tolerated",
			cms:  &mockCMS{obsErr: status.Error(codes.NotFound, "component not found")},
			spec: spec1P("redpanda"), deps: snapshot(), want: gated,
		},
		{
			name: "CMS failure while gated is surfaced",
			cms:  &mockCMS{obsErr: status.Error(codes.Unavailable, "cms down")},
			spec: spec1P("redpanda"), deps: snapshot(), want: gated, wantErr: true,
		},
		{
			name: "CMS unregistered dependency gates without error",
			cms:  &mockCMS{setErr: notRegisteredErr},
			spec: spec1P("redpanda"), deps: snapshot(readyComponent("redpanda", nil)), want: gated,
			wantMsg: "waiting on dependencies: " + status.Convert(notRegisteredErr).Message(),
		},
		{
			name: "CMS unregistered dependency gates without a resolver",
			cms:  &mockCMS{setErr: notRegisteredErr},
			spec: spec1P("redpanda"), deps: nil, want: gated,
		},
		{
			name: "other intent rejection is surfaced",
			cms:  &mockCMS{setErr: status.Error(codes.FailedPrecondition, "invalid state transition")},
			spec: spec1P("redpanda"), deps: snapshot(readyComponent("redpanda", nil)),
			want: gateOutcome{
				State:       componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
				ReadyStatus: ConditionStatusUnknown, ReadyReason: ReasonInitialSyncPending, ReconcilingStatus: ConditionStatusTrue, SetCalls: 1,
			},
			wantErr: true,
		},
		{
			name: "running release holds upgrade to a new generation while a dependency is unready",
			helm: running(1), spec: spec1P("redpanda"), deps: snapshot(), existing: runningConds(1), want: upgradeHeld,
			wantMsg: "Helm upgrade to generation 2 is held until dependencies are ready",
		},
		{
			name: "running release at the current generation keeps Ready while a dependency is unready",
			helm: running(2), spec: spec1P("redpanda"), deps: snapshot(), existing: runningConds(2), want: keptReady,
		},
		{
			name: "running release is upgraded once dependencies are ready",
			helm: running(1), spec: spec1P("redpanda"), deps: snapshot(readyComponent("redpanda", nil)), existing: runningConds(1), want: actuated,
		},
		{
			name: "running release holds upgrade while CMS has not registered a dependency",
			cms:  &mockCMS{setErr: notRegisteredErr},
			helm: running(1), spec: spec1P("redpanda"), deps: nil, existing: runningConds(1), want: upgradeHeld,
		},
		{
			name: "running 3P wrapper keeps Ready while a dependency is unready",
			spec: spec3P, deps: snapshot(),
			existing: []Condition{{Type: ConditionTypeReady, Status: ConditionStatusTrue, ObservedGeneration: 1, Reason: ReasonComponentHealthy}},
			want:     keptReady,
		},
		{
			name: "previously gated component stays gated",
			spec: spec1P("redpanda"), deps: snapshot(),
			existing: []Condition{{Type: ConditionTypeReady, Status: ConditionStatusFalse, ObservedGeneration: 2, Reason: ReasonDependenciesNotReady}},
			want:     gated,
		},
		{
			name: "dependency cycle is reported without registering intent",
			spec: spec1P("redpanda"),
			deps: snapshot(readyComponent("ingestion", withDependencies("redpanda")), readyComponent("redpanda", withDependencies("ingestion"))),
			want: gateOutcome{
				State:       componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
				ReadyStatus: ConditionStatusFalse, ReadyReason: ReasonDependencyCycle, StalledStatus: ConditionStatusTrue,
			},
			wantMsg: "dependency cycle detected: ingestion -> redpanda -> ingestion",
		},
		{
			name: "self-dependency is dropped and does not gate",
			spec: spec1P("ingestion"),
			deps: snapshot(readyComponent("ingestion", withDependencies("ingestion"))),
			want: withoutDepsCondition(actuated),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cms, helm := tt.cms, tt.helm
			if cms == nil {
				cms = &mockCMS{}
			}
			if helm == nil {
				helm = &mockHelm{}
			}
			wasActive := helm.active
			r, _ := NewReconciler(cms, nil, helm)
			res, err := r.ReconcileComponent(context.Background(), ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 2, Spec: tt.spec,
				Enabled: true, ExistingConditions: tt.existing, Dependencies: tt.deps,
			})
			if (err != nil) != tt.wantErr {
				t.Fatalf("ReconcileComponent() error = %v, wantErr %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.want, summarizeGate(res, cms, helm)); diff != "" {
				t.Errorf("ReconcileComponent() outcome mismatch (-want +got):\n%s", diff)
			}
			checkReadyMessage(t, res, tt.wantMsg)
			if wasActive && !helm.active {
				t.Error("ReconcileComponent() tore down an already-running release")
			}
		})
	}
}

// TestReconcileComponent_DependencyBlipHoldsUpgrade drives one component across passes: actuated,
// a dependency blip at the same generation (Ready kept), a spec change while the dependency is
// unready (upgrade held with HelmUpgradePending), then recovery.
func TestReconcileComponent_DependencyBlipHoldsUpgrade(t *testing.T) {
	cms, helm := &mockCMS{}, &mockHelm{}
	r, _ := NewReconciler(cms, nil, helm)
	ready := NewDependencySnapshot([]unstructured.Unstructured{readyComponent("redpanda", nil)})
	unready := NewDependencySnapshot([]unstructured.Unstructured{readyComponent("redpanda", func(u *unstructured.Unstructured) { setConditionStatus(u, 0, "status", ConditionStatusFalse) })})
	healthy := gateOutcome{State: componentspb.ComponentState_COMPONENT_STATE_ENABLED, ReadyStatus: ConditionStatusTrue, ReadyReason: ReasonComponentHealthy}
	pending := gateOutcome{State: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION, ReadyStatus: ConditionStatusFalse, ReadyReason: ReasonHelmUpgradePending, ReconcilingStatus: ConditionStatusTrue}
	with := func(o gateOutcome, depsReady string, setCalls, ensureCalls int) gateOutcome {
		o.DependenciesReady, o.SetCalls, o.EnsureCalls = depsReady, setCalls, ensureCalls
		return o
	}
	passes := []struct {
		generation int64
		deps       DependencyReadiness
		want       gateOutcome
	}{
		{generation: 2, deps: ready, want: with(healthy, ConditionStatusTrue, 1, 1)},
		{generation: 2, deps: unready, want: with(healthy, ConditionStatusFalse, 2, 1)},
		{generation: 3, deps: unready, want: with(pending, ConditionStatusFalse, 3, 1)},
		{generation: 3, deps: ready, want: with(healthy, ConditionStatusTrue, 4, 2)},
	}
	var existing []Condition
	for i, p := range passes {
		res, err := r.ReconcileComponent(context.Background(), ReconcileComponentInput{
			Namespace: "xmc", Name: "ingestion", Generation: p.generation, Enabled: true,
			Spec:               &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, Dependencies: []string{"redpanda"}},
			ExistingConditions: existing, Dependencies: p.deps,
		})
		if err != nil {
			t.Fatalf("pass %d: ReconcileComponent() error = %v", i, err)
		}
		if diff := cmp.Diff(p.want, summarizeGate(res, cms, helm)); diff != "" {
			t.Errorf("pass %d: ReconcileComponent() outcome mismatch (-want +got):\n%s", i, diff)
		}
		if !helm.active {
			t.Fatalf("pass %d: release is not active", i)
		}
		existing = res.Conditions
	}
}

// errorInfoStatus returns a status error with code and msg carrying a google.rpc.ErrorInfo with
// reason and domain, mirroring the store's machine-readable rejections.
func errorInfoStatus(t *testing.T, code codes.Code, reason, domain, msg string) error {
	t.Helper()
	return errorInfoStatusWithMetadata(t, code, reason, domain, msg, nil)
}

// errorInfoStatusWithMetadata is errorInfoStatus with ErrorInfo metadata.
func errorInfoStatusWithMetadata(t *testing.T, code codes.Code, reason, domain, msg string, metadata map[string]string) error {
	t.Helper()
	st, err := status.New(code, msg).WithDetails(&errdetails.ErrorInfo{Reason: reason, Domain: domain, Metadata: metadata})
	if err != nil {
		t.Fatalf("status.WithDetails() error = %v", err)
	}
	return st.Err()
}

// dependencyNotRegisteredErr returns the store's DEPENDENCY_NOT_REGISTERED rejection of component
// depending on the unregistered dep.
func dependencyNotRegisteredErr(t *testing.T, component, dep string) error {
	t.Helper()
	return errorInfoStatus(t, codes.FailedPrecondition, store.ReasonDependencyNotRegistered, store.ErrorInfoDomain,
		fmt.Sprintf("component %q depends on %q, which is not registered", component, dep))
}

func TestDependencyNotRegisteredMessage(t *testing.T) {
	const msg = `component "a" depends on "b", which is not registered`
	notRegistered := errorInfoStatus(t, codes.FailedPrecondition, store.ReasonDependencyNotRegistered, store.ErrorInfoDomain, msg)
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "nil", err: nil, want: ""},
		{name: "unregistered dependency", err: notRegistered, want: msg},
		{name: "wrapped unregistered dependency", err: fmt.Errorf("failed to apply component intent to CMS: %w", notRegistered), want: msg},
		{name: "legacy message prefix without ErrorInfo", err: status.Error(codes.FailedPrecondition, "dependency not found: "+msg), want: ""},
		{name: "other reason", err: errorInfoStatus(t, codes.FailedPrecondition, store.ReasonStaleGeneration, store.ErrorInfoDomain, msg), want: ""},
		{name: "other domain", err: errorInfoStatus(t, codes.FailedPrecondition, store.ReasonDependencyNotRegistered, "example.com", msg), want: ""},
		{name: "other code with the reason", err: errorInfoStatus(t, codes.Unavailable, store.ReasonDependencyNotRegistered, store.ErrorInfoDomain, msg), want: ""},
		{name: "non-status error", err: errors.New(msg), want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dependencyNotRegisteredMessage(tt.err); got != tt.want {
				t.Errorf("dependencyNotRegisteredMessage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDependencyCycleMessage(t *testing.T) {
	const msg = "dependency cycle detected: a -> b -> a"
	cycle := errorInfoStatus(t, codes.FailedPrecondition, store.ReasonDependencyCycle, store.ErrorInfoDomain, msg)
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "nil", err: nil, want: ""},
		{name: "dependency cycle", err: cycle, want: msg},
		{name: "wrapped dependency cycle", err: fmt.Errorf("failed to apply component intent to CMS: %w", cycle), want: msg},
		{name: "message text without ErrorInfo", err: status.Error(codes.FailedPrecondition, msg), want: ""},
		{name: "other reason", err: errorInfoStatus(t, codes.FailedPrecondition, store.ReasonDependencyNotRegistered, store.ErrorInfoDomain, msg), want: ""},
		{name: "other domain", err: errorInfoStatus(t, codes.FailedPrecondition, store.ReasonDependencyCycle, "example.com", msg), want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dependencyCycleMessage(tt.err); got != tt.want {
				t.Errorf("dependencyCycleMessage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSetDependenciesReadyConditionMessage(t *testing.T) {
	const waitMsg = "waiting on dependencies: redpanda (not ready)"
	tests := []struct {
		name     string
		compType componentspb.ComponentType
		want     string
	}{
		{name: "1P mentions held Helm upgrades", compType: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, want: waitMsg + "; Helm upgrades are held, running workload is left in place"},
		{name: "3P wrapper omits Helm", compType: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER, want: waitMsg + "; running workload is left in place"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := &ReconcileResult{}
			setDependenciesReadyCondition(res, ReconcileComponentInput{Generation: 1, Spec: &componentspb.ComponentSpec{Type: tt.compType}}, waitMsg)
			c := findCondition(res.Conditions, ConditionTypeDependenciesReady)
			if c == nil || c.Status != ConditionStatusFalse || c.Message != tt.want {
				t.Errorf("setDependenciesReadyCondition() condition = %+v, want status %q message %q", c, ConditionStatusFalse, tt.want)
			}
		})
	}
}

// TestReconcileComponent_ErrorPassPreservesDependenciesReady guards isActuated: an error pass must not
// drop DependenciesReady, or an actuated but unhealthy 3P wrapper would be re-gated on the next pass.
func TestReconcileComponent_ErrorPassPreservesDependenciesReady(t *testing.T) {
	r, _ := NewReconciler(&mockCMS{setErr: status.Error(codes.Unavailable, "cms down")}, nil, &mockHelm{})
	prev := Condition{Type: ConditionTypeDependenciesReady, Status: ConditionStatusFalse, ObservedGeneration: 1, Reason: ReasonDependenciesNotReady}
	res, err := r.ReconcileComponent(context.Background(), ReconcileComponentInput{
		Namespace: "xmc", Name: "ingestion", Generation: 1, Enabled: true,
		Spec:               &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER, Dependencies: []string{"redpanda"}},
		ExistingConditions: []Condition{{Type: ConditionTypeReady, Status: ConditionStatusFalse, ObservedGeneration: 1}, prev},
		Dependencies:       NewDependencySnapshot(nil),
	})
	if err == nil {
		t.Fatal("ReconcileComponent() error = nil, want CMS error")
	}
	if diff := cmp.Diff(&prev, findCondition(res.Conditions, ConditionTypeDependenciesReady)); diff != "" {
		t.Errorf("ReconcileComponent() DependenciesReady mismatch (-want +got):\n%s", diff)
	}
}

const rayClusterTestCRD = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata: {name: rayclusters.ray.io}
spec:
  group: ray.io
  names: {kind: RayCluster, listKind: RayClusterList, plural: rayclusters, singular: raycluster}
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
      subresources: {status: {}}
      schema: {openAPIV3Schema: {type: object, x-kubernetes-preserve-unknown-fields: true}}
`

// TestReconciler_EnvtestIntegration verifies controller reconciliation, finalizer attachment,
// Server-Side Apply condition merging (fieldManager: "xmc-bridge"), and malformed 3P status
// contract breach handling (ReasonContractBroken) against a local Kubebuilder envtest API server.
func TestReconciler_EnvtestIntegration(t *testing.T) {
	dynClient, c := startReconcilerEnvtest(t)
	ctx := context.Background()

	t.Run("ServerSideApplyMergesExternalConditionsAndAttachesFinalizer", func(t *testing.T) {
		verifyEnvtestSSAMergeAndFinalizers(ctx, t, dynClient, c)
	})
	t.Run("ContractBrokenStalledStatusOnMalformed3PPayload", func(t *testing.T) {
		verifyEnvtestContractBreach(ctx, t, dynClient, c)
	})
}

func startReconcilerEnvtest(t *testing.T) (dynamic.Interface, client.Client) {
	t.Helper()
	crdDir := t.TempDir()
	combined := append(renderHelmManifestTemplate(t, "crd.yaml", true), []byte("\n---\n"+rayClusterTestCRD)...)
	if err := os.WriteFile(filepath.Join(crdDir, "crds.yaml"), combined, 0600); err != nil {
		t.Fatalf("failed to write CRDs: %v", err)
	}
	testEnv := &envtest.Environment{
		BinaryAssetsDirectory:   resolveEnvtestAssets(t),
		ControlPlaneStopTimeout: 10 * time.Second,
		CRDInstallOptions:       envtest.CRDInstallOptions{Paths: []string{crdDir}, ErrorIfPathMissing: true, PollInterval: 10 * time.Millisecond, MaxTime: 5 * time.Second},
	}
	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("failed to start envtest environment: %v", err)
	}
	t.Cleanup(func() { _ = testEnv.Stop() })
	dynClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("failed to create dynamic client: %v", err)
	}
	c, err := client.New(cfg, client.Options{})
	if err != nil {
		t.Fatalf("failed to create controller-runtime client: %v", err)
	}
	return dynClient, c
}

func verifyEnvtestSSAMergeAndFinalizers(ctx context.Context, t *testing.T, dynClient dynamic.Interface, c client.Client) {
	t.Helper()
	compClient := dynClient.Resource(ComponentGVR).Namespace("default")
	cr := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "xmc.dev/v1alpha1", "kind": "Component",
		"metadata": map[string]any{"name": "ssa-merge-comp", "namespace": "default"},
		"spec":     map[string]any{"enabled": true, "componentType": "COMPONENT_TYPE_CUSTOM_1P_COMPONENT", "deletionProtection": true},
	}}
	created, err := compClient.Create(ctx, cr, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Create Component failed: %v", err)
	}
	applyExternalStatusCondition(ctx, t, compClient, created.GetName())
	reconcileAndApplyBridgeStatus(ctx, t, c, compClient, created.GetName())
	assertMergedConditionsAndFinalizer(ctx, t, compClient, created.GetName())
}

func applyExternalStatusCondition(ctx context.Context, t *testing.T, compClient dynamic.ResourceInterface, name string) {
	t.Helper()
	cond := map[string]any{"type": "ExternalSLA", "status": "True", "lastTransitionTime": time.Now().UTC().Format(time.RFC3339), "reason": "SLAVerified", "message": "External controller verified SLA"}
	patchObj := map[string]any{"apiVersion": "xmc.dev/v1alpha1", "kind": "Component", "metadata": map[string]any{"name": name, "namespace": "default"}, "status": map[string]any{"conditions": []map[string]any{cond}}}
	raw, err := json.Marshal(patchObj)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	force := true
	if _, err := compClient.Patch(ctx, name, types.ApplyPatchType, raw, metav1.PatchOptions{FieldManager: "external-sla-controller", Force: &force}, "status"); err != nil {
		t.Fatalf("external SSA status patch failed: %v", err)
	}
}

func reconcileAndApplyBridgeStatus(ctx context.Context, t *testing.T, c client.Client, compClient dynamic.ResourceInterface, name string) {
	t.Helper()
	live, err := compClient.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get Component failed: %v", err)
	}
	if live, err = AddCleanupFinalizer(ctx, c, live); err != nil {
		t.Fatalf("AddCleanupFinalizer failed: %v", err)
	}
	rec, err := NewReconciler(&mockCMS{}, nil, &mockHelm{})
	if err != nil {
		t.Fatalf("NewReconciler failed: %v", err)
	}
	res, err := rec.ReconcileWithDependencies(ctx, live, nil)
	if err != nil {
		t.Fatalf("ReconcileWithDependencies failed: %v", err)
	}
	if err := ApplyComponentStatusSSA(ctx, c, live, res); err != nil {
		t.Fatalf("ApplyComponentStatusSSA failed: %v", err)
	}
}

func assertMergedConditionsAndFinalizer(ctx context.Context, t *testing.T, compClient dynamic.ResourceInterface, name string) {
	t.Helper()
	updated, err := compClient.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get updated Component failed: %v", err)
	}
	if !HasCleanupFinalizer(updated) {
		t.Errorf("expected finalizer %q on Component, got %v", FinalizerCMSCleanup, updated.GetFinalizers())
	}
	conds, _, _ := unstructured.NestedSlice(updated.Object, "status", "conditions")
	seen := make(map[string]string)
	for _, raw := range conds {
		if m, ok := raw.(map[string]any); ok {
			seen[m["type"].(string)] = m["status"].(string)
		}
	}
	want := map[string]string{
		"ExternalSLA":       "True",
		ConditionTypeReady:  ConditionStatusTrue,
		ConditionTypeSynced: ConditionStatusTrue,
	}
	if diff := cmp.Diff(want, seen); diff != "" {
		t.Errorf("merged conditions mismatch (-want +got):\n%s", diff)
	}
}

func TestReconcileComponent_CMSSyncCondition(t *testing.T) {
	ctx := context.Background()
	spec3P := &componentspb.ComponentSpec{
		Type:       componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
		WrapperRef: &componentspb.WrapperRef{CrdGroup: "clickhouse.altinity.com", CrdVersion: "v1", CrdKind: "ClickHouseInstallation", TargetName: "ch"},
	}
	spec1P := &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT}
	prober := &mockProber{status: &ResourceStatus{Found: true, Ready: true, Endpoint: "http://ch.xmc:8123", ConnectionSecret: "ch-sec"}}
	prevAccess := &AccessStatus{EnvVars: map[string]string{"ENDPOINT": "http://ch.xmc:8123", "HOST": "ch.xmc", "PORT": "8123", "SCHEME": "http", "SECRET_NAME": "ch-sec"}}
	prevReady := []Condition{
		{Type: ConditionTypeReady, Status: ConditionStatusTrue, ObservedGeneration: 1, Reason: ReasonComponentHealthy, Message: "Ready"},
	}

	tests := []struct {
		name           string
		cms            *mockCMS
		helm           *mockHelm
		in             ReconcileComponentInput
		wantErr        bool
		wantState      componentspb.ComponentState
		wantObsGen     int64
		wantReady      string
		wantReadyRsn   string
		wantSynced     string
		wantSyncedRsn  string
		wantStalledGen int64
		wantAccessKept bool
		wantObsCall    bool
	}{
		{
			name:          "initial reconcile CMS failure sets Ready=Unknown/InitialSyncPending and Synced=False/CMSSyncFailed",
			cms:           &mockCMS{setErr: status.Error(codes.Unavailable, "cms down")},
			in:            ReconcileComponentInput{Namespace: "xmc", Name: "ch", Generation: 1, Spec: spec3P, Enabled: true},
			wantErr:       true,
			wantState:     componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantObsGen:    0,
			wantReady:     ConditionStatusUnknown,
			wantReadyRsn:  ReasonInitialSyncPending,
			wantSynced:    ConditionStatusFalse,
			wantSyncedRsn: ReasonCMSSyncFailed,
		},
		{
			name:           "transient SetComponentIntent failure preserves Ready=True, ENABLED state, and status.access without advancing observedGeneration",
			cms:            &mockCMS{setErr: status.Error(codes.Unavailable, "cms down")},
			in:             ReconcileComponentInput{Namespace: "xmc", Name: "ch", Generation: 2, ExistingObservedGeneration: 1, Spec: spec3P, Enabled: true, ExistingConditions: prevReady, ExistingAccess: prevAccess},
			wantErr:        true,
			wantState:      componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			wantObsGen:     1,
			wantReady:      ConditionStatusTrue,
			wantReadyRsn:   ReasonComponentHealthy,
			wantSynced:     ConditionStatusFalse,
			wantSyncedRsn:  ReasonCMSSyncFailed,
			wantAccessKept: true,
		},
		{
			name:           "transient UpdateObservedGeneration failure keeps newly evaluated Ready=True and status.access with Synced=False",
			cms:            &mockCMS{obsErr: status.Error(codes.Unavailable, "cms down")},
			in:             ReconcileComponentInput{Namespace: "xmc", Name: "ch", Generation: 2, ExistingObservedGeneration: 1, Spec: spec3P, Enabled: true},
			wantErr:        true,
			wantState:      componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			wantObsGen:     1,
			wantReady:      ConditionStatusTrue,
			wantReadyRsn:   ReasonComponentHealthy,
			wantSynced:     ConditionStatusFalse,
			wantSyncedRsn:  ReasonCMSSyncFailed,
			wantAccessKept: true,
		},
		{
			name:           "successful CMS sync sets Synced=True/CMSSynced and advances observedGeneration",
			cms:            &mockCMS{},
			in:             ReconcileComponentInput{Namespace: "xmc", Name: "ch", Generation: 2, ExistingObservedGeneration: 1, Spec: spec3P, Enabled: true},
			wantState:      componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			wantObsGen:     2,
			wantReady:      ConditionStatusTrue,
			wantReadyRsn:   ReasonComponentHealthy,
			wantSynced:     ConditionStatusTrue,
			wantSyncedRsn:  ReasonCMSSynced,
			wantAccessKept: true,
		},
		{
			name:           "CMS-detected dependency cycle in SetComponentIntent reports DependencyCycle and Synced=True, preserving access and stamping observedGeneration",
			cms:            &mockCMS{setErr: errorInfoStatus(t, codes.FailedPrecondition, store.ReasonDependencyCycle, store.ErrorInfoDomain, "dependency cycle detected: ch -> b -> ch")},
			in:             ReconcileComponentInput{Namespace: "xmc", Name: "ch", Generation: 2, ExistingObservedGeneration: 1, Spec: spec3P, Enabled: true, ExistingConditions: prevReady, ExistingAccess: prevAccess},
			wantErr:        true,
			wantState:      componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantObsGen:     2,
			wantReady:      ConditionStatusFalse,
			wantReadyRsn:   ReasonDependencyCycle,
			wantSynced:     ConditionStatusTrue,
			wantSyncedRsn:  ReasonCMSSynced,
			wantAccessKept: true,
		},
		{
			name: "transient CMS failure on a previously stalled component keeps observedGeneration",
			cms:  &mockCMS{setErr: status.Error(codes.Unavailable, "cms down")},
			in: ReconcileComponentInput{Namespace: "xmc", Name: "ch", Generation: 2, ExistingObservedGeneration: 1, Spec: spec3P, Enabled: true, ExistingConditions: []Condition{
				{Type: ConditionTypeReady, Status: ConditionStatusFalse, ObservedGeneration: 1, Reason: ReasonDependencyCycle},
				{Type: ConditionTypeStalled, Status: ConditionStatusTrue, ObservedGeneration: 1, Reason: ReasonDependencyCycle},
			}},
			wantErr:        true,
			wantState:      componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantObsGen:     1,
			wantReady:      ConditionStatusFalse,
			wantReadyRsn:   ReasonDependencyCycle,
			wantSynced:     ConditionStatusFalse,
			wantSyncedRsn:  ReasonCMSSyncFailed,
			wantStalledGen: 1,
		},
		{
			name:          "CMS unregistered dependency rejection finishes with err == nil and advances observedGeneration",
			cms:           &mockCMS{setErr: dependencyNotRegisteredErr(t, "ch", "redpanda")},
			in:            ReconcileComponentInput{Namespace: "xmc", Name: "ch", Generation: 3, ExistingObservedGeneration: 1, Spec: spec3P, Enabled: true},
			wantState:     componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantObsGen:    3,
			wantReady:     ConditionStatusFalse,
			wantReadyRsn:  ReasonDependenciesNotReady,
			wantSynced:    ConditionStatusTrue,
			wantSyncedRsn: ReasonCMSSynced,
		},
		{
			name:           "blocked deactivation keeps Ready=True and status.access while workload remains active",
			cms:            &mockCMS{prepareErr: status.Error(codes.FailedPrecondition, "active dependents")},
			in:             ReconcileComponentInput{Namespace: "xmc", Name: "ch", Generation: 2, ExistingObservedGeneration: 1, Spec: spec3P, Enabled: false, ExistingConditions: prevReady, ExistingAccess: prevAccess},
			wantState:      componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantObsGen:     2,
			wantReady:      ConditionStatusTrue,
			wantReadyRsn:   ReasonComponentHealthy,
			wantSynced:     ConditionStatusTrue,
			wantSyncedRsn:  ReasonCMSSynced,
			wantAccessKept: true,
		},
		{
			name:           "1P Helm upgrade failure preserves status.access",
			cms:            &mockCMS{},
			helm:           &mockHelm{active: true, ensureErr: errors.New("helm upgrade failed")},
			in:             ReconcileComponentInput{Namespace: "xmc", Name: "ch", Generation: 2, ExistingObservedGeneration: 1, Spec: spec1P, Enabled: true, ExistingConditions: prevReady, ExistingAccess: prevAccess},
			wantErr:        true,
			wantState:      componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantObsGen:     2,
			wantReady:      ConditionStatusFalse,
			wantReadyRsn:   ReasonHelmUpgradeFailed,
			wantSynced:     ConditionStatusTrue,
			wantSyncedRsn:  ReasonCMSSynced,
			wantStalledGen: 2,
			wantAccessKept: true,
		},
		{
			name: "recovery pass after Synced=False executes UpdateObservedGeneration even when Ready at current generation is recent",
			cms:  &mockCMS{},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ch", Generation: 2, Spec: spec3P, Enabled: true,
				ExistingConditions: []Condition{
					{Type: ConditionTypeReady, Status: ConditionStatusTrue, ObservedGeneration: 2, Reason: ReasonComponentHealthy, Message: "Ready", LastTransitionTime: time.Now().UTC()},
					{Type: ConditionTypeSynced, Status: ConditionStatusFalse, ObservedGeneration: 2, Reason: ReasonCMSSyncFailed, Message: "cms down", LastTransitionTime: time.Now().UTC()},
				},
			},
			wantState:      componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			wantObsGen:     2,
			wantReady:      ConditionStatusTrue,
			wantReadyRsn:   ReasonComponentHealthy,
			wantSynced:     ConditionStatusTrue,
			wantSyncedRsn:  ReasonCMSSynced,
			wantAccessKept: true,
			wantObsCall:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var helmMgr HelmManager
			if tt.helm != nil {
				helmMgr = tt.helm
			}
			r, _ := NewReconciler(tt.cms, prober, helmMgr)
			res, err := r.ReconcileComponent(ctx, tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ReconcileComponent() error = %v, wantErr %v", err, tt.wantErr)
			}
			if res.State != tt.wantState {
				t.Errorf("State = %v, want %v", res.State, tt.wantState)
			}
			if res.ObservedGeneration != tt.wantObsGen {
				t.Errorf("ObservedGeneration = %d, want %d", res.ObservedGeneration, tt.wantObsGen)
			}
			ready := findCondition(res.Conditions, ConditionTypeReady)
			if ready == nil || ready.Status != tt.wantReady || ready.Reason != tt.wantReadyRsn {
				t.Errorf("Ready condition = %+v, want %s/%s", ready, tt.wantReady, tt.wantReadyRsn)
			}
			synced := findCondition(res.Conditions, ConditionTypeSynced)
			if synced == nil || synced.Status != tt.wantSynced || synced.Reason != tt.wantSyncedRsn {
				t.Errorf("Synced condition = %+v, want %s/%s", synced, tt.wantSynced, tt.wantSyncedRsn)
			}
			if tt.wantStalledGen != 0 {
				stalled := findCondition(res.Conditions, ConditionTypeStalled)
				if stalled == nil || stalled.Status != ConditionStatusTrue || stalled.ObservedGeneration != tt.wantStalledGen {
					t.Errorf("Stalled condition = %+v, want True at ObservedGeneration=%d", stalled, tt.wantStalledGen)
				}
			}
			if tt.wantAccessKept {
				if diff := cmp.Diff(prevAccess, res.Access); diff != "" {
					t.Errorf("Access mismatch (-want +got):\n%s", diff)
				}
			}
			if tt.wantObsCall && tt.cms.lastObsReq == nil {
				t.Error("expected UpdateObservedGeneration to be called on CMS recovery, got nil")
			}
		})
	}
}

func TestGenerationEvaluated(t *testing.T) {
	const gen = 3
	errPass := errors.New("pass failed")
	stalledAt := func(g int64) Condition {
		return Condition{Type: ConditionTypeStalled, Status: ConditionStatusTrue, ObservedGeneration: g, Reason: ReasonContractBroken}
	}
	synced := func(s string) Condition {
		return Condition{Type: ConditionTypeSynced, Status: s, ObservedGeneration: gen}
	}
	tests := []struct {
		name string
		res  *ReconcileResult
		err  error
		want bool
	}{
		{name: "nil result", res: nil, want: false},
		{name: "successful pass", res: &ReconcileResult{}, want: true},
		{name: "transient error without Stalled", res: &ReconcileResult{Conditions: []Condition{synced(ConditionStatusTrue)}}, err: errPass, want: false},
		{name: "Stalled at this generation with CMS synced", res: &ReconcileResult{Conditions: []Condition{stalledAt(gen), synced(ConditionStatusTrue)}}, err: errPass, want: true},
		{name: "Stalled at this generation with CMS sync failed", res: &ReconcileResult{Conditions: []Condition{stalledAt(gen), synced(ConditionStatusFalse)}}, err: errPass, want: false},
		{name: "Stalled at an older generation", res: &ReconcileResult{Conditions: []Condition{stalledAt(gen - 1), synced(ConditionStatusTrue)}}, err: errPass, want: false},
		{name: "Stalled without Synced", res: &ReconcileResult{Conditions: []Condition{stalledAt(gen)}}, err: errPass, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := generationEvaluated(tt.res, gen, tt.err); got != tt.want {
				t.Errorf("generationEvaluated() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReconcileComponent_CMSOutageRefreshesLocalHealth(t *testing.T) {
	ctx := context.Background()
	cmsDown := status.Error(codes.Unavailable, "cms down")
	spec3P := &componentspb.ComponentSpec{
		Type:       componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
		WrapperRef: &componentspb.WrapperRef{CrdGroup: "clickhouse.altinity.com", CrdVersion: "v1", CrdKind: "ClickHouseInstallation", TargetName: "ch"},
	}
	spec1P := &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT}
	prevAccess := &AccessStatus{EnvVars: map[string]string{"ENDPOINT": "http://ch.xmc:8123"}}
	prevReady := []Condition{
		{Type: ConditionTypeReady, Status: ConditionStatusTrue, ObservedGeneration: 1, Reason: ReasonComponentHealthy, Message: "Ready"},
	}

	tests := []struct {
		name         string
		prober       *mockProber
		helm         *mockHelm
		in           ReconcileComponentInput
		wantState    componentspb.ComponentState
		wantReady    string
		wantReadyRsn string
		wantReadyGen int64
		wantAccess   *AccessStatus
	}{
		{
			name:         "actuated 3P wrapper that became unready surfaces the local failure",
			prober:       &mockProber{status: &ResourceStatus{Found: true, Ready: false, Message: "pods crashing"}},
			in:           ReconcileComponentInput{Namespace: "xmc", Name: "ch", Generation: 2, Spec: spec3P, Enabled: true, ExistingConditions: prevReady, ExistingAccess: prevAccess},
			wantState:    componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantReady:    ConditionStatusFalse,
			wantReadyRsn: "ResourceNotReady",
			wantReadyGen: 1,
		},
		{
			name:         "actuated 1P release that failed surfaces the local failure without a Helm upgrade",
			helm:         &mockHelm{active: true, unhealthyReason: ReasonHelmUpgradeFailed},
			in:           ReconcileComponentInput{Namespace: "xmc", Name: "ch", Generation: 2, Spec: spec1P, Enabled: true, ExistingConditions: prevReady, ExistingAccess: prevAccess},
			wantState:    componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantReady:    ConditionStatusFalse,
			wantReadyRsn: ReasonHelmUpgradeFailed,
			wantReadyGen: 1,
			wantAccess:   prevAccess,
		},
		{
			name:         "healthy actuated 1P release keeps Ready=True and status.access",
			helm:         &mockHelm{active: true},
			in:           ReconcileComponentInput{Namespace: "xmc", Name: "ch", Generation: 1, Spec: spec1P, Enabled: true, ExistingConditions: prevReady, ExistingAccess: prevAccess},
			wantState:    componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			wantReady:    ConditionStatusTrue,
			wantReadyRsn: ReasonComponentHealthy,
			wantReadyGen: 1,
			wantAccess:   prevAccess,
		},
		{
			name:         "disabled component does not keep Ready=True",
			prober:       &mockProber{status: &ResourceStatus{Found: true, Ready: true}},
			in:           ReconcileComponentInput{Namespace: "xmc", Name: "ch", Generation: 2, Spec: spec3P, Enabled: false, ExistingConditions: prevReady, ExistingAccess: prevAccess},
			wantState:    componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantReady:    ConditionStatusUnknown,
			wantReadyRsn: ReasonCMSSyncFailed,
			wantReadyGen: 2,
			wantAccess:   prevAccess,
		},
		{
			name:   "actuated 3P wrapper without dependencies that became unready on outage pass 1 recovers to Ready=True on outage pass 2",
			prober: &mockProber{status: &ResourceStatus{Found: true, Ready: true, Endpoint: "http://ch.xmc:8123"}},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ch", Generation: 2, Spec: spec3P, Enabled: true,
				ExistingConditions: []Condition{
					{Type: ConditionTypeReady, Status: ConditionStatusFalse, ObservedGeneration: 1, Reason: "ResourceNotReady", Message: "pods crashing"},
					{Type: ConditionTypeSynced, Status: ConditionStatusFalse, ObservedGeneration: 2, Reason: ReasonCMSSyncFailed, Message: "cms down"},
				},
			},
			wantState:    componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			wantReady:    ConditionStatusTrue,
			wantReadyRsn: ReasonComponentHealthy,
			wantReadyGen: 1,
		},
		{
			name:   "never-actuated 3P wrapper gated on DependenciesNotReady is not probed during CMS outage",
			prober: &mockProber{status: &ResourceStatus{Found: true, Ready: true, Endpoint: "http://ch.xmc:8123"}},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ch", Generation: 2, Spec: spec3P, Enabled: true,
				ExistingConditions: []Condition{
					{Type: ConditionTypeReady, Status: ConditionStatusFalse, ObservedGeneration: 1, Reason: ReasonDependenciesNotReady, Message: "waiting on db"},
				},
			},
			wantState:    componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantReady:    ConditionStatusFalse,
			wantReadyRsn: ReasonDependenciesNotReady,
			wantReadyGen: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var helmMgr HelmManager
			if tt.helm != nil {
				helmMgr = tt.helm
			}
			cms := &mockCMS{setErr: cmsDown}
			r, _ := NewReconciler(cms, tt.prober, helmMgr)
			res, err := r.ReconcileComponent(ctx, tt.in)
			if status.Code(errors.Unwrap(err)) != codes.Unavailable {
				t.Fatalf("ReconcileComponent() error = %v, want the wrapped CMS Unavailable error", err)
			}
			if res.State != tt.wantState {
				t.Errorf("State = %v, want %v", res.State, tt.wantState)
			}
			ready := findCondition(res.Conditions, ConditionTypeReady)
			if ready == nil || ready.Status != tt.wantReady || ready.Reason != tt.wantReadyRsn || ready.ObservedGeneration != tt.wantReadyGen {
				t.Errorf("Ready condition = %+v, want %s/%s at generation %d", ready, tt.wantReady, tt.wantReadyRsn, tt.wantReadyGen)
			}
			synced := findCondition(res.Conditions, ConditionTypeSynced)
			if synced == nil || synced.Status != ConditionStatusFalse || synced.Reason != ReasonCMSSyncFailed {
				t.Errorf("Synced condition = %+v, want False/%s", synced, ReasonCMSSyncFailed)
			}
			if tt.wantAccess != nil {
				if diff := cmp.Diff(tt.wantAccess, res.Access); diff != "" {
					t.Errorf("Access mismatch (-want +got):\n%s", diff)
				}
			}
			if cms.lastObsReq != nil {
				t.Errorf("UpdateObservedGeneration called during a CMS outage: %v", cms.lastObsReq)
			}
			if tt.helm != nil && tt.helm.ensureCalls != 0 {
				t.Errorf("Ensure1PComponent calls = %d, want 0", tt.helm.ensureCalls)
			}
		})
	}
}

func verifyEnvtestContractBreach(ctx context.Context, t *testing.T, dynClient dynamic.Interface, c client.Client) {
	t.Helper()
	rayGVR := schema.GroupVersionResource{Group: "ray.io", Version: "v1", Resource: "rayclusters"}
	createMalformedRayCluster(ctx, t, dynClient.Resource(rayGVR).Namespace("default"))
	compClient := dynClient.Resource(ComponentGVR).Namespace("default")
	cr := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "xmc.dev/v1alpha1", "kind": "Component",
		"metadata": map[string]any{"name": "ray-broken-wrapper", "namespace": "default"},
		"spec": map[string]any{
			"enabled": true, "componentType": "COMPONENT_TYPE_CUSTOM_3P_WRAPPER",
			"wrapperRef": map[string]any{"group": "ray.io", "version": "v1", "kind": "RayCluster", "name": "ray-malformed"},
		},
	}}
	created, err := compClient.Create(ctx, cr, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Create 3P wrapper Component failed: %v", err)
	}
	reconcileMalformedWrapper(ctx, t, dynClient, c, created)
	assertContractBrokenStatus(ctx, t, compClient, created.GetName())
}

func createMalformedRayCluster(ctx context.Context, t *testing.T, rayClient dynamic.ResourceInterface) {
	t.Helper()
	rc := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "ray.io/v1", "kind": "RayCluster", "metadata": map[string]any{"name": "ray-malformed", "namespace": "default"}}}
	created, err := rayClient.Create(ctx, rc, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Create RayCluster failed: %v", err)
	}
	created.Object["status"] = map[string]any{"conditions": "malformed-non-slice-conditions"}
	if _, err := rayClient.UpdateStatus(ctx, created, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("UpdateStatus RayCluster failed: %v", err)
	}
}

func reconcileMalformedWrapper(ctx context.Context, t *testing.T, dynClient dynamic.Interface, c client.Client, live *unstructured.Unstructured) {
	t.Helper()
	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{{Group: "ray.io", Version: "v1"}})
	mapper.Add(schema.GroupVersionKind{Group: "ray.io", Version: "v1", Kind: "RayCluster"}, meta.RESTScopeNamespace)
	prober := NewBridgeProber(NewK8sStatusDetector(dynClient, mapper), nil, time.Second)
	rec, err := NewReconciler(&mockCMS{}, prober, nil)
	if err != nil {
		t.Fatalf("NewReconciler failed: %v", err)
	}
	res, err := rec.ReconcileWithDependencies(ctx, live, nil)
	if !errors.Is(err, ErrContractBreach) {
		t.Fatalf("expected ErrContractBreach from ReconcileWithDependencies, got %v", err)
	}
	if err := ApplyComponentStatusSSA(ctx, c, live, res); err != nil {
		t.Fatalf("ApplyComponentStatusSSA failed: %v", err)
	}
}

func assertContractBrokenStatus(ctx context.Context, t *testing.T, compClient dynamic.ResourceInterface, name string) {
	t.Helper()
	updated, err := compClient.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get updated Component failed: %v", err)
	}
	conds, _, _ := unstructured.NestedSlice(updated.Object, "status", "conditions")
	seen := make(map[string]string)
	for _, raw := range conds {
		if m, ok := raw.(map[string]any); ok {
			typ, _ := m["type"].(string)
			if typ == ConditionTypeReady || typ == ConditionTypeStalled {
				seen[typ] = fmt.Sprintf("%s/%s", m["status"], m["reason"])
			}
		}
	}
	want := map[string]string{
		ConditionTypeReady:   fmt.Sprintf("%s/%s", ConditionStatusFalse, ReasonContractBroken),
		ConditionTypeStalled: fmt.Sprintf("%s/%s", ConditionStatusTrue, ReasonContractBroken),
	}
	if diff := cmp.Diff(want, seen); diff != "" {
		t.Errorf("contract broken conditions mismatch (-want +got):\n%s", diff)
	}
}

const (
	appliedIntentMessage     = "Applied component intent to CMS"
	evaluatedHealthMessage   = "Evaluated component health"
	componentDisabledMessage = "Component disabled"
)

type loggedRecord struct {
	level slog.Level
	attrs map[string]any
}

type recordingHandler struct {
	mu      sync.Mutex
	records map[string][]loggedRecord
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	attrs := make(map[string]any)
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.Any()
		return true
	})
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records[r.Message] = append(h.records[r.Message], loggedRecord{level: r.Level, attrs: attrs})
	return nil
}

func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler      { return h }

func recordLogs() (context.Context, *recordingHandler) {
	h := &recordingHandler{records: make(map[string][]loggedRecord)}
	return logging.WithLogger(context.Background(), slog.New(h)), h
}

func (h *recordingHandler) recordsFor(message string) []loggedRecord {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]loggedRecord(nil), h.records[message]...)
}

func (h *recordingHandler) singleRecord(t *testing.T, message string) loggedRecord {
	t.Helper()
	recs := h.recordsFor(message)
	if len(recs) != 1 {
		t.Fatalf("%q logged %d times, want 1", message, len(recs))
	}
	return recs[0]
}

func TestReconcileComponent_AppliedIntentLogLevel(t *testing.T) {
	tests := []struct {
		name                string
		reported, committed int64
		reportedID          string
		committedID         int64
		wantLevel           slog.Level
		wantComponentID     string
	}{
		{name: "first apply without a reported generation", reported: 0, committed: 1, committedID: 1, wantLevel: slog.LevelInfo, wantComponentID: "1"},
		{name: "unchanged generation and ComponentId re-applied every pass", reported: 4, committed: 4, reportedID: "1", committedID: 1, wantLevel: slog.LevelDebug, wantComponentID: "1"},
		{name: "newly committed generation", reported: 4, committed: 5, reportedID: "1", committedID: 1, wantLevel: slog.LevelInfo, wantComponentID: "1"},
		{name: "new ComponentId at the same generation", reported: 4, committed: 4, reportedID: "1", committedID: 2, wantLevel: slog.LevelInfo, wantComponentID: "2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := NewReconciler(&mockCMS{componentGen: tt.committed, componentID: tt.committedID}, nil, nil)
			if err != nil {
				t.Fatalf("NewReconciler() error = %v", err)
			}
			ctx, logs := recordLogs()
			in := ReconcileComponentInput{
				Namespace:           "xmc",
				Name:                "redpanda",
				Generation:          7,
				UID:                 "uid-a",
				ComponentGeneration: tt.reported,
				ComponentID:         tt.reportedID,
				Enabled:             true,
				Spec:                &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT},
			}
			if _, err := r.ReconcileComponent(ctx, in); err != nil {
				t.Fatalf("ReconcileComponent() error = %v", err)
			}
			got := logs.singleRecord(t, appliedIntentMessage)
			if got.level != tt.wantLevel {
				t.Errorf("%q level = %v, want %v", appliedIntentMessage, got.level, tt.wantLevel)
			}
			want := map[string]any{"previousComponentGeneration": tt.reported, "previousComponentId": tt.reportedID, "componentId": tt.wantComponentID}
			gotAttrs := make(map[string]any, len(want))
			for key := range want {
				gotAttrs[key] = got.attrs[key]
			}
			if diff := cmp.Diff(want, gotAttrs); diff != "" {
				t.Errorf("%q attributes mismatch (-want +got):\n%s", appliedIntentMessage, diff)
			}
		})
	}
}

func TestReconcileComponent_HealthLogLevel(t *testing.T) {
	in := ReconcileComponentInput{
		Namespace:  "xmc",
		Name:       "redpanda",
		Generation: 7,
		UID:        "uid-a",
		Enabled:    true,
		Spec:       &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT},
	}
	steady := healthyConditions(t, in)
	tests := []struct {
		name       string
		existing   []Condition
		obsErr     error
		wantLevel  slog.Level
		wantReason string
	}{
		{name: "first evaluation", wantLevel: slog.LevelInfo, wantReason: ReasonComponentHealthy},
		{name: "unchanged health", existing: steady, wantLevel: slog.LevelDebug, wantReason: ReasonComponentHealthy},
		{
			name:       "Ready message only changed",
			existing:   withCondition(steady, ConditionTypeReady, func(c *Condition) { c.Message = "previous message" }),
			wantLevel:  slog.LevelDebug,
			wantReason: ReasonComponentHealthy,
		},
		{
			name:       "Ready reason changed",
			existing:   withCondition(steady, ConditionTypeReady, func(c *Condition) { c.Reason = ReasonHelmStatusUnavailable }),
			wantLevel:  slog.LevelInfo,
			wantReason: ReasonComponentHealthy,
		},
		{
			name:       "Ready status changed",
			existing:   withCondition(steady, ConditionTypeReady, func(c *Condition) { c.Status = ConditionStatusFalse }),
			wantLevel:  slog.LevelInfo,
			wantReason: ReasonComponentHealthy,
		},
		{
			name:       "first evaluation of a new generation",
			existing:   withCondition(steady, ConditionTypeReady, func(c *Condition) { c.ObservedGeneration = in.Generation - 1 }),
			wantLevel:  slog.LevelInfo,
			wantReason: ReasonComponentHealthy,
		},
		{
			name:       "source conflict on observed-state report",
			existing:   steady,
			obsErr:     sourceConflictTestErr(t, store.ReasonSourceIncarnationMismatch),
			wantLevel:  slog.LevelInfo,
			wantReason: ReasonSourceConflict,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := NewReconciler(&mockCMS{obsErr: tt.obsErr}, nil, nil)
			if err != nil {
				t.Fatalf("NewReconciler() error = %v", err)
			}
			ctx, logs := recordLogs()
			pass := in
			pass.ExistingConditions = tt.existing
			if _, err := r.ReconcileComponent(ctx, pass); err != nil {
				t.Fatalf("ReconcileComponent() error = %v", err)
			}
			got := logs.singleRecord(t, evaluatedHealthMessage)
			if got.level != tt.wantLevel {
				t.Errorf("%q level = %v, want %v", evaluatedHealthMessage, got.level, tt.wantLevel)
			}
			if got.attrs["reason"] != tt.wantReason {
				t.Errorf("%q reason = %v, want %q", evaluatedHealthMessage, got.attrs["reason"], tt.wantReason)
			}
		})
	}
}

func TestReconcileComponent_DisabledLogLevelAndCMSFailure(t *testing.T) {
	spec1P := &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT}
	in := ReconcileComponentInput{
		Namespace:  "xmc",
		Name:       "redpanda",
		Generation: 2,
		Enabled:    false,
		Spec:       spec1P,
	}

	r, err := NewReconciler(&mockCMS{}, nil, &mockHelm{})
	if err != nil {
		t.Fatalf("NewReconciler() error = %v", err)
	}
	ctx1, logs1 := recordLogs()
	res1, err := r.ReconcileComponent(ctx1, in)
	if err != nil {
		t.Fatalf("first pass error = %v", err)
	}
	if got := logs1.singleRecord(t, componentDisabledMessage); got.level != slog.LevelInfo {
		t.Errorf("first disabled pass level = %v, want %v", got.level, slog.LevelInfo)
	}

	in.ExistingConditions = res1.Conditions
	ctx2, logs2 := recordLogs()
	if _, err := r.ReconcileComponent(ctx2, in); err != nil {
		t.Fatalf("second pass error = %v", err)
	}
	if got := logs2.singleRecord(t, componentDisabledMessage); got.level != slog.LevelDebug {
		t.Errorf("steady-state disabled pass level = %v, want %v", got.level, slog.LevelDebug)
	}

	rFail, err := NewReconciler(&mockCMS{obsErr: status.Error(codes.Unavailable, "cms down")}, nil, &mockHelm{})
	if err != nil {
		t.Fatalf("NewReconciler() error = %v", err)
	}
	ctx3, logs3 := recordLogs()
	res3, err := rFail.ReconcileComponent(ctx3, ReconcileComponentInput{
		Namespace: "xmc", Name: "redpanda", Generation: 3, Enabled: false, Spec: spec1P,
	})
	if err == nil {
		t.Fatal("expected error when UpdateObservedGeneration fails on disabled component, got nil")
	}
	if ready := findCondition(res3.Conditions, ConditionTypeReady); ready == nil || ready.Reason != ReasonDeactivationIncomplete {
		t.Errorf("Ready condition on failed disabled CMS sync = %+v, want reason %q", ready, ReasonDeactivationIncomplete)
	}
	if got := len(logs3.recordsFor(componentDisabledMessage)); got != 0 {
		t.Errorf("%q logged %d times on failed CMS sync, want 0", componentDisabledMessage, got)
	}
}

func TestReconcileComponent_DeactivationBlockedLogLevel(t *testing.T) {
	const blockedMessage = "Component deactivation blocked by active dependents"
	in := ReconcileComponentInput{
		Namespace: "xmc", Name: "redpanda", Generation: 2, Enabled: false,
		Spec: &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT},
	}
	cms := &mockCMS{prepareErr: status.Error(codes.FailedPrecondition, "active dependents remaining: [ingestion]"), componentGen: 1, componentID: 1}
	newReconciler := func() *Reconciler {
		r, err := NewReconciler(cms, nil, &mockHelm{})
		if err != nil {
			t.Fatalf("NewReconciler() error = %v", err)
		}
		return r
	}
	assertLevel := func(t *testing.T, logs *recordingHandler, message string, want slog.Level) {
		t.Helper()
		if got := logs.singleRecord(t, message); got.level != want {
			t.Errorf("%q level = %v, want %v", message, got.level, want)
		}
	}
	assertNoInfo := func(t *testing.T, logs *recordingHandler) {
		t.Helper()
		logs.mu.Lock()
		defer logs.mu.Unlock()
		for message, recs := range logs.records {
			for _, rec := range recs {
				if rec.level >= slog.LevelInfo {
					t.Errorf("steady-state pass logged %q at %v, want Debug", message, rec.level)
				}
			}
		}
	}

	r := newReconciler()
	ctx1, logs1 := recordLogs()
	res1, err := r.ReconcileComponent(ctx1, in)
	if err != nil {
		t.Fatalf("first pass error = %v", err)
	}
	assertLevel(t, logs1, blockedMessage, slog.LevelInfo)

	// A pass inside the recheck interval reuses the recorded block without calling CMS.
	in.ExistingConditions = res1.Conditions
	in.ComponentGeneration, in.ComponentID = res1.ComponentGeneration, res1.ComponentID
	ctx2, logs2 := recordLogs()
	if _, err := r.ReconcileComponent(ctx2, in); err != nil {
		t.Fatalf("cached pass error = %v", err)
	}
	if got := len(logs2.recordsFor(blockedMessage)); got != 0 {
		t.Errorf("%q logged %d times on a cached pass, want 0", blockedMessage, got)
	}
	assertNoInfo(t, logs2)

	// A recheck (fresh tracker) that finds the same block logs at Debug only.
	ctx3, logs3 := recordLogs()
	if _, err := newReconciler().ReconcileComponent(ctx3, in); err != nil {
		t.Fatalf("recheck pass error = %v", err)
	}
	assertLevel(t, logs3, blockedMessage, slog.LevelDebug)
	assertNoInfo(t, logs3)
}

func TestReconcileComponent_GatedLogLevel(t *testing.T) {
	const gatedMessage = "Component reconciliation gated"
	in := ReconcileComponentInput{
		Namespace: "xmc", Name: "ingestion", Generation: 1, Enabled: true,
		Spec:         &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, Dependencies: []string{"redpanda"}},
		Dependencies: NewDependencySnapshot(nil),
	}
	r, err := NewReconciler(&mockCMS{}, nil, &mockHelm{})
	if err != nil {
		t.Fatalf("NewReconciler() error = %v", err)
	}

	ctx1, logs1 := recordLogs()
	res1, err := r.ReconcileComponent(ctx1, in)
	if err != nil {
		t.Fatalf("first pass error = %v", err)
	}
	if got := logs1.singleRecord(t, gatedMessage); got.level != slog.LevelInfo {
		t.Errorf("first gated pass level = %v, want %v", got.level, slog.LevelInfo)
	}

	// The same gate on the next pass leaves Ready, Reconciling and Stalled unchanged.
	in.ExistingConditions = res1.Conditions
	in.ComponentGeneration, in.ComponentID = res1.ComponentGeneration, res1.ComponentID
	ctx2, logs2 := recordLogs()
	if _, err := r.ReconcileComponent(ctx2, in); err != nil {
		t.Fatalf("second pass error = %v", err)
	}
	if got := logs2.singleRecord(t, gatedMessage); got.level != slog.LevelDebug {
		t.Errorf("steady-state gated pass level = %v, want %v", got.level, slog.LevelDebug)
	}
}

func TestReconcileComponent_LogDetailAttr(t *testing.T) {
	spec1P := &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT}
	healthy := []Condition{
		{Type: ConditionTypeReady, Status: ConditionStatusTrue, ObservedGeneration: 1, Reason: ReasonComponentHealthy},
	}
	tests := []struct {
		name       string
		helm       *mockHelm
		in         ReconcileComponentInput
		logMessage string
	}{
		{
			name: "gated on an unregistered dependency",
			helm: &mockHelm{},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, Enabled: true,
				Spec:         &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, Dependencies: []string{"redpanda"}},
				Dependencies: NewDependencySnapshot(nil),
			},
			logMessage: "Component reconciliation gated",
		},
		{
			name: "transient Helm status error",
			helm: &mockHelm{unhealthyReason: ReasonHelmStatusUnavailable},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, Spec: spec1P, Enabled: true,
				ExistingConditions: healthy,
			},
			logMessage: "Transient component health check failure; keeping previous health conditions",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := NewReconciler(&mockCMS{}, nil, tt.helm)
			if err != nil {
				t.Fatalf("NewReconciler() error = %v", err)
			}
			ctx, logs := recordLogs()
			if _, err := r.ReconcileComponent(ctx, tt.in); err != nil {
				t.Fatalf("ReconcileComponent() error = %v", err)
			}
			got := logs.singleRecord(t, tt.logMessage)
			if detail, _ := got.attrs["detail"].(string); detail == "" {
				t.Errorf("%q detail = %v, want a non-empty string", tt.logMessage, got.attrs["detail"])
			}
			if msg, ok := got.attrs["message"]; ok {
				t.Errorf("%q has a \"message\" attribute %v, want none", tt.logMessage, msg)
			}
		})
	}
}

func healthyConditions(t *testing.T, in ReconcileComponentInput) []Condition {
	t.Helper()
	r, err := NewReconciler(&mockCMS{}, nil, nil)
	if err != nil {
		t.Fatalf("NewReconciler() error = %v", err)
	}
	res, err := r.ReconcileComponent(context.Background(), in)
	if err != nil {
		t.Fatalf("ReconcileComponent() error = %v", err)
	}
	if got := findCondition(res.Conditions, ConditionTypeReady); got == nil || got.Status != ConditionStatusTrue {
		t.Fatalf("Ready condition = %+v, want status %q", got, ConditionStatusTrue)
	}
	return res.Conditions
}

func withCondition(conds []Condition, condType string, mutate func(c *Condition)) []Condition {
	out := slices.Clone(conds)
	if c := findCondition(out, condType); c != nil {
		mutate(c)
	}
	return out
}

func sourceConflictTestErr(t *testing.T, reason string) error {
	t.Helper()
	st, err := status.New(codes.FailedPrecondition, "source conflict").WithDetails(&errdetails.ErrorInfo{
		Reason: reason,
		Domain: store.ErrorInfoDomain,
	})
	if err != nil {
		t.Fatalf("WithDetails() error = %v", err)
	}
	return st.Err()
}
