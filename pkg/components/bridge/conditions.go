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
	"time"
)

const (
	ConditionTypeReady               = "Ready"
	ConditionTypeDegraded            = "Degraded"
	ConditionTypeDeletionBlocked     = "DeletionBlocked"
	ConditionTypeDeactivationBlocked = "DeactivationBlocked"
	ConditionStatusTrue              = "True"
	ConditionStatusFalse             = "False"
	// ConditionTypeDependenciesReady is informational on actuated components with dependencies; it does not affect Ready.
	ConditionTypeDependenciesReady = "DependenciesReady"
)

// Ready/Degraded condition reasons reported by component health evaluation.
const (
	// ReasonComponentHealthy indicates the component is healthy (for 1P: Helm release deployed, no upgrade pending or failed).
	ReasonComponentHealthy = "ComponentHealthy"
	// ReasonHealthProbeFailed indicates the prober-based health check failed.
	ReasonHealthProbeFailed = "HealthProbeFailed"
	// ReasonInvalidComponentRef indicates the component reference is missing a name or namespace.
	ReasonInvalidComponentRef = "InvalidComponentRef"
	// ReasonHelmUpgradeFailed indicates the last Helm upgrade for the component failed.
	ReasonHelmUpgradeFailed = "HelmUpgradeFailed"
	// ReasonHelmUpgradePending indicates a Helm upgrade is queued or running; progress, not a fault (Degraded=False).
	ReasonHelmUpgradePending = "HelmUpgradePending"
	// ReasonHelmRunnerUninitialized indicates no Helm action runner is configured, so release state cannot be verified.
	ReasonHelmRunnerUninitialized = "HelmRunnerUninitialized"
	// ReasonHelmReleaseNotFound indicates the component's Helm release does not exist.
	ReasonHelmReleaseNotFound = "HelmReleaseNotFound"
	// ReasonHelmReleaseNotReady indicates the latest release revision is in a terminal non-deployed state (e.g. failed).
	ReasonHelmReleaseNotReady = "HelmReleaseNotReady"
	// ReasonHelmReleaseStuck indicates the release is pending-install/upgrade/rollback with no upgrade in flight in this bridge.
	ReasonHelmReleaseStuck = "HelmReleaseStuck"
	// ReasonHelmStatusUnavailable indicates a transient failure querying Helm release status; health is left unchanged.
	ReasonHelmStatusUnavailable = "HelmStatusUnavailable"
	// ReasonHelmReleaseDrifted indicates objects rendered by the deployed Helm release are missing from the cluster;
	// the bridge re-applies the release at the current generation to recreate them.
	ReasonHelmReleaseDrifted = "HelmReleaseDrifted"
)

// Condition represents a standardized Kubernetes custom resource condition.
type Condition struct {
	// Type is the condition identifier (e.g. Ready, Degraded, DeletionBlocked).
	Type string
	// Status is the condition state ("True" or "False").
	Status string
	// Reason is a single-word camelCase reason explaining the condition transition.
	Reason string
	// Message is a human-readable description of the condition details.
	Message string
	// ObservedGeneration records the metadata.generation at the time of evaluation.
	ObservedGeneration int64
	// LastTransitionTime records when the condition transitioned between status values.
	LastTransitionTime time.Time
}

// findCondition returns the first condition of condType, or nil if absent.
func findCondition(conds []Condition, condType string) *Condition {
	for i := range conds {
		if conds[i].Type == condType {
			return &conds[i]
		}
	}
	return nil
}

// setCondition inserts or updates a Condition in the conditions slice. If the condition already
// existed with the same status, its lastTransitionTime is preserved; otherwise, it is updated to time.Now().UTC().
func setCondition(conditions *[]Condition, existing []Condition, condType, status string, gen int64, reason, message string) {
	transitionTime := time.Now().UTC()
	for _, c := range existing {
		if c.Type == condType && c.Status == status && !c.LastTransitionTime.IsZero() {
			transitionTime = c.LastTransitionTime
			break
		}
	}
	upsertCondition(conditions, Condition{Type: condType, Status: status, ObservedGeneration: gen, LastTransitionTime: transitionTime, Reason: reason, Message: message})
}

// setReadiness records the Ready and Degraded conditions of one reconciliation outcome, sharing reason and msg.
func setReadiness(res *ReconcileResult, in ReconcileComponentInput, ready, degraded, reason, msg string) {
	setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeReady, ready, in.Generation, reason, msg)
	setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDegraded, degraded, in.Generation, reason, msg)
}

// upsertCondition replaces the entry in conditions with the same Type as c, or appends c.
func upsertCondition(conditions *[]Condition, c Condition) {
	for i := range *conditions {
		if (*conditions)[i].Type == c.Type {
			(*conditions)[i] = c
			return
		}
	}
	*conditions = append(*conditions, c)
}

// healthConditionStatuses maps a health verdict to Ready/Degraded statuses. Pending upgrades and
// unknown (transient) status are not faults, so they report Degraded=False.
func healthConditionStatuses(healthy bool, reason string) (ready, degraded string) {
	if healthy {
		return ConditionStatusTrue, ConditionStatusFalse
	}
	if reason == ReasonHelmUpgradePending || reason == ReasonHelmStatusUnavailable {
		return ConditionStatusFalse, ConditionStatusFalse
	}
	return ConditionStatusFalse, ConditionStatusTrue
}

const defaultCMSHeartbeatInterval = 5 * time.Minute

// shouldSyncToCMS determines whether an UpdateObservedGeneration RPC to CMS should be executed.
// Under the Hybrid Probing Model (Drift 1), periodic steady-state health probe ticks bypass
// CMS and Cloud Spanner to eliminate write amplification. CMS is only invoked when:
// 1. Initial reconciliation (no existing Ready condition reported).
// 2. State/readiness transitions (e.g. Ready=True <-> Ready=False).
// 3. Metadata generation update (CR spec modified).
// 4. Coarse heartbeat interval expiration (>= 5 minutes).
func shouldSyncToCMS(existingConditions []Condition, targetStatus string, currentGen int64) bool {
	var existingReady *Condition
	for i := range existingConditions {
		if existingConditions[i].Type == ConditionTypeReady {
			existingReady = &existingConditions[i]
			break
		}
	}
	if existingReady == nil {
		return true
	}
	if existingReady.ObservedGeneration != currentGen {
		return true
	}
	if existingReady.Status != targetStatus {
		return true
	}
	if existingReady.LastTransitionTime.IsZero() || time.Since(existingReady.LastTransitionTime) >= defaultCMSHeartbeatInterval {
		return true
	}
	return false
}

// resetReadyTransitionTime updates LastTransitionTime on the Ready condition to current UTC time.
// This resets the heartbeat timer after a successful sync to CMS.
func resetReadyTransitionTime(conditions []Condition) {
	now := time.Now().UTC()
	for i := range conditions {
		if conditions[i].Type == ConditionTypeReady {
			conditions[i].LastTransitionTime = now
			return
		}
	}
}
