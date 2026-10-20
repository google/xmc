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

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/proto"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/pkg/components/naming"
	"xmanager_cloud/pkg/components/store"
)

const (
	// ReasonDependenciesNotReady marks an enabled component waiting on unready spec.dependencies.
	ReasonDependenciesNotReady = "DependenciesNotReady"
	// ReasonDependencyCycle marks an enabled component that is part of a spec.dependencies cycle.
	ReasonDependencyCycle = "DependencyCycle"
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
	// ComponentGeneration is the CMS component generation committed for this resource, or 0 if unknown.
	ComponentGeneration int64
	// ComponentID is the CMS ComponentId of the record, in decimal, or "" if unknown.
	ComponentID string
	// State is the resulting lifecycle state in CMS.
	State componentspb.ComponentState
	// Conditions is the updated list of Kubernetes status conditions.
	Conditions []Condition
	// Access contains resolved network access coordinates and environment variables.
	Access *AccessStatus
	// RemoveFinalizer indicates that cleanup finished and the CMS cleanup finalizer may be released.
	RemoveFinalizer bool
	// StaleIncarnation indicates that the evaluated custom resource snapshot is stale, either because
	// its UID was superseded by a newer custom resource at the same namespace/name or because CMS
	// already applied a newer generation of it (STALE_GENERATION), so status and finalizer
	// persistence must be skipped.
	StaleIncarnation bool
	// ConflictReason and ConflictMessage record a source or cluster ownership conflict encountered
	// during deletion when CMS deletion is skipped and only the local CR is cleaned up.
	ConflictReason  string
	ConflictMessage string
	// WarningEvent, when set, is recorded as a Kubernetes Warning Event on the Component before the
	// finalizer is released or status is applied, so it is never lost with the object.
	WarningEvent *WarningEvent
	// RequeueAfter optionally overrides ProbeInterval as the delay before the next pass. It is always
	// honored, whether shorter or longer than ProbeInterval.
	RequeueAfter time.Duration
}

var errStaleLocalIncarnation = errors.New("stale component incarnation superseded by a newer UID")

// errStaleSnapshot reports that CMS rejected an intent with STALE_GENERATION because it already
// applied a newer generation of the same custom resource than the reconciled informer snapshot.
var errStaleSnapshot = errors.New("stale component snapshot older than the generation CMS already applied")

// LiveUIDReader reads the current metadata.uid of a Component custom resource directly from the
// Kubernetes API server (bypassing informer caches). It returns ("", nil) when the resource does
// not exist.
type LiveUIDReader func(ctx context.Context, namespace, name string) (string, error)

// NewAPIReaderLiveUID returns a LiveUIDReader backed by an uncached controller-runtime client.Reader
// (such as mgr.GetAPIReader()).
func NewAPIReaderLiveUID(reader client.Reader) LiveUIDReader {
	if reader == nil {
		return nil
	}
	return func(ctx context.Context, namespace, name string) (string, error) {
		u := newComponentObject()
		err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, u)
		if apierrors.IsNotFound(err) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		return string(u.GetUID()), nil
	}
}

// Reconciler executes reconciliation cycles for XMC Component custom resources.
type Reconciler struct {
	cmsClient     CMSClient
	prober        StatusDetectorHealthProber
	helmManager   HelmManager
	fence         *ClusterFence
	liveUIDReader LiveUIDReader
	syncTracker   cmsSyncTracker
}

// ReconcilerOption configures optional Reconciler dependencies.
type ReconcilerOption func(*Reconciler)

// WithClusterFence configures the Reconciler to gate active reconciliations on fence.
func WithClusterFence(fence *ClusterFence) ReconcilerOption {
	return func(r *Reconciler) { r.fence = fence }
}

// WithLiveUIDReader configures the Reconciler with an uncached UID reader used to verify
// same-cluster custom resource recreation before taking over CMS ownership via expected_owner_uid.
func WithLiveUIDReader(reader LiveUIDReader) ReconcilerOption {
	return func(r *Reconciler) {
		r.liveUIDReader = reader
	}
}

// NewReconciler constructs a new Reconciler instance.
// cms is the required gRPC client for CMS communication.
// prober is an optional health prober for component health checks.
// helmMgr is an optional HelmManager for provisioning 1P component workloads via Helm releases.
// opts configure optional dependencies such as WithClusterFence.
func NewReconciler(cms CMSClient, prober StatusDetectorHealthProber, helmMgr HelmManager, opts ...ReconcilerOption) (*Reconciler, error) {
	if cms == nil {
		return nil, errors.New("cms client must not be nil")
	}
	r := &Reconciler{cmsClient: cms, prober: prober, helmManager: helmMgr}
	for _, opt := range opts {
		if opt != nil {
			opt(r)
		}
	}
	return r, nil
}

// ClusterFence returns the ClusterFence configured on r, or nil if none.
func (r *Reconciler) ClusterFence() *ClusterFence {
	if r == nil {
		return nil
	}
	return r.fence
}

// componentStateForgetter is implemented by HelmManagers that track per-incarnation release state in memory.
type componentStateForgetter interface {
	// ForgetComponent evicts the in-memory state of every incarnation of the namespace/name component.
	ForgetComponent(namespace, name string)
}

// ForgetComponent evicts any in-memory CMS sync, deactivation-block or Helm release state tracked for the component
// once its custom resource is gone.
func (r *Reconciler) ForgetComponent(namespace, name string) {
	if r == nil {
		return
	}
	r.syncTracker.forget(componentKey(namespace, naming.CanonicalComponentName(name)))
	if f, ok := r.helmManager.(componentStateForgetter); ok {
		f.ForgetComponent(namespace, name)
	}
}

// ReconcileComponentInput bundles parameters for a component reconciliation pass.
type ReconcileComponentInput struct {
	// Namespace is the Kubernetes namespace of the Component custom resource.
	Namespace string
	// Name is the custom resource name.
	Name string
	// Generation is the metadata.generation of the custom resource.
	Generation int64
	// UID is the metadata.uid of the custom resource. When non-empty alongside a cluster identity,
	// CMS requests carry RequestSource so CMS enforces single-writer ownership.
	UID string
	// Cluster optionally overrides the cluster identity sent in RequestSource (defaults to r.fence.Identity()).
	Cluster *componentspb.ClusterIdentity
	// ComponentGeneration is the status.componentGeneration previously reported on the custom resource.
	ComponentGeneration int64
	// ComponentID is the status.componentId (in decimal) previously reported on the custom resource.
	ComponentID string
	// Spec is the parsed protobuf ComponentSpec.
	Spec *componentspb.ComponentSpec
	// IsDeleting indicates whether the custom resource has a non-nil deletionTimestamp.
	IsDeleting bool
	// Enabled indicates whether the component should be active.
	Enabled bool
	// ExistingObservedGeneration is the status.observedGeneration previously reported on the custom resource.
	ExistingObservedGeneration int64
	// ExistingConditions contains conditions previously reported on the custom resource.
	ExistingConditions []Condition
	// ExistingAccess contains status.access previously reported on the custom resource.
	ExistingAccess *AccessStatus
	HelmOpts       HelmComponentOptions
	WrapperOpts    WrapperRef
	// Dependencies optionally resolves readiness of Spec.Dependencies. When nil, activation
	// is not gated on dependency readiness.
	Dependencies DependencyReadiness
	// skipCMSSync limits a pass to local health evaluation: syncReadyToCMS does not call CMS.
	skipCMSSync bool
}

// ReconcileComponent executes a single reconciliation pass for a Component.
// The flow executes the following steps:
// 1. Validates input name and spec.
// 2. If IsDeleting, delegates to reconcileDeletion.
// 3. If !Enabled, synchronizes intent via applyDisabledIntent (tolerating a dependency that is not
// registered in CMS) and handles deactivation via reconcileDisabled (gated by CMS deactivation safety).
// 4. Otherwise delegates to reconcileEnabled (dependency gating, intent, actuation, health).
//
// Callers must persist FinalizerCMSCleanup on the Component before the first pass, so that every
// CMS or workload side effect is cleaned up on deletion; ComponentController does.
func (r *Reconciler) ReconcileComponent(ctx context.Context, in ReconcileComponentInput) (*ReconcileResult, error) {
	if in.Name == "" || in.Spec == nil {
		return nil, errors.New("component name and spec must not be empty/nil")
	}
	logging.Debug(ctx, "Reconciling Component custom resource", "name", in.Name, "generation", in.Generation, "enabled", in.Enabled, "isDeleting", in.IsDeleting)
	in.Spec.Generation = in.Generation
	canonicalName := naming.CanonicalComponentName(in.Name)
	in.Spec.Dependencies = dropSelfDependency(ctx, in.Spec.GetDependencies(), canonicalName)
	res := &ReconcileResult{
		ObservedGeneration:  in.ExistingObservedGeneration,
		ComponentGeneration: in.ComponentGeneration,
		ComponentID:         in.ComponentID,
		State:               componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
	}
	res, err := r.reconcilePass(ctx, in, canonicalName, res)
	if generationEvaluated(res, in.Generation, err) && !res.StaleIncarnation {
		res.ObservedGeneration = in.Generation
	}
	return res, err
}

// generationEvaluated reports whether a pass evaluated generation, so that status.observedGeneration
// may be stamped as kstatus expects: the pass completed without error, or it ended Stalled at this
// generation with CMS synced (e.g. a CMS-reported dependency cycle or a broken wrapper contract).
// Transient errors leave no Stalled condition at this generation or report Synced=False, so
// status.observedGeneration keeps its previous value.
func generationEvaluated(res *ReconcileResult, generation int64, err error) bool {
	if res == nil {
		return false
	}
	if err == nil {
		return true
	}
	stalled := findCondition(res.Conditions, ConditionTypeStalled)
	synced := findCondition(res.Conditions, ConditionTypeSynced)
	return stalled != nil && stalled.Status == ConditionStatusTrue && stalled.ObservedGeneration == generation &&
		synced != nil && synced.Status == ConditionStatusTrue
}

func (r *Reconciler) reconcilePass(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult) (*ReconcileResult, error) {
	// Deletion runs before the fence check on purpose: cold standby holds only non-deleting CRs. A
	// fenced cluster's deletion skips CMS (the local fence check, or CLUSTER_FENCED on the sourced
	// ExecuteDeletion), so CMS keeps the active owner's record while reconcileDeletion reports
	// StaleDeletion, tears down only the local release and releases the finalizer.
	if in.IsDeleting {
		return r.reconcileDeletion(ctx, in, canonicalName, res)
	}
	if fenced, err := r.reconcileClusterFence(ctx, in, res); fenced || err != nil {
		return res, err
	}
	if !in.Enabled {
		if err := r.applyDisabledIntent(ctx, in, canonicalName, res); err != nil || shouldStopReconcile(res) {
			return res, err
		}
		return r.reconcileDisabled(ctx, in, canonicalName, res)
	}
	return r.reconcileEnabled(ctx, in, canonicalName, res)
}

func (r *Reconciler) reconcileClusterFence(ctx context.Context, in ReconcileComponentInput, res *ReconcileResult) (bool, error) {
	if r.fence == nil {
		return false, nil
	}
	reason, msg, err := r.fence.Check(ctx)
	if err != nil {
		recordCMSSyncFailure(res, in, err)
		return false, err
	}
	if reason == "" {
		return false, nil
	}
	recordSourceConflict(res, in, reason, msg)
	return true, nil
}

// reconcileEnabled rejects dependency cycles, registers intent (so CMS knows the dependency edges
// that guard deactivation even while this component waits), then gates actuation: a not-yet-actuated
// component (see isActuated) is held with DependenciesNotReady; an actuated one keeps its running
// workload with DependenciesReady=False, and a held Helm upgrade reports HelmUpgradePending.
func (r *Reconciler) reconcileEnabled(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult) (*ReconcileResult, error) {
	if in.Dependencies != nil {
		if cycle := in.Dependencies.DependencyCycle(ctx, canonicalName); cycle != "" {
			return r.reportGated(ctx, in, canonicalName, res, ReasonDependencyCycle, "dependency cycle detected: "+cycle)
		}
	}
	waitMsg, err := r.registerIntent(ctx, in, canonicalName, res)
	if err != nil {
		preserveDependenciesReady(res, in.ExistingConditions)
		return res, err
	}
	if shouldStopReconcile(res) {
		return res, nil
	}
	if waitMsg != "" && !r.isActuated(in) {
		return r.reportGated(ctx, in, canonicalName, res, ReasonDependenciesNotReady, waitMsg)
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
	notRegistered := dependencyNotRegisteredMessage(err)
	if notRegistered == "" {
		return waitMsg, err
	}
	if waitMsg == "" {
		waitMsg = "waiting on dependencies: " + notRegistered
	}
	return waitMsg, nil
}

// dependencyNotRegisteredMessage returns the CMS status message if err (possibly wrapped) is the
// store's FailedPrecondition carrying a google.rpc.ErrorInfo with reason DEPENDENCY_NOT_REGISTERED
// in the components domain, or "" otherwise. It matches the reason only, never the message.
func dependencyNotRegisteredMessage(err error) string {
	info, msg := extractErrorInfo(err)
	if info == nil || info.GetReason() != store.ReasonDependencyNotRegistered {
		return ""
	}
	return msg
}

// dependencyCycleMessage returns the CMS status message if err (possibly wrapped) is the store's
// DEPENDENCY_CYCLE rejection, or "" otherwise.
func dependencyCycleMessage(err error) string {
	info, msg := extractErrorInfo(err)
	if info == nil || info.GetReason() != store.ReasonDependencyCycle {
		return ""
	}
	return msg
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
		return r.reportGated(ctx, in, canonicalName, res, ReasonHelmUpgradePending, msg)
	}
	return r.evaluateHealth(ctx, in, canonicalName, res)
}

// resolveComponentRef returns the ComponentRef from HelmOpts with fallback to input Name, Namespace, and UID.
func resolveComponentRef(in ReconcileComponentInput) ComponentRef {
	ref := in.HelmOpts.Ref
	if ref.Name == "" {
		ref.Name = in.Name
	}
	if ref.Namespace == "" {
		ref.Namespace = in.Namespace
	}
	if ref.UID == "" {
		ref.UID = in.UID
	}
	return ref
}

func (r *Reconciler) applyIntent(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult) error {
	setResp, err := callWithOwnership(ctx, r, in, func(ctx context.Context, src *componentspb.RequestSource) (*componentspb.SetComponentIntentResponse, error) {
		return r.cmsClient.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{
			Name:   canonicalName,
			Spec:   in.Spec,
			Source: src,
		})
	})
	if err != nil {
		return r.handleIntentError(ctx, in, canonicalName, res, err)
	}
	setSynced(res, in, ConditionStatusTrue, ReasonCMSSynced, cmsSyncedMessage)
	applyIntentResponse(res, setResp)
	logIntentApplied(ctx, in, canonicalName, res)
	return nil
}

// logIntentApplied logs an applied intent at Info when it committed a CMS generation or ComponentId
// other than the one in status.componentGeneration or status.componentId, or when the custom
// resource reports no generation yet (a new or recreated custom resource adopting a record), and at
// Debug for the idempotent re-apply that every pass performs.
func logIntentApplied(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult) {
	if in.ComponentGeneration == 0 || res.ComponentGeneration != in.ComponentGeneration || res.ComponentID != in.ComponentID {
		logging.Info(ctx, "Applied component intent to CMS", "name", canonicalName, "generation", in.Generation,
			"previousComponentGeneration", in.ComponentGeneration, "componentGeneration", res.ComponentGeneration,
			"previousComponentId", in.ComponentID, "componentId", res.ComponentID, "state", res.State.String())
	} else {
		logging.Debug(ctx, "Applied component intent to CMS", "name", canonicalName, "generation", in.Generation,
			"previousComponentGeneration", in.ComponentGeneration, "componentGeneration", res.ComponentGeneration,
			"previousComponentId", in.ComponentID, "componentId", res.ComponentID, "state", res.State.String())
	}
}

// applyIntentResponse copies the CMS state, component generation and ComponentId from a
// SetComponentIntent response into res, leaving res unchanged when the response carries no
// component.
func applyIntentResponse(res *ReconcileResult, setResp *componentspb.SetComponentIntentResponse) {
	if setResp == nil || setResp.GetComponent() == nil {
		return
	}
	component := setResp.GetComponent()
	if accessStatus := component.GetStatus(); accessStatus != nil {
		res.State = accessStatus.GetState()
	}
	if spec := component.GetSpec(); spec != nil && spec.GetGeneration() > 0 {
		res.ComponentGeneration = spec.GetGeneration()
	}
	if id := component.GetComponentId(); id > 0 {
		res.ComponentID = strconv.FormatInt(id, 10)
	}
}

// handleIntentError classifies a SetComponentIntent failure: an ownership or fence rejection is
// recorded on the result and not returned, an unregistered dependency keeps Synced=True (CMS is
// reachable, and registerIntent intercepts the returned intentErr to convert it into a non-error
// dependency wait message so the pass finishes with err == nil via reportGated and advances
// status.observedGeneration), a dependency cycle gates the component, and any other error records
// a CMS sync failure and refreshes the local health of an actuated workload. Every error except an
// ownership rejection returns the wrapped intent error.
func (r *Reconciler) handleIntentError(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult, err error) error {
	if handleOwnershipError(res, in, err) {
		return nil
	}
	intentErr := fmt.Errorf("failed to apply component intent to CMS: %w", err)
	if dependencyNotRegisteredMessage(err) != "" {
		setSynced(res, in, ConditionStatusTrue, ReasonCMSSynced, cmsSyncedMessage)
		return intentErr
	}
	msg := dependencyCycleMessage(err)
	if msg != "" {
		if _, syncErr := r.reportGated(ctx, in, canonicalName, res, ReasonDependencyCycle, msg); syncErr != nil {
			return errors.Join(intentErr, syncErr)
		}
		return intentErr
	}
	recordCMSSyncFailure(res, in, err)
	r.refreshLocalHealth(ctx, in, canonicalName, res, err)
	return intentErr
}

// refreshLocalHealth keeps probing the workload of an already-actuated, enabled component while
// CMS is unreachable, so Ready reflects the latest local probe rather than the value from before
// the outage. Health is evaluated at the last evaluated generation, because the new spec has not
// reached CMS; the pass never calls CMS and never changes the workload. Synced stays False.
func (r *Reconciler) refreshLocalHealth(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult, cmsErr error) {
	prev := findCondition(in.ExistingConditions, ConditionTypeReady)
	if !in.Enabled || in.IsDeleting || prev == nil || !r.isActuated(in) {
		return
	}
	local := in
	local.Generation = prev.ObservedGeneration
	r.evaluateLocalHealth(ctx, local, canonicalName, res)
	setSynced(res, in, ConditionStatusFalse, ReasonCMSSyncFailed, cmsErr.Error())
}

// evaluateLocalHealth probes the running workload (3P wrapper status or 1P health) and records
// Ready and access on res without calling CMS or changing the workload. Local failures are recorded
// in Ready, so callers that report their own outcome ignore the probe's error.
func (r *Reconciler) evaluateLocalHealth(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult) {
	in.skipCMSSync = true
	if in.Spec.GetType() == componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER {
		_, _ = r.reconcile3PWrapper(ctx, in, canonicalName, res)
		return
	}
	_, _ = r.evaluateHealth(ctx, in, canonicalName, res)
}

// callWithOwnership invokes fn with r.requestSource(in), re-declaring the cluster epoch on
// CLUSTER_NOT_DECLARED and retrying with expected_owner_uid after an uncached GET proves a
// same-cluster custom resource recreation on SOURCE_INCARNATION_MISMATCH (including when the
// retry after re-declaring returns it). If the recorded owner was deleted before that retry, the
// call is retried once more without expected_owner_uid (see retrySameClusterTakeover).
// STALE_GENERATION, from the first call or any retry, becomes a stale-snapshot error (see
// classifyOwnershipError), never a source conflict.
func callWithOwnership[T any](
	ctx context.Context,
	r *Reconciler,
	in ReconcileComponentInput,
	fn func(context.Context, *componentspb.RequestSource) (T, error),
) (T, error) {
	src := r.requestSource(in)
	resp, err := fn(ctx, src)
	if err == nil || src == nil {
		return resp, err
	}
	r.fence.RecordConflict(err)
	info, _ := extractErrorInfo(err)
	if info != nil && info.GetReason() == store.ReasonClusterNotDeclared && r.fence != nil {
		if declErr := r.fence.Declare(ctx); declErr != nil {
			var zero T
			return zero, fmt.Errorf("failed to redeclare cluster epoch: %w", declErr)
		}
		src = r.requestSource(in)
		resp, err = fn(ctx, src)
		r.fence.RecordConflict(err)
		info, _ = extractErrorInfo(err)
	}
	if info != nil && info.GetReason() == store.ReasonSourceIncarnationMismatch {
		return retrySameClusterTakeover(ctx, r, in, src, info, err, fn)
	}
	return resp, classifyOwnershipError(ctx, r, in, err)
}

// classifyOwnershipError maps a sourced CMS error to its reconcile outcome: STALE_GENERATION becomes
// a stale-snapshot error (see staleSnapshotError); other errors, including nil, are returned as is.
// The fence rejections are recorded by RecordConflict at each call.
func classifyOwnershipError(ctx context.Context, r *Reconciler, in ReconcileComponentInput, err error) error {
	if info, _ := extractErrorInfo(err); info != nil && info.GetReason() == store.ReasonStaleGeneration {
		return staleSnapshotError(ctx, r, in)
	}
	return err
}

// staleSnapshotError handles STALE_GENERATION: CMS already applied a newer generation of this
// custom resource, so the reconciled informer snapshot is stale. It reads the live UID with an
// uncached GET and returns errStaleLocalIncarnation if the custom resource was replaced,
// errStaleSnapshot otherwise, or the read error. Both sentinels skip status writes and requeue
// after staleComponentRetryDelay so the cache can catch up.
func staleSnapshotError(ctx context.Context, r *Reconciler, in ReconcileComponentInput) error {
	if r.liveUIDReader == nil {
		return errStaleSnapshot
	}
	liveUID, err := r.liveUIDReader(ctx, in.Namespace, in.Name)
	if err != nil {
		return fmt.Errorf("failed to read live component UID after a stale generation: %w", err)
	}
	if liveUID != in.UID {
		return errStaleLocalIncarnation
	}
	return errStaleSnapshot
}

func retrySameClusterTakeover[T any](
	ctx context.Context,
	r *Reconciler,
	in ReconcileComponentInput,
	src *componentspb.RequestSource,
	info *errdetails.ErrorInfo,
	origErr error,
	fn func(context.Context, *componentspb.RequestSource) (T, error),
) (T, error) {
	var zero T
	meta := info.GetMetadata()
	ownerUID := meta[store.MetadataKeyOwnerUID]
	clusterUID := src.GetCluster().GetUid()
	if r.liveUIDReader == nil || ownerUID == "" || clusterUID == "" || meta[store.MetadataKeyOwnerClusterUID] != clusterUID || meta[store.MetadataKeyOwnerNamespace] != in.Namespace {
		return zero, origErr
	}
	liveUID, err := r.liveUIDReader(ctx, in.Namespace, in.Name)
	if err != nil {
		return zero, fmt.Errorf("failed to read live component UID: %w", err)
	}
	if liveUID != in.UID {
		return zero, errStaleLocalIncarnation
	}
	retrySrc := proto.Clone(src).(*componentspb.RequestSource)
	retrySrc.ExpectedOwnerUid = ownerUID
	resp, err := fn(ctx, retrySrc)
	r.fence.RecordConflict(err)
	if !isOwnerGoneRejection(err) {
		return resp, classifyOwnershipError(ctx, r, in, err)
	}
	// R1: the recorded owner was deleted between the rejection and the CAS retry, so no active
	// record exists. A plain sourced call creates a fresh record for this live incarnation.
	resp, err = fn(ctx, src)
	r.fence.RecordConflict(err)
	return resp, classifyOwnershipError(ctx, r, in, err)
}

// isOwnerGoneRejection reports whether err is the SOURCE_INCARNATION_MISMATCH that CMS returns
// for expected_owner_uid when no active record exists: it carries no owner metadata.
func isOwnerGoneRejection(err error) bool {
	info, _ := extractErrorInfo(err)
	return info != nil && info.GetReason() == store.ReasonSourceIncarnationMismatch && info.GetMetadata()[store.MetadataKeyOwnerUID] == ""
}

func (r *Reconciler) requestSource(in ReconcileComponentInput) *componentspb.RequestSource {
	cluster := in.Cluster
	if cluster == nil && r.fence != nil {
		cluster = r.fence.Identity()
	}
	if in.UID == "" || cluster == nil {
		return nil
	}
	return &componentspb.RequestSource{
		Uid:        in.UID,
		Generation: max(in.Generation, 1),
		Namespace:  in.Namespace,
		Cluster:    cluster,
	}
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
// is active, it reports DependenciesReady (set only after passing the gate), or its last Ready
// condition reflects an actuated workload (wasComponentPreviouslyActive).
func (r *Reconciler) isActuated(in ReconcileComponentInput) bool {
	if r.helmManager != nil && in.Spec.GetType() == componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT && r.helmManager.IsComponentActive(resolveComponentRef(in)) {
		return true
	}
	if findCondition(in.ExistingConditions, ConditionTypeDependenciesReady) != nil {
		return true
	}
	return wasComponentPreviouslyActive(in.ExistingConditions)
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
func (r *Reconciler) reportGated(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult, reason, msg string) (*ReconcileResult, error) {
	res.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
	if r.isActuated(in) || reason == ReasonHelmUpgradePending {
		preserveExistingAccess(res, in.ExistingAccess)
	}
	setReadiness(res, in, ConditionStatusFalse, reason, msg)
	// Compared before syncObservedToCMS, which rewrites Ready on a source conflict.
	changed := healthConditionsChanged(in.ExistingConditions, res.Conditions, in.Generation)
	if err := r.syncObservedToCMS(ctx, in, canonicalName, res, ConditionStatusFalse, true); err != nil {
		return res, err
	}
	if changed {
		logging.Info(ctx, "Component reconciliation gated", "name", canonicalName, "generation", in.Generation, "reason", reason, "detail", msg)
	} else {
		logging.Debug(ctx, "Component reconciliation gated", "name", canonicalName, "generation", in.Generation, "reason", reason, "detail", msg)
	}
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
		preserveExistingAccess(res, in.ExistingAccess)
		setReadiness(res, in, ConditionStatusFalse, ReasonHelmUpgradeFailed, err.Error())
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
	for _, c := range existing {
		if c.Type == ConditionTypeReady && c.Status == ConditionStatusTrue && c.ObservedGeneration == generation {
			return true
		}
	}
	return false
}

// evaluateHealth probes the runtime health of an active component (in.Enabled == true), sets the Ready and
// Reconciling/Stalled conditions, updates state to COMPONENT_STATE_ENABLED (or PENDING_RECONCILIATION), and reports to CMS.
// Note: Disabled components (in.Enabled == false) reach evaluateHealth only through evaluateLocalHealth,
// while their deactivation is blocked and the workload keeps running.
func (r *Reconciler) evaluateHealth(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult) (*ReconcileResult, error) {
	isHealthy, reason, healthMsg := r.probeComponentHealth(ctx, in)
	res.Access = &AccessStatus{EnvVars: make(map[string]string)}
	preserveExistingAccess(res, in.ExistingAccess)
	if reason == ReasonHelmStatusUnavailable && preserveHealthConditions(in, res) {
		setSynced(res, in, ConditionStatusTrue, ReasonCMSSynced, cmsSyncedMessage)
		logging.Warn(ctx, "Transient component health check failure; keeping previous health conditions", "name", canonicalName, "generation", in.Generation, "detail", healthMsg)
		return res, nil
	}

	readyStatus := ConditionStatusFalse
	res.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
	if isHealthy {
		readyStatus = ConditionStatusTrue
		res.State = componentspb.ComponentState_COMPONENT_STATE_ENABLED
	}
	setReadiness(res, in, readyStatus, reason, healthMsg)
	if err := r.syncReadyToCMS(ctx, in, canonicalName, res, readyStatus); err != nil {
		return res, err
	}
	logHealthEvaluated(ctx, in, canonicalName, res)
	return res, nil
}

// logHealthEvaluated logs a health evaluation at Info when it changed the Ready, Reconciling, or
// Stalled condition recorded in the custom resource (see healthConditionsChanged), and at Debug for
// the unchanged result that every periodic pass over a steady component produces. A local-only
// evaluation (in.skipCMSSync, such as a workload kept running while its deactivation is blocked)
// compares Ready only, because its caller sets Reconciling and Stalled afterwards. It logs the final
// Ready condition, which differs from the probe result when reporting to CMS encountered a source conflict.
func logHealthEvaluated(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult) {
	condTypes := healthConditionTypes
	if in.skipCMSSync {
		condTypes = []string{ConditionTypeReady}
	}
	var healthy bool
	var reason string
	if ready := findCondition(res.Conditions, ConditionTypeReady); ready != nil {
		healthy, reason = ready.Status == ConditionStatusTrue, ready.Reason
	}
	if conditionsChanged(in.ExistingConditions, res.Conditions, in.Generation, condTypes) {
		logging.Info(ctx, "Evaluated component health", "name", canonicalName, "generation", in.Generation, "state", res.State.String(), "healthy", healthy, "reason", reason)
	} else {
		logging.Debug(ctx, "Evaluated component health", "name", canonicalName, "generation", in.Generation, "state", res.State.String(), "healthy", healthy, "reason", reason)
	}
}

// healthConditionTypes are the conditions whose change makes a pass log at Info.
var healthConditionTypes = []string{ConditionTypeReady, ConditionTypeReconciling, ConditionTypeStalled}

// healthConditionsChanged reports whether the Ready, Reconciling, or Stalled condition in current
// differs from existing in presence, status, or reason, or was last observed in existing at a
// generation other than generation (a first evaluation, or the first one of a new custom resource generation).
func healthConditionsChanged(existing, current []Condition, generation int64) bool {
	return conditionsChanged(existing, current, generation, healthConditionTypes)
}

// conditionsChanged is healthConditionsChanged over the condition types condTypes.
func conditionsChanged(existing, current []Condition, generation int64, condTypes []string) bool {
	for _, condType := range condTypes {
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

// preserveHealthConditions copies the existing Ready/Reconciling/Stalled conditions into res and derives the state from them.
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
	deleteCondition(&res.Conditions, ConditionTypeReconciling)
	deleteCondition(&res.Conditions, ConditionTypeStalled)
	for _, c := range in.ExistingConditions {
		if c.Type == ConditionTypeReady || c.Type == ConditionTypeReconciling || c.Type == ConditionTypeStalled {
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
	spec, _, enabled := ParseSpecFromUnstructured(obj.Object["spec"])
	opts := ParseHelmComponentOptions(obj.GetName(), obj.GetNamespace(), obj.Object["spec"], spec)
	wopts := ParseWrapperRef(obj.Object["spec"], obj.GetNamespace())
	return r.ReconcileComponent(ctx, ReconcileComponentInput{
		Namespace:                  obj.GetNamespace(),
		Name:                       obj.GetName(),
		Generation:                 obj.GetGeneration(),
		UID:                        string(obj.GetUID()),
		ComponentGeneration:        ParseComponentGenerationFromUnstructured(obj.Object["status"]),
		ComponentID:                ParseComponentIDFromUnstructured(obj.Object["status"]),
		Spec:                       spec,
		IsDeleting:                 obj.GetDeletionTimestamp() != nil,
		Enabled:                    enabled,
		ExistingObservedGeneration: ParseObservedGenerationFromUnstructured(obj.Object["status"]),
		ExistingConditions:         ParseConditionsFromUnstructured(obj.Object["status"]),
		ExistingAccess:             ParseAccessFromUnstructured(obj.Object["status"]),
		HelmOpts:                   opts,
		WrapperOpts:                wopts,
		Dependencies:               deps,
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
