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
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
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
		wantAddFin    bool
		wantRemoveFin bool
		// wantHealth, when set, asserts the Ready/Degraded condition statuses and shared reason.
		wantHealth *healthConditions
	}{
		{
			name: "active 3P with finalizer addition",
			cms:  &mockCMS{},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3P,
				DeletionProtection: true, Enabled: true,
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			wantAddFin: true,
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
				DeletionProtection: true, Enabled: true,
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			wantAddFin: true,
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
				DeletionProtection: true, Enabled: true,
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			wantAddFin: true,
		},
		{
			name:   "active 3P with status detector not ready",
			cms:    &mockCMS{},
			prober: &mockProber{status: &ResourceStatus{Found: true, Ready: false, Message: "Waiting for pods"}},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef,
				DeletionProtection: true, Enabled: true,
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantAddFin: true,
		},
		{
			name:   "active 3P with status detector resource not found",
			cms:    &mockCMS{},
			prober: &mockProber{status: &ResourceStatus{Found: false, Message: "CRD not installed: clickhouses.clickhouse.altinity.com"}},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef,
				DeletionProtection: true, Enabled: true,
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantAddFin: true,
		},
		{
			name:   "active 3P with status detector error",
			cms:    &mockCMS{},
			prober: &mockProber{detectErr: errors.New("k8s api timeout")},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef,
				DeletionProtection: true, Enabled: true,
			},
			wantErr:    true,
			wantState:  componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantAddFin: true,
		},
		{
			name:   "active 3P with status detector nil status",
			cms:    &mockCMS{},
			prober: &mockProber{nilStatus: true},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef,
				DeletionProtection: true, Enabled: true,
			},
			wantErr:    true,
			wantState:  componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantAddFin: true,
		},
		{
			name:   "active 3P with status detector error and CMS failure",
			cms:    &mockCMS{obsErr: errors.New("spanner write failed")},
			prober: &mockProber{detectErr: errors.New("k8s api timeout")},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef,
				DeletionProtection: true, Enabled: true,
			},
			wantErr:    true,
			wantState:  componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantAddFin: true,
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
				DeletionProtection: true, Enabled: true,
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantAddFin: true,
		},
		{
			name: "active 1P without deletion protection still attaches finalizer",
			cms:  &mockCMS{},
			helm: &mockHelm{},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, Spec: spec1P,
				HasFinalizer: false, DeletionProtection: false, Enabled: true,
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			wantAddFin: true,
		},
		{
			name: "active 1P with nil prober defaults to healthy",
			cms:  &mockCMS{},
			helm: &mockHelm{},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, Spec: spec1P,
				HasFinalizer: true, DeletionProtection: true, Enabled: true,
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
				HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			wantHealth: &healthConditions{ReadyStatus: ConditionStatusTrue, DegradedStatus: ConditionStatusFalse, Reason: ReasonComponentHealthy},
		},
		{
			name:   "active 1P with missing Helm release is unhealthy even if prober is healthy",
			cms:    &mockCMS{},
			prober: &mockProber{healthy: true},
			helm:   &mockHelm{unhealthyReason: ReasonHelmReleaseNotFound},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, Spec: spec1P,
				HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantHealth: &healthConditions{ReadyStatus: ConditionStatusFalse, DegradedStatus: ConditionStatusTrue, Reason: ReasonHelmReleaseNotFound},
		},
		{
			name: "active 1P with failed async Helm upgrade is unhealthy",
			cms:  &mockCMS{},
			helm: &mockHelm{unhealthyReason: ReasonHelmUpgradeFailed},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, Spec: spec1P,
				HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantHealth: &healthConditions{ReadyStatus: ConditionStatusFalse, DegradedStatus: ConditionStatusTrue, Reason: ReasonHelmUpgradeFailed},
		},
		{
			name: "active 1P with in-flight Helm upgrade is not ready but not degraded",
			cms:  &mockCMS{},
			helm: &mockHelm{unhealthyReason: ReasonHelmUpgradePending},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, Spec: spec1P,
				HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantHealth: &healthConditions{ReadyStatus: ConditionStatusFalse, DegradedStatus: ConditionStatusFalse, Reason: ReasonHelmUpgradePending},
		},
		{
			name: "active 1P with stuck Helm release is degraded",
			cms:  &mockCMS{},
			helm: &mockHelm{unhealthyReason: ReasonHelmReleaseStuck},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, Spec: spec1P,
				HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantHealth: &healthConditions{ReadyStatus: ConditionStatusFalse, DegradedStatus: ConditionStatusTrue, Reason: ReasonHelmReleaseStuck},
		},
		{
			name: "active 1P transient Helm status error keeps previous healthy conditions",
			cms:  &mockCMS{},
			helm: &mockHelm{unhealthyReason: ReasonHelmStatusUnavailable},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, Spec: spec1P,
				HasFinalizer: true, DeletionProtection: true, Enabled: true,
				ExistingConditions: []Condition{
					{Type: ConditionTypeReady, Status: ConditionStatusTrue, ObservedGeneration: 1, Reason: ReasonComponentHealthy},
					{Type: ConditionTypeDegraded, Status: ConditionStatusFalse, ObservedGeneration: 1, Reason: ReasonComponentHealthy},
				},
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			wantHealth: &healthConditions{ReadyStatus: ConditionStatusTrue, DegradedStatus: ConditionStatusFalse, Reason: ReasonComponentHealthy},
		},
		{
			name: "active 1P transient Helm status error keeps previous degraded conditions",
			cms:  &mockCMS{},
			helm: &mockHelm{unhealthyReason: ReasonHelmStatusUnavailable},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, Spec: spec1P,
				HasFinalizer: true, DeletionProtection: true, Enabled: true,
				ExistingConditions: []Condition{
					{Type: ConditionTypeReady, Status: ConditionStatusFalse, ObservedGeneration: 1, Reason: ReasonHelmReleaseNotFound},
					{Type: ConditionTypeDegraded, Status: ConditionStatusTrue, ObservedGeneration: 1, Reason: ReasonHelmReleaseNotFound},
				},
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantHealth: &healthConditions{ReadyStatus: ConditionStatusFalse, DegradedStatus: ConditionStatusTrue, Reason: ReasonHelmReleaseNotFound},
		},
		{
			name: "active 1P transient Helm status error without current-generation conditions is unknown, not degraded",
			cms:  &mockCMS{},
			helm: &mockHelm{unhealthyReason: ReasonHelmStatusUnavailable},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 2, Spec: spec1P,
				HasFinalizer: true, DeletionProtection: true, Enabled: true,
				ExistingConditions: []Condition{
					{Type: ConditionTypeReady, Status: ConditionStatusTrue, ObservedGeneration: 1, Reason: ReasonComponentHealthy},
				},
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantHealth: &healthConditions{ReadyStatus: ConditionStatusFalse, DegradedStatus: ConditionStatusFalse, Reason: ReasonHelmStatusUnavailable},
		},
		{
			name:   "active 1P without HelmManager falls back to prober",
			cms:    &mockCMS{},
			prober: &mockProber{healthy: false, msg: "probe failed"},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 1, Spec: spec1P,
				HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState:  componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
			wantHealth: &healthConditions{ReadyStatus: ConditionStatusFalse, DegradedStatus: ConditionStatusTrue, Reason: ReasonHealthProbeFailed},
		},
		{
			name: "deletion blocked by protection",
			cms:  &mockCMS{},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3P,
				IsDeleting: true, HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name: "deletion successful 1P",
			cms:  &mockCMS{},
			helm: &mockHelm{active: true},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 2, Spec: spec1P,
				IsDeleting: true, HasFinalizer: true, DeletionProtection: false,
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
				IsDeleting: true, HasFinalizer: true, DeletionProtection: false,
			},
			wantState:     componentspb.ComponentState_COMPONENT_STATE_DELETED,
			wantRemoveFin: true,
		},
		{
			name: "deletion successful 3P",
			cms:  &mockCMS{},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 2, Spec: spec3P,
				IsDeleting: true, HasFinalizer: true, DeletionProtection: false,
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
				IsDeleting: true, HasFinalizer: true, DeletionProtection: false,
			},
			wantErr: true,
		},
		{
			name: "deletion CMS failure",
			cms:  &mockCMS{delErr: errors.New("cms spanner error")},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 2, Spec: spec1P,
				IsDeleting: true, HasFinalizer: true,
			},
			wantErr: true,
		},
		{
			name: "deletion CMS validation failure blocks 1P teardown",
			cms:  &mockCMS{delErr: status.Error(codes.FailedPrecondition, "active dependents exist")},
			helm: &mockHelm{active: true},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 2, Spec: spec1P,
				IsDeleting: true, HasFinalizer: true,
			},
			wantErr: true,
		},
		{
			name: "deletion CMS NotFound idempotent retry proceeds to 1P teardown",
			cms:  &mockCMS{delErr: status.Error(codes.NotFound, "already deleted")},
			helm: &mockHelm{active: true},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 2, Spec: spec1P,
				IsDeleting: true, HasFinalizer: true,
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
				HasFinalizer: true, DeletionProtection: true, Enabled: false,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_DISABLED,
		},
		{
			name: "disabled component 1P Helm delete failure",
			cms:  &mockCMS{},
			helm: &mockHelm{active: true, deleteErr: errors.New("cannot stop release")},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "ingestion", Generation: 2, Spec: spec1P,
				HasFinalizer: true, DeletionProtection: true, Enabled: false,
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
				HasFinalizer: true, DeletionProtection: true, Enabled: false,
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
				HasFinalizer: true, DeletionProtection: true, Enabled: false,
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
				HasFinalizer: true, DeletionProtection: true, Enabled: false,
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
				HasFinalizer: true, DeletionProtection: true, Enabled: false,
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
				HasFinalizer: true, DeletionProtection: true, Enabled: true,
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
				HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name:   "health probe unhealthy",
			cms:    &mockCMS{},
			prober: &mockProber{healthy: false, msg: "Pod CrashLoop"},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef,
				HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name:   "health probe healthy",
			cms:    &mockCMS{},
			prober: &mockProber{healthy: true, msg: "Service active"},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef,
				HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		},
		{
			name:   "3P wrapper status detection error",
			cms:    &mockCMS{},
			prober: &mockProber{detectErr: errors.New("k8s api error")},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef, HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantErr: true,
		},
		{
			name:   "3P wrapper resource not found",
			cms:    &mockCMS{},
			prober: &mockProber{status: &ResourceStatus{Found: false, Ready: false, Message: "resource not found"}},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef, HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name:   "3P wrapper unready with empty message fallback",
			cms:    &mockCMS{},
			prober: &mockProber{status: &ResourceStatus{Found: false, Ready: false, Message: ""}},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef, HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name:   "3P wrapper resource not ready",
			cms:    &mockCMS{},
			prober: &mockProber{status: &ResourceStatus{Found: true, Ready: false, Message: "reconciling pods"}},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef, HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name:   "3P wrapper active probe error",
			cms:    &mockCMS{},
			prober: &mockProber{status: &ResourceStatus{Found: true, Ready: true, Endpoint: "http://clickhouse.xmc:8123"}, probeErr: errors.New("connection refused")},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef, HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name:   "3P wrapper active probe unhealthy",
			cms:    &mockCMS{},
			prober: &mockProber{status: &ResourceStatus{Found: true, Ready: true, Endpoint: "http://clickhouse.xmc:8123"}, probeRes: &HealthProbeResult{Healthy: false, Message: "503 Service Unavailable"}},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef, HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name:   "3P wrapper status detector returns nil status without error",
			cms:    &mockCMS{},
			prober: &mockProber{nilStatus: true},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef, HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantErr: true,
		},
		{
			name:   "3P wrapper prober returns nil probe result without error",
			cms:    &mockCMS{},
			prober: &mockProber{status: &ResourceStatus{Found: true, Ready: true, Endpoint: "http://clickhouse.xmc:8123"}, hasProbeRes: true, probeRes: nil},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef, HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name:   "3P wrapper with nil prober returns enabled",
			cms:    &mockCMS{},
			prober: nil,
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef, HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		},
		{
			name:   "3P wrapper fallback nil wrapperRef with unhealthy prober",
			cms:    &mockCMS{},
			prober: &mockProber{healthy: false, msg: "fallback health probe failed"},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3P, HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantState: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		},
		{
			name:   "3P wrapper handle3PUnreadyStatus CMS error",
			cms:    &mockCMS{obsErr: errors.New("cms update failed")},
			prober: &mockProber{status: &ResourceStatus{Found: false, Ready: false}},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef, HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantErr: true,
		},
		{
			name:   "3P wrapper handle3PReadyStatus CMS error",
			cms:    &mockCMS{obsErr: errors.New("cms update failed")},
			prober: &mockProber{status: &ResourceStatus{Found: true, Ready: true}},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3PWithRef, HasFinalizer: true, DeletionProtection: true, Enabled: true,
			},
			wantErr: true,
		},
		{
			name: "update observed CMS error",
			cms:  &mockCMS{obsErr: errors.New("timeout")},
			in: ReconcileComponentInput{
				Namespace: "xmc", Name: "clickhouse", Generation: 1, Spec: spec3P,
				HasFinalizer: true, DeletionProtection: true, Enabled: true,
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
				if res.AddFinalizer != tt.wantAddFin {
					t.Errorf("got AddFinalizer %v, want %v", res.AddFinalizer, tt.wantAddFin)
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
			if tt.name == "deletion blocked by protection" {
				assertConditionStatus(t, res.Conditions, ConditionTypeReady, ConditionStatusFalse)
				assertConditionStatus(t, res.Conditions, ConditionTypeDegraded, ConditionStatusFalse)
				assertConditionStatus(t, res.Conditions, ConditionTypeDeletionBlocked, ConditionStatusTrue)
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
		Spec:         &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT},
		HasFinalizer: true, DeletionProtection: true, Enabled: true,
		ExistingConditions: []Condition{
			{Type: ConditionTypeReady, Status: ConditionStatusTrue, ObservedGeneration: 1, Reason: ReasonComponentHealthy},
			{Type: ConditionTypeDegraded, Status: ConditionStatusFalse, ObservedGeneration: 1, Reason: ReasonComponentHealthy},
		},
	}

	res, err := r.ReconcileComponent(ctx, in)
	if err != nil {
		t.Fatalf("ReconcileComponent() while upgrade in flight failed: %v", err)
	}
	wantPending := healthConditions{ReadyStatus: ConditionStatusFalse, DegradedStatus: ConditionStatusFalse, Reason: ReasonHelmUpgradePending}
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
	wantHealthy := healthConditions{ReadyStatus: ConditionStatusTrue, DegradedStatus: ConditionStatusFalse, Reason: ReasonComponentHealthy}
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
		Spec:         &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT},
		HasFinalizer: true, DeletionProtection: true, Enabled: true,
		ExistingConditions: []Condition{
			{Type: ConditionTypeReady, Status: ConditionStatusTrue, ObservedGeneration: 1, Reason: ReasonComponentHealthy},
			{Type: ConditionTypeDegraded, Status: ConditionStatusFalse, ObservedGeneration: 1, Reason: ReasonComponentHealthy},
		},
	}

	res, err := r.ReconcileComponent(ctx, in)
	if err != nil {
		t.Fatalf("ReconcileComponent() detecting drift failed: %v", err)
	}
	wantDrifted := healthConditions{ReadyStatus: ConditionStatusFalse, DegradedStatus: ConditionStatusTrue, Reason: ReasonHelmReleaseDrifted}
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
	wantHealthy := healthConditions{ReadyStatus: ConditionStatusTrue, DegradedStatus: ConditionStatusFalse, Reason: ReasonComponentHealthy}
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
		Spec:         &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, Dependencies: []string{"redpanda"}},
		HasFinalizer: true, DeletionProtection: true, Enabled: true, Dependencies: unready,
		ExistingConditions: []Condition{
			{Type: ConditionTypeReady, Status: ConditionStatusTrue, ObservedGeneration: 1, Reason: ReasonComponentHealthy},
			{Type: ConditionTypeDegraded, Status: ConditionStatusFalse, ObservedGeneration: 1, Reason: ReasonComponentHealthy},
		},
	}
	type outcome struct {
		Health            healthConditions
		DependenciesReady string
		UpgradeCalls      int
	}
	passes := []outcome{
		{Health: healthConditions{ReadyStatus: ConditionStatusFalse, DegradedStatus: ConditionStatusTrue, Reason: ReasonHelmReleaseDrifted}, DependenciesReady: ConditionStatusFalse, UpgradeCalls: 0},
		{Health: healthConditions{ReadyStatus: ConditionStatusTrue, DegradedStatus: ConditionStatusFalse, Reason: ReasonComponentHealthy}, DependenciesReady: ConditionStatusFalse, UpgradeCalls: 1},
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
				Namespace: "xmc", Name: "ingestion", Generation: tt.generation, HasFinalizer: true, Enabled: true,
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

// healthConditions captures the Ready/Degraded condition statuses and the Ready reason set by evaluateHealth.
type healthConditions struct {
	ReadyStatus    string
	DegradedStatus string
	Reason         string
}

func extractHealthConditions(conds []Condition) healthConditions {
	var got healthConditions
	for _, c := range conds {
		switch c.Type {
		case ConditionTypeReady:
			got.ReadyStatus, got.Reason = c.Status, c.Reason
		case ConditionTypeDegraded:
			got.DegradedStatus = c.Status
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
		assertConditionStatus(t, res.Conditions, ConditionTypeDegraded, ConditionStatusFalse)
		return
	}

	assertConditionStatus(t, res.Conditions, ConditionTypeReady, ConditionStatusFalse)
	assertConditionStatus(t, res.Conditions, ConditionTypeDegraded, ConditionStatusTrue)
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
		Namespace:          "xmc",
		Name:               "ingestion-gateway",
		Generation:         1,
		Spec:               spec1P,
		Enabled:            true,
		DeletionProtection: true,
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
		Namespace:    "xmc",
		Name:         "ingestion-gateway",
		Generation:   4,
		Spec:         &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT},
		Enabled:      true,
		HasFinalizer: true,
		ExistingConditions: []Condition{
			{
				Type:               ConditionTypeReady,
				Status:             ConditionStatusTrue,
				ObservedGeneration: 4,
				Reason:             "ComponentHealthy",
				LastTransitionTime: time.Now().UTC(),
			},
			{
				Type:               ConditionTypeDegraded,
				Status:             ConditionStatusFalse,
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
	ready := Condition{Type: ConditionTypeReady, Status: ConditionStatusTrue, ObservedGeneration: 3, Reason: "ComponentHealthy"}
	degraded := Condition{Type: ConditionTypeDegraded, Status: ConditionStatusFalse, ObservedGeneration: 3, Reason: "ComponentHealthy"}
	in := ReconcileComponentInput{Generation: 3, ExistingConditions: []Condition{ready, degraded}}
	res := &ReconcileResult{Conditions: []Condition{{Type: ConditionTypeReady, Status: ConditionStatusFalse, Reason: "Stale"}}}

	if !preserveHealthConditions(in, res) {
		t.Fatal("preserveHealthConditions() = false, want true")
	}
	if diff := cmp.Diff([]Condition{ready, degraded}, res.Conditions); diff != "" {
		t.Errorf("preserveHealthConditions() conditions mismatch (-want +got):\n%s", diff)
	}
}

func TestReconcileComponent_DependencyActivationGating(t *testing.T) {
	spec1P := func(deps ...string) *componentspb.ComponentSpec {
		return &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, Dependencies: deps}
	}
	spec3P := &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER, Dependencies: []string{"redpanda"}}
	snapshot := func(items ...unstructured.Unstructured) DependencyReadiness { return NewDependencySnapshot(items) }
	notRegisteredErr := status.Error(codes.FailedPrecondition, store.DependencyNotFoundMessagePrefix+` component "ingestion" depends on "redpanda", which is not registered`)
	// Intent is registered with CMS even while activation is gated.
	gated := gateOutcome{
		State:       componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		ReadyStatus: ConditionStatusFalse, ReadyReason: ReasonDependenciesNotReady, DegradedStatus: ConditionStatusFalse,
		SetCalls: 1,
	}
	actuated := gateOutcome{
		State:       componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		ReadyStatus: ConditionStatusTrue, ReadyReason: ReasonComponentHealthy, DegradedStatus: ConditionStatusFalse,
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
		ReadyStatus: ConditionStatusFalse, ReadyReason: ReasonHelmUpgradePending, DegradedStatus: ConditionStatusFalse,
		DependenciesReady: ConditionStatusFalse, SetCalls: 1,
	}
	// keptReady has nothing to upgrade, so Ready stays health-derived while dependencies are unready.
	keptReady := gateOutcome{
		State:       componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		ReadyStatus: ConditionStatusTrue, ReadyReason: ReasonComponentHealthy, DegradedStatus: ConditionStatusFalse,
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
			name:    "dependency Degraded",
			spec:    spec1P("redpanda"),
			deps:    snapshot(readyComponent("redpanda", func(u *unstructured.Unstructured) { setConditionStatus(u, 1, "status", ConditionStatusTrue) })),
			want:    gated,
			wantMsg: "waiting on dependencies: redpanda (degraded)",
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
				ReadyStatus: ConditionStatusFalse, ReadyReason: "IntentApplicationFailed", DegradedStatus: ConditionStatusTrue, SetCalls: 1,
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
				ReadyStatus: ConditionStatusFalse, ReadyReason: ReasonDependencyCycle, DegradedStatus: ConditionStatusTrue,
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
				Namespace: "xmc", Name: "ingestion", Generation: 2, Spec: tt.spec, HasFinalizer: true,
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
	healthy := gateOutcome{State: componentspb.ComponentState_COMPONENT_STATE_ENABLED, ReadyStatus: ConditionStatusTrue, ReadyReason: ReasonComponentHealthy, DegradedStatus: ConditionStatusFalse}
	pending := gateOutcome{State: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION, ReadyStatus: ConditionStatusFalse, ReadyReason: ReasonHelmUpgradePending, DegradedStatus: ConditionStatusFalse}
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
			Namespace: "xmc", Name: "ingestion", Generation: p.generation, HasFinalizer: true, Enabled: true,
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

func TestDependencyNotRegisteredMessage(t *testing.T) {
	if dependencyNotFoundMessagePrefix != store.DependencyNotFoundMessagePrefix {
		t.Fatalf("dependencyNotFoundMessagePrefix = %q, want store.DependencyNotFoundMessagePrefix %q", dependencyNotFoundMessagePrefix, store.DependencyNotFoundMessagePrefix)
	}
	const msg = store.DependencyNotFoundMessagePrefix + ` component "a" depends on "b", which is not registered`
	notRegistered := status.Error(codes.FailedPrecondition, msg)
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "nil", err: nil, want: ""},
		{name: "unregistered dependency", err: notRegistered, want: msg},
		{name: "wrapped unregistered dependency", err: fmt.Errorf("failed to apply component intent to CMS: %w", notRegistered), want: msg},
		{name: "other FailedPrecondition", err: status.Error(codes.FailedPrecondition, "dependency cycle detected: a -> b -> a"), want: ""},
		{name: "other code with the prefix", err: status.Error(codes.Unavailable, msg), want: ""},
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
		Namespace: "xmc", Name: "ingestion", Generation: 1, HasFinalizer: true, Enabled: true,
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

// TestReconciler_EnvtestIntegration verifies controller reconciliation, finalizer synchronization,
// Server-Side Apply condition merging (fieldManager: "xmc-bridge"), and malformed 3P status
// contract breach handling (ReasonContractBroken) against a local Kubebuilder envtest API server.
func TestReconciler_EnvtestIntegration(t *testing.T) {
	dynClient := startReconcilerEnvtest(t)
	ctx := context.Background()

	t.Run("ServerSideApplyMergesExternalConditionsAndSyncsFinalizers", func(t *testing.T) {
		verifyEnvtestSSAMergeAndFinalizers(ctx, t, dynClient)
	})
	t.Run("ContractBrokenDegradedStatusOnMalformed3PPayload", func(t *testing.T) {
		verifyEnvtestContractBreach(ctx, t, dynClient)
	})
}

func startReconcilerEnvtest(t *testing.T) dynamic.Interface {
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
	return dynClient
}

func verifyEnvtestSSAMergeAndFinalizers(ctx context.Context, t *testing.T, dynClient dynamic.Interface) {
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
	reconcileAndApplyBridgeStatus(ctx, t, dynClient, compClient, created.GetName())
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

func reconcileAndApplyBridgeStatus(ctx context.Context, t *testing.T, dynClient dynamic.Interface, compClient dynamic.ResourceInterface, name string) {
	t.Helper()
	live, err := compClient.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get Component failed: %v", err)
	}
	rec, err := NewReconciler(&mockCMS{}, nil, &mockHelm{})
	if err != nil {
		t.Fatalf("NewReconciler failed: %v", err)
	}
	res, err := rec.ReconcileWithDependencies(ctx, live, nil)
	if err != nil {
		t.Fatalf("ReconcileWithDependencies failed: %v", err)
	}
	if live, err = SyncComponentFinalizers(ctx, dynClient, "default", live, res); err != nil {
		t.Fatalf("SyncComponentFinalizers failed: %v", err)
	}
	if err := ApplyComponentStatusSSA(ctx, dynClient, "default", live, res); err != nil {
		t.Fatalf("ApplyComponentStatusSSA failed: %v", err)
	}
}

func assertMergedConditionsAndFinalizer(ctx context.Context, t *testing.T, compClient dynamic.ResourceInterface, name string) {
	t.Helper()
	updated, err := compClient.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get updated Component failed: %v", err)
	}
	if !slices.Contains(updated.GetFinalizers(), FinalizerDeletionProtection) {
		t.Errorf("expected finalizer %q on Component, got %v", FinalizerDeletionProtection, updated.GetFinalizers())
	}
	conds, _, _ := unstructured.NestedSlice(updated.Object, "status", "conditions")
	seen := make(map[string]string)
	for _, raw := range conds {
		if m, ok := raw.(map[string]any); ok {
			seen[m["type"].(string)] = m["status"].(string)
		}
	}
	want := map[string]string{
		"ExternalSLA":         "True",
		ConditionTypeReady:    ConditionStatusTrue,
		ConditionTypeDegraded: ConditionStatusFalse,
	}
	if diff := cmp.Diff(want, seen); diff != "" {
		t.Errorf("merged conditions mismatch (-want +got):\n%s", diff)
	}
}

func verifyEnvtestContractBreach(ctx context.Context, t *testing.T, dynClient dynamic.Interface) {
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
	reconcileMalformedWrapper(ctx, t, dynClient, created)
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

func reconcileMalformedWrapper(ctx context.Context, t *testing.T, dynClient dynamic.Interface, live *unstructured.Unstructured) {
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
	if err := ApplyComponentStatusSSA(ctx, dynClient, "default", live, res); err != nil {
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
			if typ == ConditionTypeReady || typ == ConditionTypeDegraded {
				seen[typ] = fmt.Sprintf("%s/%s", m["status"], m["reason"])
			}
		}
	}
	want := map[string]string{
		ConditionTypeReady:    fmt.Sprintf("%s/%s", ConditionStatusFalse, ReasonContractBroken),
		ConditionTypeDegraded: fmt.Sprintf("%s/%s", ConditionStatusTrue, ReasonContractBroken),
	}
	if diff := cmp.Diff(want, seen); diff != "" {
		t.Errorf("contract broken conditions mismatch (-want +got):\n%s", diff)
	}
}
