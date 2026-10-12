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
	"maps"
	"slices"
	"time"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

const (
	ConditionTypeReady               = "Ready"
	ConditionTypeSynced              = "Synced"
	ConditionTypeReconciling         = "Reconciling"
	ConditionTypeStalled             = "Stalled"
	ConditionTypeDeactivationBlocked = "DeactivationBlocked"
	ConditionStatusTrue              = "True"
	ConditionStatusFalse             = "False"
	ConditionStatusUnknown           = "Unknown"
	// ConditionTypeDependenciesReady is informational on actuated components with dependencies; it does not affect Ready.
	ConditionTypeDependenciesReady = "DependenciesReady"
)

// Ready, Synced, Reconciling, and Stalled condition reasons reported by component reconciliation.
const (
	// ReasonCMSSynced indicates the component's intent and observed state are synchronized with CMS.
	ReasonCMSSynced = "CMSSynced"
	// ReasonCMSSyncFailed indicates a CMS RPC failed while synchronizing intent or observed state.
	ReasonCMSSyncFailed = "CMSSyncFailed"
	// ReasonInitialSyncPending indicates CMS failed before initial component readiness was evaluated.
	ReasonInitialSyncPending = "InitialSyncPending"
	// ReasonProgressing indicates the component is actively reconciling or awaiting workload readiness.
	ReasonProgressing = "Progressing"
	// ReasonDisabling indicates the component is being deactivated or completing its CMS deactivation handshake.
	ReasonDisabling = "Disabling"
	// ReasonDependenciesPending indicates the component is waiting on unready dependencies before activating.
	ReasonDependenciesPending = "DependenciesPending"
	// ReasonUpgradePending indicates a Helm upgrade is queued, in flight, or held on unready dependencies.
	ReasonUpgradePending = "UpgradePending"
	// ReasonComponentHealthy indicates the component is healthy (for 1P: Helm release deployed, no upgrade pending or failed).
	ReasonComponentHealthy = "ComponentHealthy"
	// ReasonHealthProbeFailed indicates the prober-based health check failed.
	ReasonHealthProbeFailed = "HealthProbeFailed"
	// ReasonInvalidComponentRef indicates the component reference is missing a name or namespace.
	ReasonInvalidComponentRef = "InvalidComponentRef"
	// ReasonHelmUpgradeFailed indicates the last Helm upgrade for the component failed.
	ReasonHelmUpgradeFailed = "HelmUpgradeFailed"
	// ReasonHelmUpgradePending indicates a Helm upgrade is queued or running; progress, not a terminal fault.
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
	// ReasonContractBroken indicates that a 3P wrapper target resource violated the status contract
	// (e.g. malformed status payload, unparseable observedGeneration, or JSONPath execution failure against the target object).
	ReasonContractBroken = "ContractBroken"
	// ReasonInvalidWrapperSpec indicates that the Component spec's wrapperRef contains an invalid JSONPath expression.
	ReasonInvalidWrapperSpec = "InvalidWrapperSpec"
	// ReasonStatusDetectionFailed indicates a failure querying the 3P status detector or Kubernetes API.
	ReasonStatusDetectionFailed = "StatusDetectionFailed"
)

const cmsSyncedMessage = "Component synchronized with CMS"

// Condition represents a standardized Kubernetes custom resource condition.
type Condition struct {
	// Type is the condition identifier (e.g. Ready, Synced, Reconciling, Stalled, DeactivationBlocked).
	Type string
	// Status is the condition state ("True", "False", or "Unknown").
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

// setReadiness records the Ready condition and manages kstatus abnormal-true conditions (Reconciling and Stalled).
// Settled states (Ready=True or ComponentDisabled) omit both Reconciling and Stalled.
func setReadiness(res *ReconcileResult, in ReconcileComponentInput, ready, reason, msg string) {
	setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeReady, ready, in.Generation, reason, msg)
	deleteCondition(&res.Conditions, ConditionTypeReconciling)
	deleteCondition(&res.Conditions, ConditionTypeStalled)
	if ready == ConditionStatusTrue || reason == reasonComponentDisabled {
		return
	}
	if isStalledReason(reason) {
		setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeStalled, ConditionStatusTrue, in.Generation, reason, msg)
		return
	}
	setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeReconciling, ConditionStatusTrue, in.Generation, reconcilingReason(reason), msg)
}

func isStalledReason(reason string) bool {
	switch reason {
	case ReasonDependencyCycle, ReasonDeactivationBlocked, ReasonContractBroken,
		ReasonInvalidWrapperSpec, "InvalidWrapperRef", "CrossNamespaceForbidden", "CRDNotFound",
		ReasonInvalidComponentRef, ReasonHelmUpgradeFailed, ReasonHelmReleaseStuck, ReasonHelmRunnerUninitialized:
		return true
	}
	return false
}

func reconcilingReason(readyReason string) string {
	switch readyReason {
	case ReasonDependenciesNotReady:
		return ReasonDependenciesPending
	case ReasonHelmUpgradePending:
		return ReasonUpgradePending
	case ReasonDeactivationIncomplete, "HelmDeletionFailed", "HelmOrphanFailed":
		return ReasonDisabling
	default:
		return ReasonProgressing
	}
}

// deleteCondition removes every condition of condType from conditions.
func deleteCondition(conditions *[]Condition, condType string) {
	*conditions = slices.DeleteFunc(*conditions, func(c Condition) bool {
		return c.Type == condType
	})
}

// setSynced records the Synced condition on res.
func setSynced(res *ReconcileResult, in ReconcileComponentInput, status, reason, msg string) {
	setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeSynced, status, in.Generation, reason, msg)
}

// recordCMSSyncFailure records Synced=False when a CMS RPC fails before workload health is
// evaluated in the current pass, preserving the previous Ready/Reconciling/Stalled conditions and
// status.access (or reporting Ready=Unknown/InitialSyncPending on first reconcile). Ready=True is
// preserved only while spec.enabled: a disabled component whose deactivation cannot reach CMS
// reports Ready=Unknown/CMSSyncFailed instead.
func recordCMSSyncFailure(res *ReconcileResult, in ReconcileComponentInput, err error) {
	res.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
	setSynced(res, in, ConditionStatusFalse, ReasonCMSSyncFailed, err.Error())
	preserveExistingAccess(res, in.ExistingAccess)
	existingReady := findCondition(in.ExistingConditions, ConditionTypeReady)
	if existingReady == nil {
		setReadiness(res, in, ConditionStatusUnknown, ReasonInitialSyncPending, err.Error())
		return
	}
	if !in.Enabled && existingReady.Status == ConditionStatusTrue {
		setReadiness(res, in, ConditionStatusUnknown, ReasonCMSSyncFailed, "deactivation is pending until CMS is reachable: "+err.Error())
		return
	}
	if in.Enabled && !in.IsDeleting && existingReady.Status == ConditionStatusTrue {
		res.State = componentspb.ComponentState_COMPONENT_STATE_ENABLED
	}
	prev := in
	prev.Generation = existingReady.ObservedGeneration
	setReadiness(res, prev, existingReady.Status, existingReady.Reason, existingReady.Message)
	upsertCondition(&res.Conditions, *existingReady)
}

func preserveExistingAccess(res *ReconcileResult, existing *AccessStatus) {
	if res == nil || existing == nil || len(existing.EnvVars) == 0 {
		return
	}
	res.Access = &AccessStatus{EnvVars: maps.Clone(existing.EnvVars)}
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

// isCMSSynced reports whether existingConditions has no non-True Synced condition (meaning the last
// reconcile pass did not leave the CR in a Synced=False state).
func isCMSSynced(existingConditions []Condition) bool {
	existingSynced := findCondition(existingConditions, ConditionTypeSynced)
	return existingSynced == nil || existingSynced.Status == ConditionStatusTrue
}
