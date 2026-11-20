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
	"errors"
	"testing"

	"xmanager_cloud/internal/experiment/experimenttest"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/workunit"
	"xmanager_cloud/pkg/components/store"
)

const testStudyControllerSA = "xmc-study-controller@my-project.iam.gserviceaccount.com"

type stubAdmissionChecker struct {
	admitted bool
	err      error
	calls    int
}

func (s *stubAdmissionChecker) IsUserAuthorized(_ context.Context, _ string) (bool, error) {
	s.calls++
	return s.admitted, s.err
}

type countingComponentReader struct {
	workunit.ComponentReader
	calls int
}

func (c *countingComponentReader) GetActiveComponent(ctx context.Context, canonicalName string) (*store.ComponentRecord, error) {
	c.calls++
	return c.ComponentReader.GetActiveComponent(ctx, canonicalName)
}

func TestStudyControllerAdmission_NilDelegate(t *testing.T) {
	t.Parallel()
	if got := studyControllerAdmission(nil, &storage.MockComponentRepository{}); got != nil {
		t.Errorf("studyControllerAdmission(nil, repo) = %v, want nil", got)
	}
}

func TestStudyControllerAdmission_IsUserAuthorized(t *testing.T) {
	t.Parallel()
	errDelegate := errors.New("crm unavailable")
	errCatalog := errors.New("db unavailable")
	hpoRepo := func(sa string) workunit.ComponentReader {
		return &storage.MockComponentRepository{
			Records: experimenttest.HPOCatalog(map[string]string{"studyControllerServiceAccount": sa}),
		}
	}

	tests := []struct {
		name               string
		delegate           stubAdmissionChecker
		components         workunit.ComponentReader
		email              string
		wantAdmitted       bool
		wantErr            error
		wantCatalogLookups int
	}{
		{
			name:               "delegate admits user with zero catalog lookups",
			delegate:           stubAdmissionChecker{admitted: true},
			components:         hpoRepo(testStudyControllerSA),
			email:              "alice@example.com",
			wantAdmitted:       true,
			wantCatalogLookups: 0,
		},
		{
			name:               "delegate denies study controller admitted via catalog",
			delegate:           stubAdmissionChecker{admitted: false},
			components:         hpoRepo(testStudyControllerSA),
			email:              testStudyControllerSA,
			wantAdmitted:       true,
			wantCatalogLookups: 1,
		},
		{
			name:               "study controller matched case-insensitively",
			delegate:           stubAdmissionChecker{admitted: false},
			components:         hpoRepo(testStudyControllerSA),
			email:              "XMC-Study-Controller@My-Project.iam.gserviceaccount.com",
			wantAdmitted:       true,
			wantCatalogLookups: 1,
		},
		{
			name:               "delegate errors during outage but study controller still admitted",
			delegate:           stubAdmissionChecker{err: errDelegate},
			components:         hpoRepo(testStudyControllerSA),
			email:              testStudyControllerSA,
			wantAdmitted:       true,
			wantCatalogLookups: 1,
		},
		{
			name:               "delegate denies non-controller user",
			delegate:           stubAdmissionChecker{admitted: false},
			components:         hpoRepo(testStudyControllerSA),
			email:              "mallory@example.com",
			wantAdmitted:       false,
			wantCatalogLookups: 1,
		},
		{
			name:               "delegate error preserved for non-controller user",
			delegate:           stubAdmissionChecker{err: errDelegate},
			components:         hpoRepo(testStudyControllerSA),
			email:              "alice@example.com",
			wantAdmitted:       false,
			wantErr:            errDelegate,
			wantCatalogLookups: 1,
		},
		{
			name:               "delegate error takes precedence over catalog error",
			delegate:           stubAdmissionChecker{err: errDelegate},
			components:         &storage.MockComponentRepository{GetActiveComponentErr: errCatalog},
			email:              "alice@example.com",
			wantAdmitted:       false,
			wantErr:            errDelegate,
			wantCatalogLookups: 1,
		},
		{
			name:               "catalog error propagated when delegate denies",
			delegate:           stubAdmissionChecker{admitted: false},
			components:         &storage.MockComponentRepository{GetActiveComponentErr: errCatalog},
			email:              testStudyControllerSA,
			wantAdmitted:       false,
			wantErr:            errCatalog,
			wantCatalogLookups: 1,
		},
		{
			name:               "missing hpo component fails closed",
			delegate:           stubAdmissionChecker{admitted: false},
			components:         &storage.MockComponentRepository{},
			email:              testStudyControllerSA,
			wantAdmitted:       false,
			wantCatalogLookups: 1,
		},
		{
			name:         "nil components fails closed",
			delegate:     stubAdmissionChecker{admitted: false},
			components:   nil,
			email:        testStudyControllerSA,
			wantAdmitted: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			delegate := tt.delegate
			var reader workunit.ComponentReader
			var counter *countingComponentReader
			if tt.components != nil {
				counter = &countingComponentReader{ComponentReader: tt.components}
				reader = counter
			}
			checker := studyControllerAdmission(&delegate, reader)
			got, err := checker.IsUserAuthorized(context.Background(), tt.email)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("IsUserAuthorized() err = %v, want %v", err, tt.wantErr)
			}
			if got != tt.wantAdmitted {
				t.Errorf("IsUserAuthorized() = %v, want %v", got, tt.wantAdmitted)
			}
			if delegate.calls != 1 {
				t.Errorf("delegate calls = %d, want 1", delegate.calls)
			}
			if counter != nil && counter.calls != tt.wantCatalogLookups {
				t.Errorf("catalog lookups = %d, want %d", counter.calls, tt.wantCatalogLookups)
			}
		})
	}
}
