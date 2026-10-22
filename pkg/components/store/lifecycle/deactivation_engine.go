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

// Package lifecycle provides component deactivation safety evaluation, snapshot resolution,
// requirement validation, and platform health aggregation for the XMC Component Store.
package lifecycle

import (
	"fmt"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/durationpb"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/naming"
	"xmanager_cloud/pkg/components/store"
	"xmanager_cloud/pkg/components/store/dependency"
)

const (
	labelDeletionProtection = "xmc.dev/deletion-protection"
	MinDrainTimeout         = 30 * time.Second
)

// IsDeletionProtected checks if a component record is marked with deletion protection labels.
func IsDeletionProtected(rec *store.ComponentRecord) bool {
	if rec == nil || rec.Labels == nil {
		return false
	}
	val := strings.ToLower(strings.TrimSpace(rec.Labels[labelDeletionProtection]))
	return val == "true"
}

// CalculateDrainTimeout determines the required drain duration from overrides, spec configs, and minimum defaults.
func CalculateDrainTimeout(rec *store.ComponentRecord, cascaded []*store.ComponentRecord, reqTimeout *durationpb.Duration) time.Duration {
	if reqTimeout != nil && reqTimeout.CheckValid() == nil && reqTimeout.AsDuration() > 0 {
		return reqTimeout.AsDuration()
	}

	maxDuration := extractRecordDrainTimeout(rec)
	for _, c := range cascaded {
		if d := extractRecordDrainTimeout(c); d > maxDuration {
			maxDuration = d
		}
	}

	if maxDuration < MinDrainTimeout {
		return MinDrainTimeout
	}
	return maxDuration
}

// extractRecordDrainTimeout retrieves the maximum lifecycle draining or grace period timeout for a record.
// Note: A spec can declare both DrainingTimeout and GracePeriod simultaneously; the larger duration governs.
func extractRecordDrainTimeout(rec *store.ComponentRecord) time.Duration {
	if rec == nil || rec.Spec == nil || rec.Spec.GetLifecycleControl() == nil {
		return 0
	}
	lc := rec.Spec.GetLifecycleControl()
	var maxDuration time.Duration
	if dt := lc.GetDrainingTimeout(); dt != nil && dt.CheckValid() == nil && dt.AsDuration() > 0 {
		maxDuration = dt.AsDuration()
	}
	if gp := lc.GetGracePeriod(); gp != nil && gp.CheckValid() == nil && gp.AsDuration() > maxDuration {
		maxDuration = gp.AsDuration()
	}
	return maxDuration
}

// EvaluateDeactivationSafety evaluates whether a component can safely be deactivated or if cascading is required.
// Note: IsDeletionProtected is intentionally NOT checked here. Deactivation is a non-destructive draining
// lifecycle transition (PrepareDeactivation -> CompleteDeactivation); deletion protection is strictly enforced
// during permanent resource destruction in ValidateDeletionSafety / ExecuteDeletion.
func EvaluateDeactivationSafety(components map[string]*store.ComponentRecord, targetName string, requestCascade bool, minDrainFloor time.Duration) (*store.PrepareDeactivationResult, error) {
	canonicalName := naming.CanonicalComponentName(targetName)
	targetRec, exists := components[canonicalName]
	if !exists || targetRec == nil || store.IsDeletedRecord(targetRec) {
		return nil, store.ErrComponentNotFound
	}

	activeDependents, cascadedRecs := FindActiveDependents(components, canonicalName)
	if len(activeDependents) > 0 && !requestCascade {
		return &store.PrepareDeactivationResult{
			IsSafeToDeactivate: false,
			BlockingDependents: activeDependents,
			RejectionReason:    "component has active dependents",
		}, activeDependentsError(store.OperationPrepareDeactivation, canonicalName, activeDependents)
	}

	drainTimeout := CalculateDrainTimeout(targetRec, cascadedRecs, nil)
	if minDrainFloor > drainTimeout {
		drainTimeout = minDrainFloor
	}

	return &store.PrepareDeactivationResult{
		IsSafeToDeactivate:    true,
		RecommendedDrainSec:   int32(drainTimeout / time.Second),
		CascadedDeactivations: activeDependents,
	}, nil
}

// FindActiveDependents collects names and records of all active, non-disabled transitive dependents.
func FindActiveDependents(components map[string]*store.ComponentRecord, rootCanonical string) ([]string, []*store.ComponentRecord) {
	records := make([]*store.ComponentRecord, 0, len(components))
	for _, r := range components {
		if !store.IsDeletedRecord(r) {
			records = append(records, r)
		}
	}

	dependentsGraph := dependency.BuildDependentsGraph(records)
	transitive := dependency.FindTransitiveDependents(dependentsGraph, rootCanonical)

	var activeNames []string
	var activeRecords []*store.ComponentRecord
	for _, depName := range transitive {
		depRec, ok := components[depName]
		if !ok || depRec == nil || store.IsDeletedRecord(depRec) || depRec.State == componentspb.ComponentState_COMPONENT_STATE_DISABLED {
			continue
		}
		activeNames = append(activeNames, depName)
		activeRecords = append(activeRecords, depRec)
	}
	return activeNames, activeRecords
}

// FindAllDependents collects names and records of all transitive dependents (both active and disabled).
func FindAllDependents(components map[string]*store.ComponentRecord, rootCanonical string) ([]string, []*store.ComponentRecord) {
	records := make([]*store.ComponentRecord, 0, len(components))
	for _, r := range components {
		if !store.IsDeletedRecord(r) {
			records = append(records, r)
		}
	}

	dependentsGraph := dependency.BuildDependentsGraph(records)
	transitive := dependency.FindTransitiveDependents(dependentsGraph, rootCanonical)

	var names []string
	var recs []*store.ComponentRecord
	for _, depName := range transitive {
		depRec, ok := components[depName]
		if !ok || depRec == nil || store.IsDeletedRecord(depRec) {
			continue
		}
		names = append(names, depName)
		recs = append(recs, depRec)
	}
	return names, recs
}

// ValidateDeletionSafety checks if a component can be deleted without violating protection or dependency constraints.
func ValidateDeletionSafety(components map[string]*store.ComponentRecord, targetName string, policy componentspb.DeletionPolicy, force bool) error {
	canonicalName := naming.CanonicalComponentName(targetName)
	targetRec, exists := components[canonicalName]
	if !exists || targetRec == nil || store.IsDeletedRecord(targetRec) {
		return store.ErrComponentNotFound
	}
	if force {
		return nil
	}
	if IsDeletionProtected(targetRec) {
		return store.ErrDeletionProtected
	}

	// Per api/protos/components/v1alpha1/components.proto (line 44), DELETION_POLICY_UNSPECIFIED
	// defaults to DELETION_POLICY_DELETE (cascading resource deletion across dependents).
	if policy == componentspb.DeletionPolicy_DELETION_POLICY_DELETE || policy == componentspb.DeletionPolicy_DELETION_POLICY_UNSPECIFIED {
		_, allRecords := FindAllDependents(components, canonicalName)
		for _, depRec := range allRecords {
			if IsDeletionProtected(depRec) {
				return store.ErrDeletionProtected
			}
		}
		return nil
	}

	activeDependents, _ := FindActiveDependents(components, canonicalName)
	if len(activeDependents) > 0 {
		return activeDependentsError(store.OperationExecuteDeletion, canonicalName, activeDependents)
	}
	return nil
}

// activeDependentsError returns the FailedPrecondition ACTIVE_DEPENDENTS_EXIST rejection of
// operation on canonicalName, which the active dependents still depend on.
func activeDependentsError(operation, canonicalName string, dependents []string) error {
	return store.ErrorWithInfo(codes.FailedPrecondition, store.ReasonActiveDependentsExist,
		map[string]string{store.MetadataKeyOperation: operation, store.MetadataKeyComponent: canonicalName, store.MetadataKeyDependents: strings.Join(dependents, ",")},
		fmt.Sprintf("component %s has active dependents: %v", canonicalName, dependents))
}
