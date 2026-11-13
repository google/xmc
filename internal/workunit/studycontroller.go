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

// componentReader reads active component records by canonical name.
type componentReader interface {
	GetActiveComponent(ctx context.Context, canonicalName string) (*store.ComponentRecord, error)
}

// AuthorizeStudyController returns nil when caller, a canonical email as returned by
// auth.AuthenticateUserFromContext, is the studyControllerServiceAccount parameter of the active hpo
// component read from components. It fails closed with ErrNotStudyController when the component is
// missing or the parameter is empty or not a plain email address.
func AuthorizeStudyController(ctx context.Context, components componentReader, caller string) error {
	if components == nil {
		return fmt.Errorf("component catalog is not available")
	}
	rec, err := components.GetActiveComponent(ctx, hpoComponentName)
	if errors.Is(err, store.ErrComponentNotFound) {
		return fmt.Errorf("component %q not found: %w", hpoComponentName, ErrNotStudyController)
	}
	if err != nil {
		return fmt.Errorf("failed to read component %q: %w", hpoComponentName, err)
	}
	want, err := principal.CanonicalEmail(rec.Spec.GetParameters()[studyControllerServiceAccountParameter])
	if err != nil {
		return fmt.Errorf("component %q has no valid %s parameter: %w", hpoComponentName, studyControllerServiceAccountParameter, ErrNotStudyController)
	}
	if caller != want {
		return fmt.Errorf("caller %s is not the study controller: %w", caller, ErrNotStudyController)
	}
	return nil
}
