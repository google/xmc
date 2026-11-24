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

package server

import (
	"context"

	authz "xmanager_cloud/internal/auth"
	"xmanager_cloud/internal/workunit"
)

type studyControllerAdmissionChecker struct {
	delegate   authz.AdmissionChecker
	components workunit.ComponentReader
}

var _ authz.AdmissionChecker = studyControllerAdmissionChecker{}

// studyControllerAdmission admits the active hpo component's studyControllerServiceAccount in
// addition to every principal admitted by delegate. The delegate is checked first so normal user
// traffic incurs zero component-catalog lookups; on delegate denial or error (e.g. a CRM outage),
// the configured study controller is still admitted.
func studyControllerAdmission(delegate authz.AdmissionChecker, components workunit.ComponentReader) authz.AdmissionChecker {
	if delegate == nil {
		return nil
	}
	return studyControllerAdmissionChecker{
		delegate:   delegate,
		components: components,
	}
}

// IsUserAuthorized reports whether email is admitted by the delegate or matches the configured
// study controller service account.
func (c studyControllerAdmissionChecker) IsUserAuthorized(ctx context.Context, email string) (bool, error) {
	ok, err := c.delegate.IsUserAuthorized(ctx, email)
	if err == nil && ok {
		return true, nil
	}
	isCtrl, ctrlErr := workunit.IsStudyController(ctx, c.components, email)
	if ctrlErr == nil && isCtrl {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if ctrlErr != nil {
		return false, ctrlErr
	}
	return false, nil
}
