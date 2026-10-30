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
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/pkg/components/store"
)

const (
	// FinalizerCMSCleanup holds a Component's deletion until xmc-bridge has removed it from CMS and
	// torn down its workload. It never vetoes deletion: the component-deletion-protection
	// ValidatingAdmissionPolicy enforces spec.deletionProtection.
	FinalizerCMSCleanup = "xmc.dev/cms-cleanup"
	// legacyFinalizerCMSCleanup is the name earlier xmc-bridge versions gave FinalizerCMSCleanup.
	// Components that still carry it are cleaned up as if they carried FinalizerCMSCleanup, and
	// releasing the cleanup finalizer removes both names.
	legacyFinalizerCMSCleanup = "finalizers.xmc.dev/deletion-protection"
	// ReasonDeactivationBlocked marks a disabled component kept running because enabled components depend on it.
	ReasonDeactivationBlocked = "DeactivationBlocked"
	// ReasonDeactivationIncomplete marks a disabled component with no running workload (removed by an
	// earlier pass, or never actuated) whose CMS deactivation handshake has not completed.
	ReasonDeactivationIncomplete = "DeactivationIncomplete"
	reasonComponentDisabled      = "ComponentDisabled"
	// ReasonActiveDependentsExist marks Stalled=True on a deleting component whose non-cascading
	// (Orphan policy) CMS deletion is rejected because active dependents exist; the finalizer is kept.
	ReasonActiveDependentsExist = "ActiveDependentsExist"
	// deactivationRecheckInterval throttles PrepareDeactivation while deactivation is blocked.
	deactivationRecheckInterval = 30 * time.Second
)

// errDeletionBlocked reports that CMS rejected a non-cascading deletion with ACTIVE_DEPENDENTS_EXIST,
// so the workload and the finalizer are kept until the dependents are gone.
var errDeletionBlocked = errors.New("component deletion blocked by active dependents")

// reconcileDeletion tears a deleted Component down: CMS ExecuteDeletion first (a NotFound means
// CMS already deleted it; a source or cluster fence conflict skips CMS deletion so the active
// owner's CMS record stays intact; active dependents of a non-cascading deletion keep the workload
// and finalizer and report Stalled=True), then this CR's own 1P workload per deletionPolicy, then
// the finalizer is released. spec.deletionProtection is not consulted: a deletion that passed
// admission is final.
func (r *Reconciler) reconcileDeletion(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult) (*ReconcileResult, error) {
	if err := r.executeCMSDeletion(ctx, in, canonicalName, res); err != nil {
		if errors.Is(err, errStaleLocalIncarnation) || errors.Is(err, errDeletionBlocked) {
			return res, nil
		}
		return res, err
	}
	if err := r.teardown1PComponent(ctx, in, res); err != nil {
		return res, err
	}
	r.syncTracker.forget(componentKey(in.Namespace, canonicalName))
	res.RemoveFinalizer = true
	res.State = componentspb.ComponentState_COMPONENT_STATE_DELETED
	return res, nil
}

func (r *Reconciler) executeCMSDeletion(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult) error {
	if r.fence != nil {
		reason, msg, err := r.fence.Check(ctx)
		if err != nil {
			recordCMSSyncFailure(res, in, err)
			return fmt.Errorf("failed to check cluster fence before deletion: %w", err)
		}
		if reason != "" {
			recordDeletionConflict(ctx, res, in, canonicalName, reason, msg)
			return nil
		}
	}
	_, err := callWithOwnership(ctx, r, in, func(ctx context.Context, src *componentspb.RequestSource) (*componentspb.ExecuteDeletionResponse, error) {
		return r.cmsClient.ExecuteDeletion(ctx, &componentspb.ExecuteDeletionRequest{
			Name:           canonicalName,
			DeletionPolicy: in.Spec.GetDeletionPolicy(),
			Source:         src,
		})
	})
	if errors.Is(err, errStaleLocalIncarnation) {
		res.StaleIncarnation = true
		res.RequeueAfter = staleComponentRetryDelay
		return errStaleLocalIncarnation
	}
	if conflictReason, msg := deletionConflictFromError(err); conflictReason != "" {
		recordDeletionConflict(ctx, res, in, canonicalName, conflictReason, msg)
		return nil
	}
	if msg := activeDependentsMessage(err); msg != "" {
		reportDeletionBlocked(ctx, res, in, canonicalName, msg)
		return errDeletionBlocked
	}
	if err != nil {
		if status.Code(err) != codes.NotFound {
			recordCMSSyncFailure(res, in, err)
			return fmt.Errorf("failed to execute deletion in CMS: %w", err)
		}
		logging.Info(ctx, "Component already deleted in CMS, proceeding with workload teardown", "name", canonicalName)
	} else {
		logging.Info(ctx, "Executed component deletion in CMS", "name", canonicalName, "policy", in.Spec.GetDeletionPolicy().String())
	}
	setSynced(res, in, ConditionStatusTrue, ReasonCMSSynced, cmsSyncedMessage)
	return nil
}

func recordDeletionConflict(ctx context.Context, res *ReconcileResult, in ReconcileComponentInput, canonicalName, conflictReason, msg string) {
	skipMsg := fmt.Sprintf("Skipped CMS deletion for %q (%s): %s", canonicalName, conflictReason, msg)
	logging.Warn(ctx, "Skipping CMS deletion due to ownership conflict; cleaning up local CR workload and finalizer only", "name", canonicalName, "namespace", in.Namespace, "uid", in.UID, "reason", conflictReason, "detail", msg)
	recordSourceConflict(res, in, conflictReason, skipMsg)
	res.ConflictReason = conflictReason
	res.ConflictMessage = skipMsg
	res.WarningEvent = &WarningEvent{Reason: ReasonStaleDeletion, Message: skipMsg, generateNameSuffix: staleDeletionEventSuffix}
}

// deletionConflictFromError returns the SourceConflict reason and CMS message when err is one of
// the deletion conflicts that skip CMS deletion and release the finalizer: SOURCE_NAMESPACE_CONFLICT,
// SOURCE_INCARNATION_MISMATCH, CLUSTER_FENCED or CLUSTER_EPOCH_REGRESSED. Any other rejection,
// including CLUSTER_NOT_DECLARED left after the re-declare retry, keeps the finalizer and is retried
// so the CMS record never leaks.
func deletionConflictFromError(err error) (string, string) {
	info, _ := extractErrorInfo(err)
	if info == nil || info.GetReason() == store.ReasonClusterNotDeclared {
		return "", ""
	}
	return sourceConflictFromError(err)
}

// activeDependentsMessage returns the CMS status message if err (possibly wrapped) is the store's
// ACTIVE_DEPENDENTS_EXIST rejection, or "" otherwise.
func activeDependentsMessage(err error) string {
	info, msg := extractErrorInfo(err)
	if info == nil || info.GetReason() != store.ReasonActiveDependentsExist {
		return ""
	}
	return msg
}

// reportDeletionBlocked records a non-cascading (Orphan policy) deletion that CMS rejected because
// active dependents exist: Stalled=True (ActiveDependentsExist) with Synced=True, the previous Ready
// condition and access kept (the workload keeps serving its dependents), the finalizer kept, and a
// recheck after deactivationRecheckInterval. A Warning Event is emitted when the block first appears.
func reportDeletionBlocked(ctx context.Context, res *ReconcileResult, in ReconcileComponentInput, canonicalName, cmsMsg string) {
	msg := fmt.Sprintf("Cannot delete %q while active dependents exist: %s", in.Name, cmsMsg)
	logging.Info(ctx, "Component deletion blocked by active dependents", "name", canonicalName, "namespace", in.Namespace, "detail", cmsMsg)
	res.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
	res.RequeueAfter = deactivationRecheckInterval
	preserveExistingAccess(res, in.ExistingAccess)
	setSynced(res, in, ConditionStatusTrue, ReasonCMSSynced, cmsSyncedMessage)
	if ready := findCondition(in.ExistingConditions, ConditionTypeReady); ready != nil {
		upsertCondition(&res.Conditions, *ready)
	}
	deleteCondition(&res.Conditions, ConditionTypeReconciling)
	setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeStalled, ConditionStatusTrue, in.Generation, ReasonActiveDependentsExist, msg)
	if prev := findCondition(in.ExistingConditions, ConditionTypeStalled); prev == nil || prev.Status != ConditionStatusTrue || prev.Reason != ReasonActiveDependentsExist {
		res.WarningEvent = &WarningEvent{Reason: ReasonActiveDependentsExist, Message: msg, generateNameSuffix: blockedDeletionEventSuffix}
	}
}

func (r *Reconciler) teardown1PComponent(ctx context.Context, in ReconcileComponentInput, res *ReconcileResult) error {
	if in.Spec.GetType() != componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT || r.helmManager == nil {
		return nil
	}
	ref := resolveComponentRef(in)
	if in.Spec.GetDeletionPolicy() == componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN {
		if err := r.helmManager.Orphan1PComponent(ctx, ref); err != nil {
			setReadiness(res, in, ConditionStatusFalse, "HelmOrphanFailed", err.Error())
			return fmt.Errorf("failed to orphan 1P Helm component: %w", err)
		}
		return nil
	}
	if err := r.helmManager.Purge1PComponent(ctx, ref); err != nil {
		setReadiness(res, in, ConditionStatusFalse, "HelmDeletionFailed", err.Error())
		return fmt.Errorf("failed to delete 1P Helm component: %w", err)
	}
	return nil
}

// reconcileDisabled deactivates a component (enabled: false). PrepareDeactivation blocks while
// active dependents exist (the workload keeps running; re-checked every deactivationRecheckInterval).
// Otherwise the 1P workload is deleted, CompleteDeactivation finalizes the handshake, and the
// component reports COMPONENT_STATE_DISABLED. Failures after teardown report DeactivationIncomplete.
func (r *Reconciler) reconcileDisabled(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult) (*ReconcileResult, error) {
	key := componentKey(in.Namespace, canonicalName)
	if msg, ok := r.syncTracker.recentDeactivationBlock(key, in); ok {
		return r.reportDeactivationBlocked(ctx, in, canonicalName, res, msg)
	}
	prepared, err := r.prepareDeactivation(ctx, in, canonicalName)
	if handleOwnershipError(res, in, err) {
		return res, nil
	}
	if status.Code(err) == codes.FailedPrecondition {
		msg := fmt.Sprintf("Cannot disable %q: %s", in.Name, status.Convert(err).Message())
		logDeactivationBlocked(ctx, in, canonicalName, msg)
		r.syncTracker.recordDeactivationBlock(key, in.Generation, msg)
		return r.reportDeactivationBlocked(ctx, in, canonicalName, res, msg)
	}
	if err != nil {
		err = fmt.Errorf("failed to prepare deactivation in CMS: %w", err)
		recordCMSSyncFailure(res, in, err)
		return res, err
	}
	if err := r.teardownAndCompleteDeactivation(ctx, in, canonicalName, res, prepared); err != nil || shouldStopReconcile(res) {
		return res, err
	}
	r.syncTracker.clearDeactivationBlock(key)
	return r.reportDisabled(ctx, in, canonicalName, res)
}

// logDeactivationBlocked logs a PrepareDeactivation blocked by active dependents at Info when the
// block is new (no DeactivationBlocked=True condition with the same message yet), and at Debug for
// the periodic recheck of a block that has not changed.
func logDeactivationBlocked(ctx context.Context, in ReconcileComponentInput, canonicalName, msg string) {
	if prev := findCondition(in.ExistingConditions, ConditionTypeDeactivationBlocked); prev != nil && prev.Status == ConditionStatusTrue && prev.Message == msg {
		logging.Debug(ctx, "Component deactivation blocked by active dependents", "name", canonicalName, "generation", in.Generation, "detail", msg)
	} else {
		logging.Info(ctx, "Component deactivation blocked by active dependents", "name", canonicalName, "generation", in.Generation, "detail", msg)
	}
}

// reportDisabled reports the component disabled in res and to CMS. A pass that changes the Ready,
// Reconciling, or Stalled condition (such as the first one after deactivation) logs at Info; an
// unchanged periodic pass logs at Debug. A NotFound from CMS is tolerated; any other CMS failure
// reports DeactivationIncomplete so the next pass retries the deactivation handshake.
func (r *Reconciler) reportDisabled(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult) (*ReconcileResult, error) {
	res.State = componentspb.ComponentState_COMPONENT_STATE_DISABLED
	setReadiness(res, in, ConditionStatusFalse, reasonComponentDisabled, "Component is disabled")
	// Compared before syncObservedToCMS, which rewrites Ready on a source conflict.
	changed := healthConditionsChanged(in.ExistingConditions, res.Conditions, in.Generation)
	if err := r.syncObservedToCMS(ctx, in, canonicalName, res, ConditionStatusFalse, true); err != nil {
		markDeactivationFailed(in, res, ReasonDeactivationIncomplete, err.Error())
		return res, err
	}
	if !hasSourceConflict(res) {
		if changed {
			logging.Info(ctx, "Component disabled", "name", canonicalName, "generation", in.Generation)
		} else {
			logging.Debug(ctx, "Component disabled", "name", canonicalName, "generation", in.Generation)
		}
	}
	// A settled disabled component has no workload to probe; spec changes requeue it immediately,
	// so it only needs a pass per CMS heartbeat.
	res.RequeueAfter = defaultCMSHeartbeatInterval
	return res, nil
}

func (r *Reconciler) teardownAndCompleteDeactivation(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult, prepared bool) error {
	if in.Spec.GetType() == componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT {
		if err := r.deleteDisabledHelmComponent(ctx, in, res); err != nil {
			return err
		}
	}
	if !prepared {
		return nil
	}
	if _, err := callWithOwnership(ctx, r, in, func(ctx context.Context, src *componentspb.RequestSource) (*componentspb.CompleteDeactivationResponse, error) {
		return r.cmsClient.CompleteDeactivation(ctx, &componentspb.CompleteDeactivationRequest{Name: canonicalName, Source: src})
	}); err != nil {
		if handleOwnershipError(res, in, err) {
			return nil
		}
		err = fmt.Errorf("failed to complete deactivation in CMS: %w", err)
		markDeactivationFailed(in, res, ReasonDeactivationIncomplete, err.Error())
		setSynced(res, in, ConditionStatusFalse, ReasonCMSSyncFailed, err.Error())
		return err
	}
	return nil
}

// applyDisabledIntent records the disabled component's intent in CMS. Like registerIntent on the
// enabled path, a rejection because a dependency is not registered is tolerated: a disabled
// component needs no dependencies. Deactivation then proceeds against any existing CMS record, and
// a missing one (NotFound) is tolerated by prepareDeactivation and the final observed-state update.
//
// Intent is written on every pass, as on the enabled path; CMS treats an unchanged intent at the
// same generation as a no-op.
func (r *Reconciler) applyDisabledIntent(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult) error {
	err := r.applyIntent(ctx, in, canonicalName, res)
	notRegistered := dependencyNotRegisteredMessage(err)
	if notRegistered == "" {
		return err
	}
	logging.Info(ctx, "Deactivating component whose dependency is not registered in CMS", "name", canonicalName, "generation", in.Generation, "detail", notRegistered)
	return nil
}

// prepareDeactivation runs the CMS safety check unless already disabled at this generation. It returns
// true when CompleteDeactivation must follow; a CMS NotFound means there is nothing to guard.
func (r *Reconciler) prepareDeactivation(ctx context.Context, in ReconcileComponentInput, canonicalName string) (bool, error) {
	if ready := findCondition(in.ExistingConditions, ConditionTypeReady); ready != nil && ready.Reason == reasonComponentDisabled && ready.ObservedGeneration == in.Generation {
		return false, nil
	}
	_, err := callWithOwnership(ctx, r, in, func(ctx context.Context, src *componentspb.RequestSource) (*componentspb.PrepareDeactivationResponse, error) {
		return r.cmsClient.PrepareDeactivation(ctx, &componentspb.PrepareDeactivationRequest{Name: canonicalName, Source: src})
	})
	if status.Code(err) == codes.NotFound {
		return false, nil
	}
	return err == nil, err
}

// reportDeactivationBlocked reports DeactivationBlocked=True with msg and schedules a recheck
// after deactivationRecheckInterval. Ready reports DeactivationIncomplete when no workload is
// running (an earlier pass removed it, or it was never actuated), so no local health probe runs.
func (r *Reconciler) reportDeactivationBlocked(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult, msg string) (*ReconcileResult, error) {
	res.RequeueAfter = deactivationRecheckInterval
	setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDeactivationBlocked, ConditionStatusTrue, in.Generation, ReasonDeactivationBlocked, msg)
	if wasTornDown(in) || !r.isActuated(in) {
		return r.reportGated(ctx, in, canonicalName, res, ReasonDeactivationIncomplete, "no workload is running; "+msg)
	}
	return r.reportBlockedWorkload(ctx, in, canonicalName, res, msg)
}

// reportBlockedWorkload reports a component whose deactivation is blocked by active dependents. Its
// workload keeps running and serving them, so Ready follows the workload's local health (normally
// True) while Stalled=True (DeactivationBlocked) surfaces the block.
func (r *Reconciler) reportBlockedWorkload(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult, msg string) (*ReconcileResult, error) {
	r.evaluateLocalHealth(ctx, in, canonicalName, res)
	readyStatus := ConditionStatusUnknown
	if ready := findCondition(res.Conditions, ConditionTypeReady); ready != nil {
		readyStatus = ready.Status
	}
	res.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
	deleteCondition(&res.Conditions, ConditionTypeReconciling)
	setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeStalled, ConditionStatusTrue, in.Generation, ReasonDeactivationBlocked, msg)
	return res, r.syncReadyToCMS(ctx, in, canonicalName, res, readyStatus)
}

// wasTornDown reports whether a previous pass removed the workload without completing deactivation.
func wasTornDown(in ReconcileComponentInput) bool {
	ready := findCondition(in.ExistingConditions, ConditionTypeReady)
	return ready != nil && ready.Reason == ReasonDeactivationIncomplete
}

// markDeactivationFailed records Ready=False, Reconciling=True with reason, or DeactivationIncomplete
// once an earlier pass removed the workload (the handshake is retried every pass).
func markDeactivationFailed(in ReconcileComponentInput, res *ReconcileResult, reason, msg string) {
	if wasTornDown(in) {
		reason = ReasonDeactivationIncomplete
	}
	res.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
	setReadiness(res, in, ConditionStatusFalse, reason, msg)
}

func (r *Reconciler) deleteDisabledHelmComponent(ctx context.Context, in ReconcileComponentInput, res *ReconcileResult) error {
	if r.helmManager == nil {
		logging.Warn(ctx, "HelmManager is nil for 1P component deletion", "name", in.Name, "namespace", in.Namespace)
		return nil
	}
	ref := resolveComponentRef(in)
	wasPreviouslyActive := wasComponentPreviouslyActive(in.ExistingConditions)
	if err := r.helmManager.HydrateComponentState(ctx, ref, 0, wasPreviouslyActive); err != nil {
		logging.Warn(ctx, "Failed to hydrate Helm state from cluster; continuing with CR status fallback", "name", in.Name, "namespace", in.Namespace, "err", err)
	}
	// TODO: Skipping the uninstall here relies on Delete1PComponent discarding queued async upgrades; until
	// #1072 lands, an upgrade enqueued before the uninstall can reinstall the release while activeState stays false
	// (pre-existing race in K8sHelmManager). Drop this TODO once #1072 is merged.
	if !r.helmManager.IsComponentActive(ref) && !wasPreviouslyActive {
		return nil
	}
	if err := r.helmManager.Delete1PComponent(ctx, ref); err != nil {
		res.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
		setReadiness(res, in, ConditionStatusFalse, "HelmDeletionFailed", err.Error())
		return fmt.Errorf("failed to delete 1P Helm component: %w", err)
	}
	return nil
}

// wasComponentPreviouslyActive infers from the last Ready condition whether a workload may exist;
// gated (DependenciesNotReady, DependencyCycle), InitialSyncPending, or disabled components never had
// one actuated, and DeactivationIncomplete means an earlier pass already removed it. SourceConflict is
// not in this list: the fence or an ownership conflict can engage after actuation, so a workload may exist.
func wasComponentPreviouslyActive(existing []Condition) bool {
	ready := findCondition(existing, ConditionTypeReady)
	if ready == nil {
		return false
	}
	switch ready.Reason {
	case "", reasonComponentDisabled, ReasonDependenciesNotReady, ReasonDependencyCycle, ReasonDeactivationIncomplete, ReasonInitialSyncPending:
		return ready.Status == ConditionStatusTrue
	}
	return true
}
