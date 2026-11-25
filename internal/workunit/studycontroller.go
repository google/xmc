// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package workunit

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/auth/principal"
	"xmanager_cloud/pkg/components/store"
)

const (
	// hpoComponentName is the catalog name of the hyperparameter optimization component.
	hpoComponentName = "hpo"
	// studyControllerServiceAccountParameter is the hpo component parameter that holds the email of
	// the study controller's service account.
	studyControllerServiceAccountParameter = "studyControllerServiceAccount"
)

// ErrNotStudyController is returned when a caller other than the study controller creates work
// units of a STUDY experiment, including when no study controller service account is configured.
var ErrNotStudyController = status.Error(codes.PermissionDenied, "work units of a study experiment may only be created by the study controller")

// ComponentReader reads active component records by canonical name.
type ComponentReader interface {
	GetActiveComponent(ctx context.Context, canonicalName string) (*store.ComponentRecord, error)
}

// IsStudyController reports whether caller, a canonical email as returned by
// auth.AuthenticateUserFromContext, is the studyControllerServiceAccount parameter of an enabled or
// pending-reconciliation hpo component read from components. It fails closed, returning (false, nil)
// when components is nil, the hpo component is missing, disabled/disabling/deleted, experimental
// without acknowledgement, or the parameter is empty or invalid. Any non-NotFound catalog read error
// is returned to the caller.
func IsStudyController(ctx context.Context, components ComponentReader, caller string) (bool, error) {
	if components == nil {
		return false, nil
	}
	want, err := readStudyControllerPrincipal(ctx, components)
	if errors.Is(err, ErrNotStudyController) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	got, err := principal.CanonicalEmail(caller)
	if err != nil {
		return false, nil
	}
	return got == want, nil
}

// AuthorizeStudyController returns nil when caller, a canonical email as returned by
// auth.AuthenticateUserFromContext, is the studyControllerServiceAccount parameter of an enabled or
// pending-reconciliation hpo component read from components. It fails closed with
// ErrNotStudyController when the component is missing, disabled/disabling/deleted/unspecified,
// experimental without acknowledgement, or when caller or the parameter is empty or not a plain email address.
func AuthorizeStudyController(ctx context.Context, components ComponentReader, caller string) error {
	if components == nil {
		return fmt.Errorf("component catalog is not available")
	}
	want, err := readStudyControllerPrincipal(ctx, components)
	if err != nil {
		return err
	}
	got, err := principal.CanonicalEmail(caller)
	if err != nil || got != want {
		return fmt.Errorf("caller %s is not the study controller: %w", caller, ErrNotStudyController)
	}
	return nil
}

// readStudyControllerPrincipal loads the active hpo component and returns the canonical email
// configured in studyControllerServiceAccount. Missing or invalid configuration wraps
// ErrNotStudyController; other catalog read errors are returned wrapped directly.
func readStudyControllerPrincipal(ctx context.Context, components ComponentReader) (string, error) {
	rec, err := components.GetActiveComponent(ctx, hpoComponentName)
	if errors.Is(err, store.ErrComponentNotFound) {
		return "", fmt.Errorf("component %q not found: %w", hpoComponentName, ErrNotStudyController)
	}
	if err != nil {
		return "", fmt.Errorf("failed to read component %q: %w", hpoComponentName, err)
	}
	if rec == nil {
		return "", fmt.Errorf("component %q record is missing: %w", hpoComponentName, ErrNotStudyController)
	}
	if rec.State != componentspb.ComponentState_COMPONENT_STATE_ENABLED &&
		rec.State != componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION {
		return "", fmt.Errorf("component %q is not active (state %v): %w", hpoComponentName, rec.State, ErrNotStudyController)
	}
	if rec.Spec != nil &&
		rec.Spec.GetStability() == componentspb.ComponentStability_COMPONENT_STABILITY_EXPERIMENTAL &&
		!rec.Spec.GetAcknowledgeExperimental() {
		return "", fmt.Errorf("component %q experimental stability is not acknowledged: %w", hpoComponentName, ErrNotStudyController)
	}
	want, err := principal.CanonicalEmail(rec.Spec.GetParameters()[studyControllerServiceAccountParameter])
	if err != nil {
		return "", fmt.Errorf("component %q has no valid %s parameter: %w", hpoComponentName, studyControllerServiceAccountParameter, ErrNotStudyController)
	}
	return want, nil
}
