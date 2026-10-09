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

// Package bridge implements the GitOps Bridge Controller reconciliation loop.
package bridge

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/pkg/components/naming"
)

const (
	// ReasonDependenciesNotReady marks an enabled component waiting on unready spec.dependencies.
	ReasonDependenciesNotReady = "DependenciesNotReady"
	// ReasonDependencyCycle marks an enabled component that is part of a spec.dependencies cycle.
	ReasonDependencyCycle = "DependencyCycle"
	// ReasonStaleIntent marks a component whose intent CMS rejected as stale: it comes from an
	// older custom resource generation or an older incarnation of the custom resource. It also
	// marks a custom resource whose observed-state report or deactivation step CMS rejected because
	// another custom resource owns the record (see releaseCMSOwnership).
	ReasonStaleIntent = "StaleIntent"
	// reasonIntentApplicationFailed marks any other rejected or failed intent write.
	reasonIntentApplicationFailed = "IntentApplicationFailed"
	// dependencyNotFoundMessagePrefix mirrors store.DependencyNotFoundMessagePrefix (pinned by a test).
	dependencyNotFoundMessagePrefix = "dependency not found:"
	// staleIntentMessagePrefix mirrors store.StaleIntentMessagePrefix (pinned by a test).
	staleIntentMessagePrefix = "stale intent:"
	// ReasonStaleDeletion is the reason of the Warning Event emitted when a deleted custom resource
	// is released without deleting its component in CMS, because another custom resource owns it.
	ReasonStaleDeletion = "StaleDeletion"
	// staleDeletionMessagePrefix mirrors store.StaleDeletionMessagePrefix (pinned by a test).
	staleDeletionMessagePrefix = "stale deletion:"
	// staleObservationMessagePrefix mirrors store.StaleObservationMessagePrefix (pinned by a test).
	staleObservationMessagePrefix = "stale observation:"
	// staleDeactivationMessagePrefix mirrors store.StaleDeactivationMessagePrefix (pinned by a test).
	staleDeactivationMessagePrefix = "stale deactivation:"
)

// AccessStatus encapsulates network and access metadata (such as environment variables) for a component.
type AccessStatus struct {
	// EnvVars maps configuration and coordinate keys to their resolved values.
	EnvVars map[string]string
}

// ReconcileResult encapsulates the outcome of a single component reconciliation pass.
type ReconcileResult struct {
	// ObservedGeneration is the custom resource metadata.generation evaluated.
	ObservedGeneration int64
	// ComponentGeneration is the CMS component generation committed by this pass's intent, or the
	// one carried over from ReconcileComponentInput.ComponentGeneration when no intent was
	// committed. CMS orders intents by the custom resource epoch, so it is independent of
	// ObservedGeneration. 0 means unknown.
	ComponentGeneration int64
	// ComponentID is the CMS ComponentId of the record, in decimal, as returned by this pass's
	// intent or carried over from ReconcileComponentInput.ComponentID when no intent was
	// committed. It stays the same when a recreated custom resource adopts the record. "" means
	// unknown.
	ComponentID string
	// State is the resulting lifecycle state in CMS.
	State componentspb.ComponentState
	// Conditions is the updated list of Kubernetes status conditions.
	Conditions []Condition
	// Access contains resolved network access coordinates and environment variables.
	Access *AccessStatus
	// AddFinalizer indicates whether the deletion protection finalizer should be attached.
	AddFinalizer bool
	// RemoveFinalizer indicates whether the deletion protection finalizer should be detached.
	RemoveFinalizer bool
	// WarningEvent, when set, is emitted as a Warning Event on the custom resource. Events outlive
	// the custom resource, so they report outcomes of a pass that also releases the finalizer.
	WarningEvent *Event
}

// Event is a Kubernetes Event to emit on the reconciled custom resource.
type Event struct {
	// Reason is the machine-readable UpperCamelCase reason, e.g. ReasonStaleDeletion.
	Reason string
	// Message is the human-readable description.
	Message string
}

// Reconciler executes reconciliation cycles for XMC Component custom resources.
type Reconciler struct {
	cmsClient   CMSClient
	prober      StatusDetectorHealthProber
	helmManager HelmManager
}

// NewReconciler constructs a new Reconciler instance.
// cms is the required gRPC client for CMS communication.
// prober is an optional health prober for component health checks.
// helmMgr is an optional HelmManager for provisioning 1P component workloads via Helm releases.
func NewReconciler(cms CMSClient, prober StatusDetectorHealthProber, helmMgr HelmManager) (*Reconciler, error) {
	if cms == nil {
		return nil, errors.New("cms client must not be nil")
	}
	return &Reconciler{cmsClient: cms, prober: prober, helmManager: helmMgr}, nil
}

// ReconcileComponentInput bundles parameters for a component reconciliation pass.
type ReconcileComponentInput struct {
	// Namespace is the Kubernetes namespace of the Component custom resource.
	Namespace string
	// Name is the custom resource name.
	Name string
	// Generation is the metadata.generation of the custom resource.
	Generation int64
	// UID and CreationTimestamp identify the custom resource object. Intents are sent with them as
	// their source, so CMS orders intents by object epoch and a recreated resource adopts the
	// existing component. An empty UID sends unsourced intents ordered by Generation.
	UID               string
	CreationTimestamp time.Time
	// ComponentGeneration is the CMS component generation last reported in
	// status.componentGeneration, or 0 if none was reported.
	ComponentGeneration int64
	// ComponentID is the CMS ComponentId last reported in status.componentId, or "" if none was
	// reported.
	ComponentID string
	// Spec is the parsed protobuf ComponentSpec.
	Spec *componentspb.ComponentSpec
	// IsDeleting indicates whether the custom resource has a non-nil deletionTimestamp.
	IsDeleting bool
	// HasFinalizer indicates whether the deletion protection finalizer is already present on the resource.
	HasFinalizer bool
	// DeletionProtection indicates whether the custom resource is protected against deletion.
	DeletionProtection bool
	// Enabled indicates whether the component should be active.
	Enabled bool
	// ExistingConditions contains conditions previously reported on the custom resource.
	ExistingConditions []Condition
	HelmOpts           HelmComponentOptions
	WrapperOpts        WrapperRef
	// Dependencies optionally resolves readiness of Spec.Dependencies. When nil, activation
	// is not gated on dependency readiness.
	Dependencies DependencyReadiness
}

// ReconcileComponent executes a single reconciliation pass for a Component.
// The flow executes the following steps:
// 1. Validates input name and spec.
// 2. If IsDeleting, delegates to reconcileDeletion (gated by DeletionProtection).
// 3. Attaches deletion protection finalizer if not already present.
// 4. If !Enabled, synchronizes intent via applyDisabledIntent (tolerating a dependency that is not
// registered in CMS) and handles deactivation via reconcileDisabled (gated by CMS deactivation safety).
// 5. Otherwise delegates to reconcileEnabled (dependency gating, intent, actuation, health).
func (r *Reconciler) ReconcileComponent(ctx context.Context, in ReconcileComponentInput) (*ReconcileResult, error) {
	if in.Name == "" || in.Spec == nil {
		return nil, errors.New("component name and spec must not be empty/nil")
	}
	logging.Debug(ctx, "Reconciling Component custom resource", "name", in.Name, "generation", in.Generation, "enabled", in.Enabled, "isDeleting", in.IsDeleting)
	// CMS orders an intent by spec.generation only when it carries no source (empty UID).
	in.Spec.Generation = in.Generation
	canonicalName := naming.CanonicalComponentName(in.Name)
	in.Spec.Dependencies = dropSelfDependency(ctx, in.Spec.GetDependencies(), canonicalName)
	res := &ReconcileResult{ObservedGeneration: in.Generation, ComponentGeneration: in.ComponentGeneration, ComponentID: in.ComponentID, State: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION}

	if in.IsDeleting {
		return r.reconcileDeletion(ctx, in, canonicalName, res)
	}

	// The finalizer is attached and logged by PersistReconcileResult, which performs the update.
	res.AddFinalizer = !in.HasFinalizer
	if !in.Enabled {
		if err := r.applyDisabledIntent(ctx, in, canonicalName, res); err != nil {
			return res, err
		}
		return r.reconcileDisabled(ctx, in, canonicalName, res)
	}
	return r.reconcileEnabled(ctx, in, canonicalName, res)
}

// reconcileEnabled rejects dependency cycles, registers intent (so CMS knows the dependency edges
// that guard deactivation even while this component waits), then gates actuation: a not-yet-actuated
// component (see isActuated) is held with DependenciesNotReady; an actuated one keeps its running
// workload with DependenciesReady=False, and a held Helm upgrade reports HelmUpgradePending.
func (r *Reconciler) reconcileEnabled(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult) (*ReconcileResult, error) {
	if in.Dependencies != nil {
		if cycle := in.Dependencies.DependencyCycle(ctx, canonicalName); cycle != "" {
			return r.reportGated(ctx, in, canonicalName, res, ReasonDependencyCycle, "dependency cycle detected: "+cycle, ConditionStatusTrue)
		}
	}
	waitMsg, err := r.registerIntent(ctx, in, canonicalName, res)
	if err != nil {
		preserveDependenciesReady(res, in.ExistingConditions)
		return res, err
	}
	if waitMsg != "" && !r.isActuated(in) {
		return r.reportGated(ctx, in, canonicalName, res, ReasonDependenciesNotReady, waitMsg, ConditionStatusFalse)
	}
	res, err = r.actuateAndEvaluate(ctx, in, canonicalName, res, waitMsg != "")
	if err != nil {
		preserveDependenciesReady(res, in.ExistingConditions)
		return res, err
	}
	if len(in.Spec.GetDependencies()) > 0 && (in.Dependencies != nil || waitMsg != "") {
		setDependenciesReadyCondition(res, in, waitMsg)
	}
	return res, nil
}

// preserveDependenciesReady carries an existing DependenciesReady condition over an error pass. The
// status write replaces all conditions, and dropping it would make isActuated treat an actuated
// component (e.g. a 3P wrapper that is not Ready) as never actuated on the next pass.
func preserveDependenciesReady(res *ReconcileResult, existing []Condition) {
	prev := findCondition(existing, ConditionTypeDependenciesReady)
	if res == nil || prev == nil || findCondition(res.Conditions, ConditionTypeDependenciesReady) != nil {
		return
	}
	res.Conditions = append(res.Conditions, *prev)
}

// registerIntent applies intent to CMS regardless of dependency readiness and returns a "waiting on
// dependencies" message when actuation must wait. A CMS rejection because a dependency is not
// registered yet is reported as waiting, not as an error; the intent is retried next pass.
func (r *Reconciler) registerIntent(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult) (string, error) {
	waitMsg := unreadyDependenciesMessage(ctx, in)
	err := r.applyIntent(ctx, in, canonicalName, res)
	notRegistered := storeRejectionMessage(err, dependencyNotFoundMessagePrefix)
	if notRegistered == "" {
		return waitMsg, err
	}
	if waitMsg == "" {
		waitMsg = "waiting on dependencies: " + notRegistered
	}
	return waitMsg, nil
}

// storeRejectionMessage returns the CMS status message if err (possibly wrapped) is a store
// FailedPrecondition whose message starts with prefix, or "" otherwise.
func storeRejectionMessage(err error, prefix string) string {
	// Not status.FromError: for wrapped errors it replaces the message with the full err.Error(),
	// which would defeat the prefix match below.
	var se interface{ GRPCStatus() *status.Status }
	if !errors.As(err, &se) {
		return ""
	}
	st := se.GRPCStatus()
	if st.Code() != codes.FailedPrecondition || !strings.HasPrefix(st.Message(), prefix) {
		return ""
	}
	return st.Message()
}

// actuateAndEvaluate reconciles the workload (3P observation or 1P Helm release, with Helm upgrades
// skipped when holdUpgrade is true) and evaluates health.
func (r *Reconciler) actuateAndEvaluate(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult, holdUpgrade bool) (*ReconcileResult, error) {
	if in.Spec.GetType() == componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER {
		return r.reconcile3PWrapper(ctx, in, canonicalName, res)
	}
	held, err := r.ensure1PComponent(ctx, in, res, holdUpgrade)
	if err != nil {
		return res, err
	}
	if held {
		msg := fmt.Sprintf("Helm upgrade to generation %d is held until dependencies are ready", in.Generation)
		return r.reportGated(ctx, in, canonicalName, res, ReasonHelmUpgradePending, msg, ConditionStatusFalse)
	}
	return r.evaluateHealth(ctx, in, canonicalName, res)
}

// resolveComponentRef returns the ComponentRef from HelmOpts with fallback to input Name and Namespace.
func resolveComponentRef(in ReconcileComponentInput) ComponentRef {
	ref := in.HelmOpts.Ref
	if ref.Name == "" {
		ref.Name = in.Name
	}
	if ref.Namespace == "" {
		ref.Namespace = in.Namespace
	}
	return ref
}

// applyIntent writes the intent to CMS, with the custom resource as its source, and records the
// committed CMS generation and the ComponentId in res.ComponentGeneration and res.ComponentID. On
// failure it reports Ready=False, Degraded=True with reason StaleIntent when CMS rejected the
// intent as coming from an older generation or an older object than its record (see the
// components README), or IntentApplicationFailed otherwise, and returns the error so the pass is
// retried with backoff. A failed write records nothing in CMS.
func (r *Reconciler) applyIntent(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult) error {
	setResp, err := r.cmsClient.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{Name: canonicalName, Spec: in.Spec, Source: intentSource(in)})
	if err != nil {
		res.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
		reason, msg := reasonIntentApplicationFailed, err.Error()
		if stale := storeRejectionMessage(err, staleIntentMessagePrefix); stale != "" {
			reason, msg = ReasonStaleIntent, stale
		}
		setReadiness(res, in, ConditionStatusFalse, ConditionStatusTrue, reason, msg)
		return fmt.Errorf("failed to apply component intent to CMS: %w", err)
	}
	if accessStatus := setResp.GetComponent().GetStatus(); accessStatus != nil {
		res.State = accessStatus.GetState()
	}
	if gen := setResp.GetComponent().GetSpec().GetGeneration(); gen > 0 {
		res.ComponentGeneration = gen
	}
	if id := setResp.GetComponent().GetComponentId(); id > 0 {
		res.ComponentID = strconv.FormatInt(id, 10)
	}
	logIntentApplied(ctx, in, canonicalName, res)
	return nil
}

// logIntentApplied logs an applied intent at Info when it committed a CMS generation or ComponentId
// other than the one in status.componentGeneration or status.componentId, or when the custom
// resource reports no generation yet (a new or recreated custom resource adopting a record), and at
// Debug for the idempotent re-apply that every pass performs.
func logIntentApplied(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult) {
	logAt := logging.Debug
	if in.ComponentGeneration == 0 || res.ComponentGeneration != in.ComponentGeneration || res.ComponentID != in.ComponentID {
		logAt = logging.Info
	}
	logAt(ctx, "Applied component intent to CMS", "name", canonicalName, "generation", in.Generation,
		"previousComponentGeneration", in.ComponentGeneration, "componentGeneration", res.ComponentGeneration,
		"previousComponentId", in.ComponentID, "componentId", res.ComponentID, "state", res.State.String())
}

// intentSource returns the custom resource identity sent with intents, or nil when the input has
// no UID. A UID with a zero CreationTimestamp yields a zero create_time, which CMS rejects as
// InvalidArgument; objects read from the API server always carry both.
func intentSource(in ReconcileComponentInput) *componentspb.IntentSource {
	if in.UID == "" {
		return nil
	}
	return &componentspb.IntentSource{Uid: in.UID, CreateTime: timestamppb.New(in.CreationTimestamp), Generation: in.Generation}
}

// unreadyDependenciesMessage returns "waiting on dependencies: ..." naming every unready dependency,
// or "" when activation may proceed (disabled, no dependencies, or no resolver configured).
func unreadyDependenciesMessage(ctx context.Context, in ReconcileComponentInput) string {
	if !in.Enabled || in.Dependencies == nil {
		return ""
	}
	var waiting []string
	for _, dep := range in.Spec.GetDependencies() {
		if ready, reason := in.Dependencies.IsDependencyReady(ctx, dep); !ready {
			waiting = append(waiting, fmt.Sprintf("%s (%s)", dep, reason))
		}
	}
	if len(waiting) == 0 {
		return ""
	}
	return "waiting on dependencies: " + strings.Join(waiting, ", ")
}

// isActuated reports whether an enabled component already passed dependency gating: its 1P release
// is active, it reports DependenciesReady (set only after passing the gate), or its Ready is True.
func (r *Reconciler) isActuated(in ReconcileComponentInput) bool {
	if r.helmManager != nil && in.Spec.GetType() == componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT && r.helmManager.IsComponentActive(resolveComponentRef(in)) {
		return true
	}
	if findCondition(in.ExistingConditions, ConditionTypeDependenciesReady) != nil {
		return true
	}
	ready := findCondition(in.ExistingConditions, ConditionTypeReady)
	return ready != nil && ready.Status == ConditionStatusTrue
}

// setDependenciesReadyCondition records the informational DependenciesReady condition for an actuated
// component; waitMsg is empty when every dependency is ready.
func setDependenciesReadyCondition(res *ReconcileResult, in ReconcileComponentInput, waitMsg string) {
	if waitMsg == "" {
		setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDependenciesReady, ConditionStatusTrue, in.Generation, "AllDependenciesReady", "All dependencies are ready")
		return
	}
	held := "; running workload is left in place"
	if in.Spec.GetType() == componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT {
		held = "; Helm upgrades are held, running workload is left in place"
	}
	setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDependenciesReady, ConditionStatusFalse, in.Generation, ReasonDependenciesNotReady, waitMsg+held)
}

// reportGated holds the component in PENDING_RECONCILIATION with Ready=False without touching workloads.
// CMS gets the coarse "Ready=False" message like other unready paths (it is only re-synced on Ready or
// generation changes, so details would go stale). NotFound is tolerated: it may not be registered yet.
func (r *Reconciler) reportGated(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult, reason, msg, degraded string) (*ReconcileResult, error) {
	res.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
	setReadiness(res, in, ConditionStatusFalse, degraded, reason, msg)
	if !shouldSyncToCMS(in.ExistingConditions, ConditionStatusFalse, in.Generation) && !componentGenerationChanged(in, res) {
		return res, nil
	}
	released, err := r.updateObservedCMS(ctx, in, canonicalName, res, "Ready=False")
	if err != nil && status.Code(err) != codes.NotFound {
		return res, fmt.Errorf("failed to update observed generation in CMS: %w", err)
	}
	resetReadyTransitionTime(res.Conditions)
	if released {
		return res, nil
	}
	logging.Info(ctx, "Component reconciliation gated", "name", canonicalName, "generation", in.Generation, "reason", reason, "detail", msg)
	return res, nil
}

// ensure1PComponent sets component enablement and parameter values in the Helm release. When
// holdUpgrade is true (dependencies unready), a pending install/upgrade is skipped and held is true;
// a repair of the already-actuated generation is not held, since it re-applies an approved spec.
func (r *Reconciler) ensure1PComponent(ctx context.Context, in ReconcileComponentInput, res *ReconcileResult, holdUpgrade bool) (held bool, err error) {
	if in.Spec.GetType() != componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT {
		return false, nil
	}
	if r.helmManager == nil {
		logging.Warn(ctx, "HelmManager is nil; skipping 1P Helm component reconciliation", "name", in.Name, "namespace", in.Namespace)
		return false, nil
	}
	ref := resolveComponentRef(in)
	readyAtGen := isReadyAtGeneration(in.ExistingConditions, in.Generation)
	if err := r.helmManager.HydrateComponentState(ctx, ref, in.Generation, readyAtGen); err != nil {
		logging.Warn(ctx, "Failed to hydrate Helm state from cluster; continuing with CR status fallback", "name", in.Name, "namespace", in.Namespace, "err", err)
	}
	repair := r.helmManager.NeedsRepair(ref, in.Generation)
	if !repair && r.helmManager.IsComponentActuated(ref, in.Generation) {
		logging.Debug(ctx, "1P component already actuated at current generation, skipping Helm upgrade", "name", in.Name, "generation", in.Generation)
		return false, nil
	}
	if holdUpgrade && !repair {
		logging.Debug(ctx, "Holding 1P Helm upgrade until dependencies are ready", "name", in.Name, "generation", in.Generation)
		return true, nil
	}
	in.HelmOpts.Ref = ref
	in.HelmOpts.Enabled = in.Enabled
	if err := r.helmManager.Ensure1PComponent(ctx, ref, in.HelmOpts); err != nil {
		res.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
		setReadiness(res, in, ConditionStatusFalse, ConditionStatusTrue, ReasonHelmUpgradeFailed, err.Error())
		return false, fmt.Errorf("failed to ensure 1P Helm component: %w", err)
	}
	r.helmManager.RecordActuation(ref, in.Generation)
	logging.Debug(ctx, "Ensured 1P Helm component", "name", in.Name, "namespace", in.Namespace, "generation", in.Generation)
	return false, nil
}

func isReadyAtGeneration(existing []Condition, generation int64) bool {
	if generation <= 0 {
		return false
	}
	readyAtGen := false
	for _, c := range existing {
		if c.Type == ConditionTypeDegraded && c.Status == ConditionStatusTrue && c.ObservedGeneration == generation {
			return false
		}
		if c.Type == ConditionTypeReady && c.Status == ConditionStatusTrue && c.ObservedGeneration == generation {
			readyAtGen = true
		}
	}
	return readyAtGen
}

// evaluateHealth probes the runtime health of an active component (in.Enabled == true), sets the Ready and Degraded
// conditions, updates state to COMPONENT_STATE_ENABLED (or PENDING_RECONCILIATION), and reports to CMS.
// Note: Disabled components (in.Enabled == false) are processed exclusively by reconcileDisabled and do not reach evaluateHealth.
func (r *Reconciler) evaluateHealth(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult) (*ReconcileResult, error) {
	isHealthy, reason, healthMsg := r.probeComponentHealth(ctx, in)
	res.Access = &AccessStatus{EnvVars: make(map[string]string)}
	if reason == ReasonHelmStatusUnavailable && preserveHealthConditions(in, res) {
		logging.Warn(ctx, "Transient component health check failure; keeping previous Ready/Degraded conditions", "name", canonicalName, "generation", in.Generation, "detail", healthMsg)
		return res, nil
	}

	readyStatus, degradedStatus := healthConditionStatuses(isHealthy, reason)
	res.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
	if isHealthy {
		res.State = componentspb.ComponentState_COMPONENT_STATE_ENABLED
	}
	setReadiness(res, in, readyStatus, degradedStatus, reason, healthMsg)
	if err := r.syncReadyToCMS(ctx, in, canonicalName, res, readyStatus); err != nil {
		return res, err
	}
	logHealthEvaluated(ctx, in, canonicalName, res)
	return res, nil
}

// logHealthEvaluated logs a health evaluation at Info when it changed the Ready or Degraded
// condition recorded in the custom resource (see healthConditionsChanged), and at Debug for the
// unchanged result that every periodic pass over a steady component produces. It logs the final
// Ready condition, which differs from the probe result when reporting to CMS released ownership.
func logHealthEvaluated(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult) {
	logAt := logging.Debug
	if healthConditionsChanged(in.ExistingConditions, res.Conditions, in.Generation) {
		logAt = logging.Info
	}
	var healthy bool
	var reason string
	if ready := findCondition(res.Conditions, ConditionTypeReady); ready != nil {
		healthy, reason = ready.Status == ConditionStatusTrue, ready.Reason
	}
	logAt(ctx, "Evaluated component health", "name", canonicalName, "generation", in.Generation, "state", res.State.String(), "healthy", healthy, "reason", reason)
}

// healthConditionsChanged reports whether the Ready or Degraded condition in current differs from
// existing in presence, status, or reason, or was last observed in existing at a generation other
// than generation (a first evaluation, or the first one of a new custom resource generation).
func healthConditionsChanged(existing, current []Condition, generation int64) bool {
	for _, condType := range []string{ConditionTypeReady, ConditionTypeDegraded} {
		prev, next := findCondition(existing, condType), findCondition(current, condType)
		if (prev == nil) != (next == nil) {
			return true
		}
		if prev == nil {
			continue
		}
		if prev.Status != next.Status || prev.Reason != next.Reason || prev.ObservedGeneration != generation {
			return true
		}
	}
	return false
}

// preserveHealthConditions copies the existing Ready/Degraded conditions into res and derives the state from them.
// It returns false (leaving res untouched) when there is no Ready condition observed at the current generation.
func preserveHealthConditions(in ReconcileComponentInput, res *ReconcileResult) bool {
	idx := slices.IndexFunc(in.ExistingConditions, func(c Condition) bool { return c.Type == ConditionTypeReady })
	if idx < 0 || in.ExistingConditions[idx].ObservedGeneration != in.Generation {
		return false
	}
	res.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
	if in.ExistingConditions[idx].Status == ConditionStatusTrue {
		res.State = componentspb.ComponentState_COMPONENT_STATE_ENABLED
	}
	for _, c := range in.ExistingConditions {
		if c.Type == ConditionTypeReady || c.Type == ConditionTypeDegraded {
			upsertCondition(&res.Conditions, c)
		}
	}
	return true
}

// probeComponentHealth returns the health verdict, condition reason, and message for an enabled component.
// 1P components managed by a HelmManager report their real Helm release state; others fall back to the prober.
func (r *Reconciler) probeComponentHealth(ctx context.Context, in ReconcileComponentInput) (bool, string, string) {
	if in.Spec.GetType() == componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT && r.helmManager != nil {
		return r.helmManager.ComponentHealth(ctx, resolveComponentRef(in))
	}
	if r.prober == nil {
		return true, ReasonComponentHealthy, "Component is active"
	}
	healthy, msg := r.prober.ProbeHealth(ctx, in.Spec, in.Namespace)
	if !healthy {
		return false, ReasonHealthProbeFailed, msg
	}
	return true, ReasonComponentHealthy, msg
}

// Reconcile is the top-level reconciler entrypoint for an unstructured Kubernetes Component custom resource.
// It parses the spec, generation, and status conditions into ReconcileComponentInput and calls ReconcileComponent.
// Activation is not gated on dependency readiness; see ReconcileWithDependencies.
func (r *Reconciler) Reconcile(ctx context.Context, obj *unstructured.Unstructured) (*ReconcileResult, error) {
	return r.ReconcileWithDependencies(ctx, obj, nil)
}

// ReconcileWithDependencies is Reconcile with activation gated on deps (nil disables gating).
func (r *Reconciler) ReconcileWithDependencies(ctx context.Context, obj *unstructured.Unstructured, deps DependencyReadiness) (*ReconcileResult, error) {
	if obj == nil {
		return nil, errors.New("unstructured object must not be nil")
	}
	hasFinalizer := false
	for _, f := range obj.GetFinalizers() {
		if f == FinalizerDeletionProtection {
			hasFinalizer = true
			break
		}
	}
	spec, dp, enabled := ParseSpecFromUnstructured(obj.Object["spec"])
	opts := ParseHelmComponentOptions(obj.GetName(), obj.GetNamespace(), obj.Object["spec"], spec)
	wopts := ParseWrapperRef(obj.Object["spec"], obj.GetNamespace())
	return r.ReconcileComponent(ctx, ReconcileComponentInput{
		Namespace:           obj.GetNamespace(),
		Name:                obj.GetName(),
		Generation:          obj.GetGeneration(),
		UID:                 string(obj.GetUID()),
		CreationTimestamp:   obj.GetCreationTimestamp().Time,
		ComponentGeneration: ParseComponentGeneration(obj.Object["status"]),
		ComponentID:         ParseComponentID(obj.Object["status"]),
		Spec:                spec,
		IsDeleting:          obj.GetDeletionTimestamp() != nil,
		HasFinalizer:        hasFinalizer,
		DeletionProtection:  dp,
		Enabled:             enabled,
		ExistingConditions:  ParseConditionsFromUnstructured(obj.Object["status"]),
		HelmOpts:            opts,
		WrapperOpts:         wopts,
		Dependencies:        deps,
	})
}

// dropSelfDependency removes references to canonicalName from deps, logging a warning when found,
// because a component that depends on itself could never become ready. deps must already be
// canonical (parseDependencies is the bridge's single canonicalization point).
func dropSelfDependency(ctx context.Context, deps []string, canonicalName string) []string {
	var res []string
	for _, dep := range deps {
		if dep == canonicalName {
			logging.Warn(ctx, "Ignoring self-dependency in component spec", "name", canonicalName)
			continue
		}
		res = append(res, dep)
	}
	return res
}
