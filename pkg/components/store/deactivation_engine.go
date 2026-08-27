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

package store

import (
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

const (
	labelDeletionProtection = "xmc.dev/deletion-protection"
	minDrainTimeout         = 30 * time.Second
)

// IsDeletionProtected checks if a component record is marked with deletion protection labels.
func IsDeletionProtected(rec *ComponentRecord) bool {
	if rec == nil || rec.Labels == nil {
		return false
	}
	val := strings.ToLower(strings.TrimSpace(rec.Labels[labelDeletionProtection]))
	return val == "true" || val == "1"
}

// CalculateDrainTimeout determines the required drain duration from overrides, spec configs, and minimum defaults.
func CalculateDrainTimeout(rec *ComponentRecord, cascaded []*ComponentRecord, reqTimeout *durationpb.Duration) time.Duration {
	if reqTimeout != nil && reqTimeout.CheckValid() == nil && reqTimeout.AsDuration() > 0 {
		return reqTimeout.AsDuration()
	}

	maxDuration := extractRecordDrainTimeout(rec)
	for _, c := range cascaded {
		if d := extractRecordDrainTimeout(c); d > maxDuration {
			maxDuration = d
		}
	}

	if maxDuration < minDrainTimeout {
		return minDrainTimeout
	}
	return maxDuration
}

// extractRecordDrainTimeout retrieves the maximum lifecycle draining or grace period timeout for a record.
// Note: A spec can declare both DrainingTimeout and GracePeriod simultaneously; the larger duration governs.
func extractRecordDrainTimeout(rec *ComponentRecord) time.Duration {
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
func EvaluateDeactivationSafety(components map[string]*ComponentRecord, targetName string, requestCascade bool, minDrainFloor time.Duration) (*PrepareDeactivationResult, error) {
	canonicalName := CanonicalComponentName(targetName)
	targetRec, exists := components[canonicalName]
	if !exists || targetRec == nil || isDeletedRecord(targetRec) {
		return nil, ErrComponentNotFound
	}

	activeDependents, cascadedRecs := findActiveDependents(components, canonicalName)
	if len(activeDependents) > 0 && !requestCascade {
		return &PrepareDeactivationResult{
			IsSafeToDeactivate: false,
			BlockingDependents: activeDependents,
			RejectionReason:    "component has active dependents",
		}, ErrHasActiveDependents
	}

	drainTimeout := CalculateDrainTimeout(targetRec, cascadedRecs, nil)
	if minDrainFloor > drainTimeout {
		drainTimeout = minDrainFloor
	}

	return &PrepareDeactivationResult{
		IsSafeToDeactivate:    true,
		RecommendedDrainSec:   int32(drainTimeout / time.Second),
		CascadedDeactivations: activeDependents,
	}, nil
}

// findActiveDependents collects names and records of all active, non-disabled transitive dependents.
func findActiveDependents(components map[string]*ComponentRecord, rootCanonical string) ([]string, []*ComponentRecord) {
	records := make([]*ComponentRecord, 0, len(components))
	for _, r := range components {
		if !isDeletedRecord(r) {
			records = append(records, r)
		}
	}

	dependentsGraph := BuildDependentsGraph(records)
	transitive := FindTransitiveDependents(dependentsGraph, rootCanonical)

	var activeNames []string
	var activeRecords []*ComponentRecord
	for _, depName := range transitive {
		depRec, ok := components[depName]
		if !ok || depRec == nil || isDeletedRecord(depRec) || depRec.State == componentspb.ComponentState_COMPONENT_STATE_DISABLED {
			continue
		}
		activeNames = append(activeNames, depName)
		activeRecords = append(activeRecords, depRec)
	}
	return activeNames, activeRecords
}

// findAllDependents collects names and records of all transitive dependents (both active and disabled).
func findAllDependents(components map[string]*ComponentRecord, rootCanonical string) ([]string, []*ComponentRecord) {
	records := make([]*ComponentRecord, 0, len(components))
	for _, r := range components {
		if !isDeletedRecord(r) {
			records = append(records, r)
		}
	}

	dependentsGraph := BuildDependentsGraph(records)
	transitive := FindTransitiveDependents(dependentsGraph, rootCanonical)

	var names []string
	var recs []*ComponentRecord
	for _, depName := range transitive {
		depRec, ok := components[depName]
		if !ok || depRec == nil || isDeletedRecord(depRec) {
			continue
		}
		names = append(names, depName)
		recs = append(recs, depRec)
	}
	return names, recs
}

// ValidateDeletionSafety checks if a component can be deleted without violating protection or dependency constraints.
func ValidateDeletionSafety(components map[string]*ComponentRecord, targetName string, policy componentspb.DeletionPolicy, force bool) error {
	canonicalName := CanonicalComponentName(targetName)
	targetRec, exists := components[canonicalName]
	if !exists || targetRec == nil || isDeletedRecord(targetRec) {
		return ErrComponentNotFound
	}
	if force {
		return nil
	}
	if IsDeletionProtected(targetRec) {
		return ErrDeletionProtected
	}

	// Per api/protos/components/v1alpha1/components.proto (line 44), DELETION_POLICY_UNSPECIFIED
	// defaults to DELETION_POLICY_DELETE (cascading resource deletion across dependents).
	if policy == componentspb.DeletionPolicy_DELETION_POLICY_DELETE || policy == componentspb.DeletionPolicy_DELETION_POLICY_UNSPECIFIED {
		_, allRecords := findAllDependents(components, canonicalName)
		for _, depRec := range allRecords {
			if IsDeletionProtected(depRec) {
				return ErrDeletionProtected
			}
		}
		return nil
	}

	activeDependents, _ := findActiveDependents(components, canonicalName)
	if len(activeDependents) > 0 {
		return ErrHasActiveDependents
	}
	return nil
}
