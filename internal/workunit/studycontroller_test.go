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

package workunit_test

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"xmanager_cloud/internal/experiment/experimenttest"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/workunit"
	"xmanager_cloud/pkg/components/store"
)

func TestAuthorizeStudyController(t *testing.T) {
	errRead := errors.New("catalog unavailable")
	tests := []struct {
		name       string
		components *storage.MockComponentRepository
		caller     string
		wantCode   codes.Code
	}{
		{name: "study controller", components: hpoCatalog(studyControllerGSA), caller: studyControllerGSA, wantCode: codes.OK},
		{name: "parameter compared case-insensitively", components: hpoCatalog("XMC-Study-Controller@My-Project.iam.gserviceaccount.com"), caller: studyControllerGSA, wantCode: codes.OK},
		{name: "other caller", components: hpoCatalog(studyControllerGSA), caller: "alice@example.com", wantCode: codes.PermissionDenied},
		{name: "empty caller", components: hpoCatalog(studyControllerGSA), caller: "", wantCode: codes.PermissionDenied},
		{name: "empty parameter", components: hpoCatalog(""), caller: studyControllerGSA, wantCode: codes.PermissionDenied},
		{name: "missing parameter", components: &storage.MockComponentRepository{Records: experimenttest.HPOCatalog(nil)}, caller: studyControllerGSA, wantCode: codes.PermissionDenied},
		{name: "parameter with display name", components: hpoCatalog("SC <" + studyControllerGSA + ">"), caller: studyControllerGSA, wantCode: codes.PermissionDenied},
		{name: "parameter with surrounding whitespace", components: hpoCatalog(" " + studyControllerGSA), caller: studyControllerGSA, wantCode: codes.PermissionDenied},
		{name: "hpo component absent", components: &storage.MockComponentRepository{}, caller: studyControllerGSA, wantCode: codes.PermissionDenied},
		{name: "nil hpo record", components: &storage.MockComponentRepository{Records: map[string]*store.ComponentRecord{"hpo": nil}}, caller: studyControllerGSA, wantCode: codes.PermissionDenied},
		{name: "hpo record without spec", components: &storage.MockComponentRepository{Records: map[string]*store.ComponentRecord{"hpo": {}}}, caller: studyControllerGSA, wantCode: codes.PermissionDenied},
		{name: "catalog read failure", components: &storage.MockComponentRepository{GetActiveComponentErr: errRead}, caller: studyControllerGSA, wantCode: codes.Unknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := workunit.AuthorizeStudyController(context.Background(), tt.components, tt.caller)
			if got := status.Code(err); got != tt.wantCode {
				t.Errorf("AuthorizeStudyController() = %v (code %v), want code %v", err, got, tt.wantCode)
			}
			if tt.wantCode == codes.PermissionDenied && !errors.Is(err, workunit.ErrNotStudyController) {
				t.Errorf("AuthorizeStudyController() = %v, want it to wrap ErrNotStudyController", err)
			}
		})
	}
}

func TestAuthorizeStudyController_NoCatalog(t *testing.T) {
	err := workunit.AuthorizeStudyController(context.Background(), nil, studyControllerGSA)
	if err == nil || errors.Is(err, workunit.ErrNotStudyController) {
		t.Errorf("AuthorizeStudyController(nil catalog) = %v, want a non-authorization error", err)
	}
}

func TestIsStudyController(t *testing.T) {
	errRead := errors.New("catalog unavailable")
	tests := []struct {
		name       string
		components workunit.ComponentReader
		caller     string
		want       bool
		wantErr    error
	}{
		{name: "matching study controller", components: hpoCatalog(studyControllerGSA), caller: studyControllerGSA, want: true},
		{name: "caller compared case-insensitively", components: hpoCatalog(studyControllerGSA), caller: "XMC-Study-Controller@My-Project.iam.gserviceaccount.com", want: true},
		{name: "parameter compared case-insensitively", components: hpoCatalog("XMC-Study-Controller@My-Project.iam.gserviceaccount.com"), caller: studyControllerGSA, want: true},
		{name: "other caller", components: hpoCatalog(studyControllerGSA), caller: "alice@example.com", want: false},
		{name: "empty caller", components: hpoCatalog(studyControllerGSA), caller: "", want: false},
		{name: "nil components fails closed", components: nil, caller: studyControllerGSA, want: false},
		{name: "hpo component absent fails closed", components: &storage.MockComponentRepository{}, caller: studyControllerGSA, want: false},
		{name: "nil hpo record fails closed", components: &storage.MockComponentRepository{Records: map[string]*store.ComponentRecord{"hpo": nil}}, caller: studyControllerGSA, want: false},
		{name: "hpo record without spec fails closed", components: &storage.MockComponentRepository{Records: map[string]*store.ComponentRecord{"hpo": {}}}, caller: studyControllerGSA, want: false},
		{name: "empty parameter fails closed", components: hpoCatalog(""), caller: studyControllerGSA, want: false},
		{name: "invalid parameter fails closed", components: hpoCatalog("SC <" + studyControllerGSA + ">"), caller: studyControllerGSA, want: false},
		{name: "catalog read failure propagated", components: &storage.MockComponentRepository{GetActiveComponentErr: errRead}, caller: studyControllerGSA, want: false, wantErr: errRead},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := workunit.IsStudyController(context.Background(), tt.components, tt.caller)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("IsStudyController() err = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("IsStudyController() = %v, want %v", got, tt.want)
			}
		})
	}
}
