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
	"strings"

	componentspb "github.com/google/xmc/api/protos/components/v1alpha1"
)

// resolveAndValidateWrapperRef resolves WrapperRef from input and validates namespace isolation and JSONPath syntax.
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
	if ref.Name != "" {
		if err := ValidateWrapperRefJSONPaths(ref); err != nil {
			return ref, ReasonInvalidWrapperSpec, err.Error()
		}
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

// reconcile3PWrapper executes the reconciliation lifecycle for a third-party custom resource wrapper,
// querying the dynamic StatusDetector to evaluate CR readiness and reporting state to CMS.
func (r *Reconciler) reconcile3PWrapper(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult) (*ReconcileResult, error) {
	if r.prober == nil {
		// Without a status detector there is nothing to verify; report the wrapper as ready.
		return r.handle3PReadyStatus(ctx, in, canonicalName, res, nil)
	}
	ref, unreadyReason, unreadyMsg := resolveAndValidateWrapperRef(in)
	if unreadyReason != "" {
		return r.handle3PUnreadyStatus(ctx, in, canonicalName, res, unreadyReason, unreadyMsg)
	}
	if ref.Name == "" {
		// resolveAndValidateWrapperRef guarantees an operator-less endpoint when the target name is empty.
		return r.reconcileOperatorlessWrapper(ctx, in, canonicalName, res, resolveOperatorlessEndpoint(in))
	}

	resStatus, err := r.prober.DetectStatus(ctx, ref)
	if err != nil || resStatus == nil {
		return r.handle3PStatusDetectionFailure(ctx, in, canonicalName, res, ref, err)
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

func (r *Reconciler) handle3PStatusDetectionFailure(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult, ref WrapperRef, err error) (*ReconcileResult, error) {
	if err == nil {
		errmsg := "status detector returned nil status"
		if _, cmsErr := r.handle3PUnreadyStatus(ctx, in, canonicalName, res, ReasonStatusDetectionFailed, errmsg); cmsErr != nil {
			return res, fmt.Errorf("failed to detect 3P status for %s/%s: %s (CMS update failed: %v)", ref.Namespace, ref.Name, errmsg, cmsErr)
		}
		return res, fmt.Errorf("failed to detect 3P status for %s/%s: %s", ref.Namespace, ref.Name, errmsg)
	}
	reason := ReasonStatusDetectionFailed
	if errors.Is(err, ErrInvalidWrapperSpec) {
		reason = ReasonInvalidWrapperSpec
	} else if errors.Is(err, ErrContractBreach) {
		reason = ReasonContractBroken
	}
	if _, cmsErr := r.handle3PUnreadyStatus(ctx, in, canonicalName, res, reason, err.Error()); cmsErr != nil {
		return res, fmt.Errorf("failed to detect 3P status for %s/%s: %w (CMS update failed: %v)", ref.Namespace, ref.Name, err, cmsErr)
	}
	return res, fmt.Errorf("failed to detect 3P status for %s/%s: %w", ref.Namespace, ref.Name, err)
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
// A zero timeout defers to the prober's configured default.
func (r *Reconciler) verifyEndpointHealth(ctx context.Context, endpoint string) (bool, string, string) {
	probeRes, err := probeEndpointHealth(ctx, r.prober, endpoint, 0)
	if errors.Is(err, errNilProbeResult) {
		return false, "ProbeFailed", fmt.Sprintf("Health probe returned nil result for %s", endpoint)
	}
	if err != nil {
		return false, "ProbeFailed", fmt.Sprintf("Health probe failed for %s: %v", endpoint, err)
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
	setReadiness(res, in, ConditionStatusFalse, ConditionStatusTrue, reason, message)
	res.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
	return res, r.syncReadyToCMS(ctx, in, canonicalName, res, ConditionStatusFalse)
}

// handle3PReadyStatus transitions component state to COMPONENT_STATE_ENABLED and populates Ready=True conditions.
func (r *Reconciler) handle3PReadyStatus(ctx context.Context, in ReconcileComponentInput, canonicalName string, res *ReconcileResult, resStatus *ResourceStatus) (*ReconcileResult, error) {
	msg := "3P wrapper resource is operational and ready"
	if resStatus != nil && resStatus.Message != "" {
		msg = resStatus.Message
	}
	res.State = componentspb.ComponentState_COMPONENT_STATE_ENABLED
	populateAccessEnvVars(res, resStatus)
	setReadiness(res, in, ConditionStatusTrue, ConditionStatusFalse, ReasonComponentHealthy, msg)
	return res, r.syncReadyToCMS(ctx, in, canonicalName, res, ConditionStatusTrue)
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
