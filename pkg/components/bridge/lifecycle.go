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
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/internal/logging"
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
	// ReasonDeactivationIncomplete marks a disabled component whose workload was removed before the CMS handshake completed.
	ReasonDeactivationIncomplete = "DeactivationIncomplete"
	reasonComponentDisabled      = "ComponentDisabled"
	// deactivationRecheckInterval throttles PrepareDeactivation while deactivation is blocked.
	deactivationRecheckInterval = 30 * time.Second
)

// reconcileDeletion tears a deleted Component down: CMS ExecuteDeletion first (a NotFound means
// CMS already deleted it), then the 1P workload per deletionPolicy, then the finalizer is
// released. spec.deletionProtection is not consulted: a deletion that passed admission is final.
func (r *Reconciler) reconcileDeletion(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult) (*ReconcileResult, error) {
	if _, err := r.cmsClient.ExecuteDeletion(ctx, &componentspb.ExecuteDeletionRequest{Name: canonicalName, DeletionPolicy: in.Spec.GetDeletionPolicy()}); err != nil {
		if status.Code(err) != codes.NotFound {
			setReadiness(res, in, ConditionStatusFalse, ConditionStatusTrue, "CMSDeletionFailed", err.Error())
			return res, fmt.Errorf("failed to execute deletion in CMS: %w", err)
		}
		logging.Info(ctx, "Component already deleted in CMS, proceeding with workload teardown", "name", canonicalName)
	} else {
		logging.Info(ctx, "Executed component deletion in CMS", "name", canonicalName, "policy", in.Spec.GetDeletionPolicy().String())
	}
	if err := r.teardown1PComponent(ctx, in, res); err != nil {
		return res, err
	}
	res.RemoveFinalizer = true
	res.State = componentspb.ComponentState_COMPONENT_STATE_DELETED
	return res, nil
}

func (r *Reconciler) teardown1PComponent(ctx context.Context, in ReconcileComponentInput, res *ReconcileResult) error {
	if in.Spec.GetType() != componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT || r.helmManager == nil {
		return nil
	}
	ref := resolveComponentRef(in)
	if in.Spec.GetDeletionPolicy() == componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN {
		if err := r.helmManager.Orphan1PComponent(ctx, ref); err != nil {
			setReadiness(res, in, ConditionStatusFalse, ConditionStatusTrue, "HelmOrphanFailed", err.Error())
			return fmt.Errorf("failed to orphan 1P Helm component: %w", err)
		}
		return nil
	}
	if err := r.helmManager.Purge1PComponent(ctx, ref); err != nil {
		setReadiness(res, in, ConditionStatusFalse, ConditionStatusTrue, "HelmDeletionFailed", err.Error())
		return fmt.Errorf("failed to delete 1P Helm component: %w", err)
	}
	return nil
}

// reconcileDisabled deactivates a component (enabled: false). PrepareDeactivation blocks while
// active dependents exist (the workload keeps running; re-checked every deactivationRecheckInterval).
// Otherwise the 1P workload is deleted, CompleteDeactivation finalizes the handshake, and the
// component reports COMPONENT_STATE_DISABLED. Failures after teardown report DeactivationIncomplete.
func (r *Reconciler) reconcileDisabled(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult) (*ReconcileResult, error) {
	if msg, ok := recentDeactivationBlock(in); ok {
		return r.reportDeactivationBlocked(ctx, in, canonicalName, res, msg, false)
	}
	prepared, err := r.prepareDeactivation(ctx, in, canonicalName)
	if status.Code(err) == codes.FailedPrecondition {
		msg := fmt.Sprintf("Cannot disable %q: %s", in.Name, status.Convert(err).Message())
		logging.Info(ctx, "Component deactivation blocked by active dependents", "name", canonicalName, "generation", in.Generation, "detail", msg)
		return r.reportDeactivationBlocked(ctx, in, canonicalName, res, msg, true)
	}
	if err != nil {
		err = fmt.Errorf("failed to prepare deactivation in CMS: %w", err)
		markDeactivationFailed(in, res, "CMSDeactivationFailed", err.Error())
		return res, err
	}
	if in.Spec.GetType() == componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT {
		if err := r.deleteDisabledHelmComponent(ctx, in, res); err != nil {
			return res, err
		}
	}
	if prepared {
		if _, err := r.cmsClient.CompleteDeactivation(ctx, &componentspb.CompleteDeactivationRequest{Name: canonicalName}); err != nil {
			err = fmt.Errorf("failed to complete deactivation in CMS: %w", err)
			markDeactivationFailed(in, res, ReasonDeactivationIncomplete, err.Error())
			return res, err
		}
	}
	res.State = componentspb.ComponentState_COMPONENT_STATE_DISABLED
	setReadiness(res, in, ConditionStatusFalse, ConditionStatusFalse, reasonComponentDisabled, "Component is disabled")
	if err := r.updateObservedCMS(ctx, canonicalName, in.Generation, res.State, "Ready=False"); err != nil && status.Code(err) != codes.NotFound {
		return res, fmt.Errorf("failed to update observed generation in CMS: %w", err)
	}
	logging.Info(ctx, "Component disabled", "name", canonicalName, "generation", in.Generation)
	return res, nil
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
	_, err := r.cmsClient.PrepareDeactivation(ctx, &componentspb.PrepareDeactivationRequest{Name: canonicalName})
	if status.Code(err) == codes.NotFound {
		return false, nil
	}
	return err == nil, err
}

// recentDeactivationBlock returns the DeactivationBlocked message if CMS rejected deactivation at
// this generation less than deactivationRecheckInterval ago (its LastTransitionTime is the last check).
func recentDeactivationBlock(in ReconcileComponentInput) (string, bool) {
	c := findCondition(in.ExistingConditions, ConditionTypeDeactivationBlocked)
	if c == nil || c.Status != ConditionStatusTrue || c.ObservedGeneration != in.Generation || time.Since(c.LastTransitionTime) >= deactivationRecheckInterval {
		return "", false
	}
	return c.Message, true
}

// reportDeactivationBlocked reports DeactivationBlocked=True with msg; checked marks a fresh CMS
// check, restarting the recheck interval. Ready keeps DeactivationIncomplete if the workload is gone.
func (r *Reconciler) reportDeactivationBlocked(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult, msg string, checked bool) (*ReconcileResult, error) {
	setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDeactivationBlocked, ConditionStatusTrue, in.Generation, ReasonDeactivationBlocked, msg)
	if cond := findCondition(res.Conditions, ConditionTypeDeactivationBlocked); checked && cond != nil {
		cond.LastTransitionTime = time.Now().UTC()
	}
	if wasTornDown(in) {
		return r.reportGated(ctx, in, canonicalName, res, ReasonDeactivationIncomplete, "workload already removed; "+msg, ConditionStatusTrue)
	}
	return r.reportGated(ctx, in, canonicalName, res, ReasonDeactivationBlocked, msg, ConditionStatusFalse)
}

// wasTornDown reports whether a previous pass removed the workload without completing deactivation.
func wasTornDown(in ReconcileComponentInput) bool {
	ready := findCondition(in.ExistingConditions, ConditionTypeReady)
	return ready != nil && ready.Reason == ReasonDeactivationIncomplete
}

// markDeactivationFailed records Ready=False, Degraded=True with reason, or DeactivationIncomplete
// once an earlier pass removed the workload (the handshake is retried every pass).
func markDeactivationFailed(in ReconcileComponentInput, res *ReconcileResult, reason, msg string) {
	if wasTornDown(in) {
		reason = ReasonDeactivationIncomplete
	}
	res.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
	setReadiness(res, in, ConditionStatusFalse, ConditionStatusTrue, reason, msg)
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
	// TODO(vluk): Skipping the uninstall here relies on Delete1PComponent discarding queued async upgrades; until
	// #1072 lands, an upgrade enqueued before the uninstall can reinstall the release while activeState stays false
	// (pre-existing race in K8sHelmManager). Drop this TODO once #1072 is merged.
	if !r.helmManager.IsComponentActive(ref) && !wasPreviouslyActive {
		return nil
	}
	if err := r.helmManager.Delete1PComponent(ctx, ref); err != nil {
		res.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
		setReadiness(res, in, ConditionStatusFalse, ConditionStatusTrue, "HelmDeletionFailed", err.Error())
		return fmt.Errorf("failed to delete 1P Helm component: %w", err)
	}
	return nil
}

// wasComponentPreviouslyActive infers from the last Ready condition whether a workload may exist;
// gated (DependenciesNotReady, DependencyCycle) or disabled components never had one actuated, and
// DeactivationIncomplete means an earlier pass already removed it. Transient error reasons such as
// IntentApplicationFailed or CMSDeactivationFailed overwrite the previous reason, so they are
// treated as active: an idempotent extra uninstall is cheaper than leaking a running workload.
func wasComponentPreviouslyActive(existing []Condition) bool {
	ready := findCondition(existing, ConditionTypeReady)
	if ready == nil {
		return false
	}
	switch ready.Reason {
	case "", reasonComponentDisabled, ReasonDependenciesNotReady, ReasonDependencyCycle, ReasonDeactivationIncomplete:
		return ready.Status == ConditionStatusTrue
	}
	return true
}
