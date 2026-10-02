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

// TODO: Split this file by responsibility (conditions.go, spec_parser.go,
// wrapper_reconciler.go, lifecycle.go, reconciler.go); see google/xmc#992 review.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/pkg/components/naming"
)

const (
	FinalizerDeletionProtection = "finalizers.xmc.dev/deletion-protection"
	// ReasonDependenciesNotReady marks an enabled component waiting on unready spec.dependencies.
	ReasonDependenciesNotReady = "DependenciesNotReady"
	// ReasonDeactivationBlocked marks a disabled component kept running because enabled components depend on it.
	ReasonDeactivationBlocked = "DeactivationBlocked"
	// ReasonDeactivationIncomplete marks a disabled component whose workload was removed before the CMS handshake completed.
	ReasonDeactivationIncomplete = "DeactivationIncomplete"
	// ReasonDependencyCycle marks an enabled component that is part of a spec.dependencies cycle.
	ReasonDependencyCycle   = "DependencyCycle"
	reasonComponentDisabled = "ComponentDisabled"
	// deactivationRecheckInterval throttles PrepareDeactivation while deactivation is blocked.
	deactivationRecheckInterval = 30 * time.Second
	// dependencyNotFoundMessagePrefix mirrors store.DependencyNotFoundMessagePrefix (pinned by a test).
	dependencyNotFoundMessagePrefix = "dependency not found:"
)

// CMSClient defines the interface for communicating with the CMS control plane.
type CMSClient interface {
	// SetComponentIntent registers or updates declarative component intent in CMS.
	SetComponentIntent(ctx context.Context, in *componentspb.SetComponentIntentRequest, opts ...grpc.CallOption) (*componentspb.SetComponentIntentResponse, error)
	// UpdateObservedGeneration updates the observed generation, state, and status message of a component in CMS.
	UpdateObservedGeneration(ctx context.Context, in *componentspb.UpdateObservedGenerationRequest, opts ...grpc.CallOption) (*componentspb.UpdateObservedGenerationResponse, error)
	// ExecuteDeletion executes component deletion in the CMS backend.
	ExecuteDeletion(ctx context.Context, in *componentspb.ExecuteDeletionRequest, opts ...grpc.CallOption) (*componentspb.ExecuteDeletionResponse, error)
	// PrepareDeactivation validates deactivation safety (FailedPrecondition on active dependents) and marks the component DISABLING.
	PrepareDeactivation(ctx context.Context, in *componentspb.PrepareDeactivationRequest, opts ...grpc.CallOption) (*componentspb.PrepareDeactivationResponse, error)
	// CompleteDeactivation transitions a DISABLING component to DISABLED.
	CompleteDeactivation(ctx context.Context, in *componentspb.CompleteDeactivationRequest, opts ...grpc.CallOption) (*componentspb.CompleteDeactivationResponse, error)
}

// StatusDetectorHealthProber defines the status detection and active health checking interface for 3P custom resources.
type StatusDetectorHealthProber interface {
	// DetectStatus queries the target 3P custom resource to determine readiness and extract access coordinates.
	DetectStatus(ctx context.Context, ref WrapperRef) (*ResourceStatus, error)
	// Probe executes an active HTTP/TCP probe against target.
	Probe(ctx context.Context, target string, timeout time.Duration) (*HealthProbeResult, error)
	// ProbeHealth evaluates the runtime health of a component based on its spec and default namespace, returning health status and message.
	ProbeHealth(ctx context.Context, spec *componentspb.ComponentSpec, defaultNamespace string) (bool, string)
}

// BridgeProber combines StatusDetector and HealthProber into StatusDetectorHealthProber.
type BridgeProber struct {
	detector     StatusDetector
	healthProber HealthProber
	probeTimeout time.Duration
}

var _ StatusDetectorHealthProber = (*BridgeProber)(nil)

// NewBridgeProber creates a BridgeProber combining detector and health prober.
func NewBridgeProber(detector StatusDetector, prober HealthProber, probeTimeout time.Duration) *BridgeProber {
	if probeTimeout <= 0 {
		probeTimeout = 5 * time.Second
	}
	return &BridgeProber{
		detector:     detector,
		healthProber: prober,
		probeTimeout: probeTimeout,
	}
}

// DetectStatus queries the target 3P resource via the underlying dynamic StatusDetector.
func (p *BridgeProber) DetectStatus(ctx context.Context, ref WrapperRef) (*ResourceStatus, error) {
	if p.detector == nil {
		return nil, errors.New("status detector is uninitialized")
	}
	return p.detector.DetectStatus(ctx, ref)
}

// Probe executes an active HTTP/TCP health probe against target using the configured probe timeout.
func (p *BridgeProber) Probe(ctx context.Context, target string, timeout time.Duration) (*HealthProbeResult, error) {
	if p.healthProber == nil {
		return nil, errors.New("health prober is uninitialized")
	}
	if timeout <= 0 {
		timeout = p.probeTimeout
	}
	return p.healthProber.Probe(ctx, target, timeout)
}

// ProbeHealth evaluates whether a component is healthy based on its spec and default namespace.
// For 3P wrappers, it queries the status detector and verifies endpoint health via active probing.
// Non-3P components are reported healthy; the Reconciler only relies on this for 1P components when no
// HelmManager is configured, otherwise 1P health comes from HelmManager.ComponentHealth.
func (p *BridgeProber) ProbeHealth(ctx context.Context, spec *componentspb.ComponentSpec, defaultNamespace string) (bool, string) {
	if spec == nil {
		return false, "spec is nil"
	}
	if spec.GetType() != componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER {
		return true, "Component healthy"
	}
	if spec.GetWrapperRef() == nil {
		return p.probeOperatorlessHealth(ctx, spec)
	}
	wRef := ToBridgeWrapperRef(spec.GetWrapperRef(), defaultNamespace)
	if p.detector == nil {
		return false, "status detector is uninitialized"
	}
	res, err := p.detector.DetectStatus(ctx, wRef)
	if err != nil {
		return false, fmt.Sprintf("status detection error: %v", err)
	}
	if res == nil {
		return false, "status detector returned nil status"
	}
	if !res.Found {
		return false, res.Message
	}
	if !res.Ready {
		return false, res.Message
	}
	if ok, probeMsg := p.probe3PEndpoint(ctx, res.Endpoint); !ok {
		return false, probeMsg
	} else if probeMsg != "" {
		return true, probeMsg
	}
	return true, res.Message
}

func (p *BridgeProber) probeOperatorlessHealth(ctx context.Context, spec *componentspb.ComponentSpec) (bool, string) {
	endpoint := resolveProbeEndpoint(spec.GetParameters())
	if endpoint == "" {
		return false, "no wrapperRef or connection endpoint parameters configured"
	}
	ok, probeMsg := p.probe3PEndpoint(ctx, endpoint)
	if !ok {
		if probeMsg == "" {
			return false, "health probe failed"
		}
		return false, probeMsg
	}
	if probeMsg != "" {
		return true, probeMsg
	}
	return true, "Component healthy"
}

func (p *BridgeProber) probe3PEndpoint(ctx context.Context, endpoint string) (bool, string) {
	if endpoint == "" || p.healthProber == nil {
		return true, ""
	}
	probeRes, err := p.healthProber.Probe(ctx, endpoint, p.probeTimeout)
	if err != nil {
		return false, fmt.Sprintf("health probe error: %v", err)
	}
	if probeRes == nil {
		return false, "health prober returned nil result"
	}
	if !probeRes.Healthy {
		return false, probeRes.Message
	}
	return true, probeRes.Message
}

type serverClientAdapter struct {
	server componentspb.ComponentManagerServiceServer
}

// NewCMSClientAdapter creates a CMSClient adapter wrapping an in-process gRPC server.
func NewCMSClientAdapter(s componentspb.ComponentManagerServiceServer) CMSClient {
	return &serverClientAdapter{server: s}
}

func (a *serverClientAdapter) SetComponentIntent(ctx context.Context, in *componentspb.SetComponentIntentRequest, opts ...grpc.CallOption) (*componentspb.SetComponentIntentResponse, error) {
	return a.server.SetComponentIntent(ctx, in)
}
func (a *serverClientAdapter) UpdateObservedGeneration(ctx context.Context, in *componentspb.UpdateObservedGenerationRequest, opts ...grpc.CallOption) (*componentspb.UpdateObservedGenerationResponse, error) {
	return a.server.UpdateObservedGeneration(ctx, in)
}
func (a *serverClientAdapter) ExecuteDeletion(ctx context.Context, in *componentspb.ExecuteDeletionRequest, opts ...grpc.CallOption) (*componentspb.ExecuteDeletionResponse, error) {
	return a.server.ExecuteDeletion(ctx, in)
}
func (a *serverClientAdapter) PrepareDeactivation(ctx context.Context, in *componentspb.PrepareDeactivationRequest, opts ...grpc.CallOption) (*componentspb.PrepareDeactivationResponse, error) {
	return a.server.PrepareDeactivation(ctx, in)
}
func (a *serverClientAdapter) CompleteDeactivation(ctx context.Context, in *componentspb.CompleteDeactivationRequest, opts ...grpc.CallOption) (*componentspb.CompleteDeactivationResponse, error) {
	return a.server.CompleteDeactivation(ctx, in)
}

// AccessStatus encapsulates network and access metadata (such as environment variables) for a component.
type AccessStatus struct {
	// EnvVars maps configuration and coordinate keys to their resolved values.
	EnvVars map[string]string
}

// ReconcileResult encapsulates the outcome of a single component reconciliation pass.
type ReconcileResult struct {
	// ObservedGeneration is the custom resource metadata.generation evaluated.
	ObservedGeneration int64
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
// 4. If !Enabled, synchronizes intent via applyIntent and handles deactivation via
// reconcileDisabled (gated by CMS deactivation safety).
// 5. Otherwise delegates to reconcileEnabled (dependency gating, intent, actuation, health).
func (r *Reconciler) ReconcileComponent(ctx context.Context, in ReconcileComponentInput) (*ReconcileResult, error) {
	if in.Name == "" || in.Spec == nil {
		return nil, errors.New("component name and spec must not be empty/nil")
	}
	logging.Debug(ctx, "Reconciling Component custom resource", "name", in.Name, "generation", in.Generation, "enabled", in.Enabled, "isDeleting", in.IsDeleting)
	in.Spec.Generation = in.Generation
	canonicalName := naming.CanonicalComponentName(in.Name)
	in.Spec.Dependencies = dropSelfDependency(ctx, in.Spec.GetDependencies(), canonicalName)
	res := &ReconcileResult{ObservedGeneration: in.Generation, State: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION}

	if in.IsDeleting {
		return r.reconcileDeletion(ctx, in, canonicalName, res)
	}

	if !in.HasFinalizer {
		res.AddFinalizer = true
		logging.Info(ctx, "Attaching deletion protection finalizer", "name", in.Name)
	}
	if !in.Enabled {
		if err := r.applyIntent(ctx, in, canonicalName, res); err != nil {
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
// store's FailedPrecondition for an unregistered dependency, or "" otherwise.
func dependencyNotRegisteredMessage(err error) string {
	// Not status.FromError: for wrapped errors it replaces the message with the full err.Error(),
	// which would defeat the prefix match below.
	var se interface{ GRPCStatus() *status.Status }
	if !errors.As(err, &se) {
		return ""
	}
	st := se.GRPCStatus()
	if st.Code() != codes.FailedPrecondition || !strings.HasPrefix(st.Message(), dependencyNotFoundMessagePrefix) {
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

// reconcileDeletion handles component teardown when a deletionTimestamp is present.
// If DeletionProtection is true, deletion is blocked, DeletionBlocked condition is set to True,
// and finalizers remain intact. Otherwise, CMS ExecuteDeletion is called first to validate
// deletion safety before mutating Kubernetes resources. If CMS reports NotFound, deletion
// is treated as idempotently completed in CMS and proceeds to 1P workload teardown.
func (r *Reconciler) reconcileDeletion(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult) (*ReconcileResult, error) {
	if in.DeletionProtection {
		logging.Warn(ctx, "Component deletion blocked by deletion protection", "name", in.Name, "generation", in.Generation)
		setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDeletionBlocked, ConditionStatusTrue, in.Generation, "DeletionProtectionEnabled", fmt.Sprintf("Cannot delete %q: deletionProtection is enabled", in.Name))
		setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeReady, ConditionStatusFalse, in.Generation, "DeletingBlocked", "Deletion blocked by deletion protection")
		setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDegraded, ConditionStatusFalse, in.Generation, "DeletingBlocked", "Deletion blocked by deletion protection")
		return res, nil
	}
	if _, err := r.cmsClient.ExecuteDeletion(ctx, &componentspb.ExecuteDeletionRequest{Name: canonicalName, DeletionPolicy: in.Spec.GetDeletionPolicy()}); err != nil {
		if status.Code(err) != codes.NotFound {
			setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeReady, ConditionStatusFalse, in.Generation, "CMSDeletionFailed", err.Error())
			setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDegraded, ConditionStatusTrue, in.Generation, "CMSDeletionFailed", err.Error())
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

func (r *Reconciler) teardown1PComponent(ctx context.Context, in ReconcileComponentInput, res *ReconcileResult) error {
	if in.Spec.GetType() != componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT || r.helmManager == nil {
		return nil
	}
	ref := resolveComponentRef(in)
	if in.Spec.GetDeletionPolicy() == componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN {
		if err := r.helmManager.Orphan1PComponent(ctx, ref); err != nil {
			setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeReady, ConditionStatusFalse, in.Generation, "HelmOrphanFailed", err.Error())
			setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDegraded, ConditionStatusTrue, in.Generation, "HelmOrphanFailed", err.Error())
			return fmt.Errorf("failed to orphan 1P Helm component: %w", err)
		}
		return nil
	}
	if err := r.helmManager.Purge1PComponent(ctx, ref); err != nil {
		setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeReady, ConditionStatusFalse, in.Generation, "HelmDeletionFailed", err.Error())
		setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDegraded, ConditionStatusTrue, in.Generation, "HelmDeletionFailed", err.Error())
		return fmt.Errorf("failed to delete 1P Helm component: %w", err)
	}
	return nil
}
func (r *Reconciler) applyIntent(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult) error {
	setResp, err := r.cmsClient.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{Name: canonicalName, Spec: in.Spec})
	if err != nil {
		res.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
		setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeReady, ConditionStatusFalse, in.Generation, "IntentApplicationFailed", err.Error())
		setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDegraded, ConditionStatusTrue, in.Generation, "IntentApplicationFailed", err.Error())
		return fmt.Errorf("failed to apply component intent to CMS: %w", err)
	}
	if setResp != nil && setResp.GetComponent() != nil && setResp.GetComponent().GetStatus() != nil {
		res.State = setResp.GetComponent().GetStatus().GetState()
	}
	logging.Info(ctx, "Applied component intent to CMS", "name", canonicalName, "generation", in.Generation, "state", res.State.String())
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
	setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeReady, ConditionStatusFalse, in.Generation, reasonComponentDisabled, "Component is disabled")
	setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDegraded, ConditionStatusFalse, in.Generation, reasonComponentDisabled, "Component is disabled")
	if err := r.updateObservedCMS(ctx, canonicalName, in.Generation, res.State, "Ready=False"); err != nil {
		return res, fmt.Errorf("failed to update observed generation in CMS: %w", err)
	}
	logging.Info(ctx, "Component disabled", "name", canonicalName, "generation", in.Generation)
	return res, nil
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
		markDeactivationFailed(in, res, ReasonDeactivationIncomplete, "workload already removed; "+msg)
		return res, nil
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
	setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeReady, ConditionStatusFalse, in.Generation, reason, msg)
	setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDegraded, ConditionStatusTrue, in.Generation, reason, msg)
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
	setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeReady, ConditionStatusFalse, in.Generation, reason, msg)
	setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDegraded, degraded, in.Generation, reason, msg)
	if !shouldSyncToCMS(in.ExistingConditions, ConditionStatusFalse, in.Generation) {
		return res, nil
	}
	if err := r.updateObservedCMS(ctx, canonicalName, in.Generation, res.State, "Ready=False"); err != nil && status.Code(err) != codes.NotFound {
		return res, fmt.Errorf("failed to update observed generation in CMS: %w", err)
	}
	resetReadyTransitionTime(res.Conditions)
	logging.Info(ctx, "Component reconciliation gated", "name", canonicalName, "generation", in.Generation, "reason", reason, "message", msg)
	return res, nil
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
	if !r.helmManager.IsComponentActive(ref) && !wasPreviouslyActive {
		return nil
	}
	if err := r.helmManager.Delete1PComponent(ctx, ref); err != nil {
		res.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
		setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeReady, ConditionStatusFalse, in.Generation, "HelmDeletionFailed", err.Error())
		setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDegraded, ConditionStatusTrue, in.Generation, "HelmDeletionFailed", err.Error())
		return fmt.Errorf("failed to delete 1P Helm component: %w", err)
	}
	return nil
}

// ensure1PComponent sets component enablement and parameter values in the Helm release. When
// holdUpgrade is true (dependencies unready), a pending install/upgrade is skipped and held is true.
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
	if r.helmManager.IsComponentActuated(ref, in.Generation) {
		logging.Debug(ctx, "1P component already actuated at current generation, skipping Helm upgrade", "name", in.Name, "generation", in.Generation)
		return false, nil
	}
	if holdUpgrade {
		logging.Debug(ctx, "Holding 1P Helm upgrade until dependencies are ready", "name", in.Name, "generation", in.Generation)
		return true, nil
	}
	in.HelmOpts.Ref = ref
	in.HelmOpts.Enabled = in.Enabled
	if err := r.helmManager.Ensure1PComponent(ctx, ref, in.HelmOpts); err != nil {
		res.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
		setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeReady, ConditionStatusFalse, in.Generation, ReasonHelmUpgradeFailed, err.Error())
		setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDegraded, ConditionStatusTrue, in.Generation, ReasonHelmUpgradeFailed, err.Error())
		return false, fmt.Errorf("failed to ensure 1P Helm component: %w", err)
	}
	r.helmManager.RecordActuation(ref, in.Generation)
	logging.Debug(ctx, "Ensured 1P Helm component", "name", in.Name, "namespace", in.Namespace, "generation", in.Generation)
	return false, nil
}

// wasComponentPreviouslyActive infers from the last Ready condition whether a workload may exist;
// gated (DependenciesNotReady, DependencyCycle) or disabled components never had one actuated.
func wasComponentPreviouslyActive(existing []Condition) bool {
	ready := findCondition(existing, ConditionTypeReady)
	if ready == nil {
		return false
	}
	switch ready.Reason {
	case "", reasonComponentDisabled, ReasonDependenciesNotReady, ReasonDependencyCycle:
		return ready.Status == ConditionStatusTrue
	}
	return true
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
		logging.Warn(ctx, "Transient component health check failure; keeping previous Ready/Degraded conditions", "name", canonicalName, "generation", in.Generation, "message", healthMsg)
		return res, nil
	}

	readyStatus, degradedStatus := healthConditionStatuses(isHealthy, reason)
	res.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
	if isHealthy {
		res.State = componentspb.ComponentState_COMPONENT_STATE_ENABLED
	}
	setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeReady, readyStatus, in.Generation, reason, healthMsg)
	setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDegraded, degradedStatus, in.Generation, reason, healthMsg)

	if shouldSyncToCMS(in.ExistingConditions, readyStatus, in.Generation) {
		if err := r.updateObservedCMS(ctx, canonicalName, in.Generation, res.State, fmt.Sprintf("Ready=%s", readyStatus)); err != nil {
			return res, fmt.Errorf("failed to update observed generation in CMS: %w", err)
		}
		resetReadyTransitionTime(res.Conditions)
	}
	logging.Info(ctx, "Evaluated component health", "name", canonicalName, "generation", in.Generation, "state", res.State.String(), "healthy", isHealthy, "reason", reason)
	return res, nil
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
		Namespace:          obj.GetNamespace(),
		Name:               obj.GetName(),
		Generation:         obj.GetGeneration(),
		Spec:               spec,
		IsDeleting:         obj.GetDeletionTimestamp() != nil,
		HasFinalizer:       hasFinalizer,
		DeletionProtection: dp,
		Enabled:            enabled,
		ExistingConditions: ParseConditionsFromUnstructured(obj.Object["status"]),
		HelmOpts:           opts,
		WrapperOpts:        wopts,
		Dependencies:       deps,
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

// updateObservedCMS sends an UpdateObservedGeneration RPC to CMS.
func (r *Reconciler) updateObservedCMS(ctx context.Context, name string, gen int64, state componentspb.ComponentState, statusMsg string) error {
	_, err := r.cmsClient.UpdateObservedGeneration(ctx, &componentspb.UpdateObservedGenerationRequest{
		Name: name, ObservedGeneration: gen, State: state, StatusMessage: statusMsg,
	})
	return err
}

// resolveAndValidateWrapperRef resolves WrapperRef from input and validates namespace isolation.
func resolveAndValidateWrapperRef(in ReconcileComponentInput) (WrapperRef, string, string) {
	ref := in.WrapperOpts
	if ref.Name == "" && in.Spec.GetWrapperRef() != nil {
		ref = ToBridgeWrapperRef(in.Spec.GetWrapperRef(), in.Namespace)
	}
	if ref.Name == "" && resolveOperatorlessEndpoint(in) == "" {
		return ref, "InvalidWrapperRef", "3P wrapper target name is empty"
	}
	if in.Namespace != "" && ref.Namespace != "" && ref.Namespace != in.Namespace {
		return ref, "CrossNamespaceForbidden", fmt.Sprintf("cross-namespace wrapperRef is forbidden (component in %q cannot target %q)", in.Namespace, ref.Namespace)
	}
	return ref, "", ""
}

func resolveOperatorlessEndpoint(in ReconcileComponentInput) string {
	if in.WrapperOpts.EndpointJSONPath != "" && !strings.HasPrefix(in.WrapperOpts.EndpointJSONPath, "$") {
		return in.WrapperOpts.EndpointJSONPath
	}
	if in.Spec == nil || in.Spec.GetWrapperRef() != nil {
		return ""
	}
	return resolveProbeEndpoint(in.Spec.GetParameters())
}

func firstNonEmptyToken(val string) string {
	if val == "" {
		return ""
	}
	for _, part := range strings.Split(val, ",") {
		if first := strings.TrimSpace(part); first != "" {
			return first
		}
	}
	return ""
}

func resolveProbeEndpoint(params map[string]string) string {
	if len(params) == 0 {
		return ""
	}
	if ep := firstNonEmptyToken(params["healthCheckEndpoint"]); ep != "" {
		return ep
	}
	return resolveConnectionEndpoint(params)
}

func resolveConnectionEndpoint(params map[string]string) string {
	if len(params) == 0 {
		return ""
	}
	for _, k := range []string{"endpoint", "clusterEndpoints", "brokerEndpoints"} {
		if first := firstNonEmptyToken(params[k]); first != "" {
			return first
		}
	}
	host := firstNonEmptyToken(params["host"])
	if host == "" {
		return ""
	}
	return formatHostEndpoint(host, params)
}

func formatHostEndpoint(host string, params map[string]string) string {
	if strings.Count(host, ":") == 1 || (strings.HasPrefix(host, "[") && strings.Contains(host, "]:")) {
		return host
	}
	for _, pk := range []string{"nativePort", "kafkaPort", "port", "httpPort"} {
		port := strings.TrimSpace(params[pk])
		if port == "" {
			continue
		}
		if strings.Count(host, ":") > 1 && !strings.HasPrefix(host, "[") {
			return fmt.Sprintf("[%s]:%s", host, port)
		}
		return fmt.Sprintf("%s:%s", host, port)
	}
	return host
}

// reconcile3PWrapper executes the reconciliation lifecycle for a third-party custom resource wrapper,
// querying the dynamic StatusDetector to evaluate CR readiness and reporting state to CMS.
func (r *Reconciler) reconcile3PWrapper(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult) (*ReconcileResult, error) {
	if r.prober == nil {
		return r.reconcile3PWrapperFallback(ctx, in, canonicalName, res)
	}
	ref, unreadyReason, unreadyMsg := resolveAndValidateWrapperRef(in)
	if unreadyReason != "" {
		return r.handle3PUnreadyStatus(ctx, in, canonicalName, res, unreadyReason, unreadyMsg)
	}
	if ref.Name == "" {
		if endpoint := resolveOperatorlessEndpoint(in); endpoint != "" {
			return r.reconcileOperatorlessWrapper(ctx, in, canonicalName, res, endpoint)
		}
	}

	resStatus, err := r.prober.DetectStatus(ctx, ref)
	if err != nil {
		if _, cmsErr := r.handle3PUnreadyStatus(ctx, in, canonicalName, res, "StatusDetectionFailed", err.Error()); cmsErr != nil {
			return res, fmt.Errorf("failed to detect 3P status for %s/%s: %w (CMS update failed: %v)", ref.Namespace, ref.Name, err, cmsErr)
		}
		return res, fmt.Errorf("failed to detect 3P status for %s/%s: %w", ref.Namespace, ref.Name, err)
	}
	if resStatus == nil {
		errmsg := "status detector returned nil status"
		if _, cmsErr := r.handle3PUnreadyStatus(ctx, in, canonicalName, res, "StatusDetectionFailed", errmsg); cmsErr != nil {
			return res, fmt.Errorf("failed to detect 3P status for %s/%s: %s (CMS update failed: %v)", ref.Namespace, ref.Name, errmsg, cmsErr)
		}
		return res, fmt.Errorf("failed to detect 3P status for %s/%s: %s", ref.Namespace, ref.Name, errmsg)
	}
	if !resStatus.Found {
		reason := "ResourceNotFound"
		if strings.Contains(resStatus.Message, "CRD not installed") {
			reason = "CRDNotFound"
		}
		return r.handle3PUnreadyStatus(ctx, in, canonicalName, res, reason, resStatus.Message)
	}
	if !resStatus.Ready {
		return r.handle3PUnreadyStatus(ctx, in, canonicalName, res, "ResourceNotReady", resStatus.Message)
	}

	if ok, reason, msg := r.verifyEndpointHealth(ctx, resStatus.Endpoint); !ok {
		return r.handle3PUnreadyStatus(ctx, in, canonicalName, res, reason, msg)
	}

	return r.handle3PReadyStatus(ctx, in, canonicalName, res, resStatus)
}

// reconcile3PWrapperFallback performs health evaluation when no dynamic status detector is configured.
func (r *Reconciler) reconcile3PWrapperFallback(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult) (*ReconcileResult, error) {
	isHealthy, healthMsg := true, "Component is active"
	if r.prober != nil {
		isHealthy, healthMsg = r.prober.ProbeHealth(ctx, in.Spec, in.Namespace)
	}
	if !isHealthy {
		return r.handle3PUnreadyStatus(ctx, in, canonicalName, res, ReasonHealthProbeFailed, healthMsg)
	}
	return r.handle3PReadyStatus(ctx, in, canonicalName, res, nil)
}

// reconcileOperatorlessWrapper handles Profile 3 (Operator-less static or external SaaS components).
// It bypasses CRD status detection and validates runtime availability via direct TCP/HTTP health probes.
func (r *Reconciler) reconcileOperatorlessWrapper(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult, probeEndpoint string) (*ReconcileResult, error) {
	if ok, reason, msg := r.verifyEndpointHealth(ctx, probeEndpoint); !ok {
		return r.handle3PUnreadyStatus(ctx, in, canonicalName, res, reason, msg)
	}
	connEndpoint := probeEndpoint
	var secretName string
	if in.Spec != nil && in.Spec.GetParameters() != nil {
		if ce := resolveConnectionEndpoint(in.Spec.GetParameters()); ce != "" {
			connEndpoint = ce
		}
		secretName = in.Spec.GetParameters()["secretName"]
	}
	resStatus := &ResourceStatus{
		Found:            true,
		Ready:            true,
		Endpoint:         connEndpoint,
		ConnectionSecret: secretName,
		Message:          "Static endpoint is reachable and healthy",
	}
	return r.handle3PReadyStatus(ctx, in, canonicalName, res, resStatus)
}

// verifyEndpointHealth performs an active probe against endpoint (if non-empty) to verify data-plane availability.
func (r *Reconciler) verifyEndpointHealth(ctx context.Context, endpoint string) (bool, string, string) {
	if endpoint == "" || r.prober == nil {
		return true, "", ""
	}
	probeRes, err := r.prober.Probe(ctx, endpoint, 0)
	if err != nil {
		return false, "ProbeFailed", fmt.Sprintf("Health probe failed for %s: %v", endpoint, err)
	}
	if probeRes == nil {
		return false, "ProbeFailed", fmt.Sprintf("Health probe returned nil result for %s", endpoint)
	}
	if !probeRes.Healthy {
		msg := probeRes.Message
		if msg == "" {
			msg = fmt.Sprintf("Health probe unhealthy for %s", endpoint)
		}
		return false, "ProbeUnhealthy", msg
	}
	return true, "", ""
}

// handle3PUnreadyStatus sets Ready=False and Degraded=True status conditions when 3P health or readiness fails.
func (r *Reconciler) handle3PUnreadyStatus(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult, reason, message string) (*ReconcileResult, error) {
	if message == "" {
		message = fmt.Sprintf("3P wrapper %s is not ready: %s", in.Name, reason)
	}
	setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeReady, ConditionStatusFalse, in.Generation, reason, message)
	setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDegraded, ConditionStatusTrue, in.Generation, reason, message)
	res.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
	if shouldSyncToCMS(in.ExistingConditions, ConditionStatusFalse, in.Generation) {
		if err := r.updateObservedCMS(ctx, canonicalName, in.Generation, res.State, "Ready=False"); err != nil {
			return res, fmt.Errorf("failed to update observed generation in CMS: %w", err)
		}
		resetReadyTransitionTime(res.Conditions)
	}
	return res, nil
}

// handle3PReadyStatus transitions component state to COMPONENT_STATE_ENABLED and populates Ready=True conditions.
func (r *Reconciler) handle3PReadyStatus(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult, resStatus *ResourceStatus) (*ReconcileResult, error) {
	msg := "3P wrapper resource is operational and ready"
	if resStatus != nil && resStatus.Message != "" {
		msg = resStatus.Message
	}
	res.State = componentspb.ComponentState_COMPONENT_STATE_ENABLED
	populateAccessEnvVars(res, resStatus)

	setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeReady, ConditionStatusTrue, in.Generation, ReasonComponentHealthy, msg)
	setCondition(&res.Conditions, in.ExistingConditions, ConditionTypeDegraded, ConditionStatusFalse, in.Generation, ReasonComponentHealthy, msg)

	if shouldSyncToCMS(in.ExistingConditions, ConditionStatusTrue, in.Generation) {
		if err := r.updateObservedCMS(ctx, canonicalName, in.Generation, res.State, "Ready=True"); err != nil {
			return res, fmt.Errorf("failed to update observed generation in CMS: %w", err)
		}
		resetReadyTransitionTime(res.Conditions)
	}
	return res, nil
}

func populateAccessEnvVars(res *ReconcileResult, resStatus *ResourceStatus) {
	if res == nil || resStatus == nil {
		return
	}
	if res.Access == nil {
		res.Access = &AccessStatus{EnvVars: make(map[string]string)}
	}
	if res.Access.EnvVars == nil {
		res.Access.EnvVars = make(map[string]string)
	}

	coords := ParseCoordinates(resStatus.Endpoint, resStatus.ConnectionSecret)
	if coords.RawEndpoint != "" {
		res.Access.EnvVars["ENDPOINT"] = coords.RawEndpoint
	}
	if coords.Host != "" {
		res.Access.EnvVars["HOST"] = coords.Host
	}
	if coords.Port != "" {
		res.Access.EnvVars["PORT"] = coords.Port
	}
	if coords.Scheme != "" {
		res.Access.EnvVars["SCHEME"] = coords.Scheme
	}
	if coords.SecretName != "" {
		res.Access.EnvVars["SECRET_NAME"] = coords.SecretName
	}
}
