// Copyright 2025 Google LLC
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

package server

import (
	"context"
	"strings"
	"testing"

	"xmanager_cloud/experiment_state_server/config"
	"xmanager_cloud/storage/storage"
	tq "xmanager_cloud/task_queue/client"
)

func TestServer_New_Validation(t *testing.T) {
	t.Parallel()

	validParams := func() Params {
		return Params{
			Config:           &config.Config{},
			Storage:          &storage.MockStorage{},
			Authorizer:       createTestAuthorizer(context.Background(), t),
			XidClient:        &mockXidClient{},
			WorkUnitActuator: &tq.FakeClient{},
		}
	}

	testCases := []struct {
		name        string
		modify      func(p *Params)
		wantErrText string
	}{
		{
			name: "success with all required params",
			modify: func(p *Params) {
			},
			wantErrText: "",
		},
		{
			name: "missing config",
			modify: func(p *Params) {
				p.Config = nil
			},
			wantErrText: "Config is required",
		},
		{
			name: "missing storage",
			modify: func(p *Params) {
				p.Storage = nil
			},
			wantErrText: "Storage is required",
		},
		{
			name: "missing authorizer",
			modify: func(p *Params) {
				p.Authorizer = nil
			},
			wantErrText: "Authorizer is required",
		},
		{
			name: "missing xid client",
			modify: func(p *Params) {
				p.XidClient = nil
			},
			wantErrText: "XidClient is required",
		},
		{
			name: "missing work unit actuator",
			modify: func(p *Params) {
				p.WorkUnitActuator = nil
			},
			wantErrText: "WorkUnitActuator is required",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := validParams()
			tc.modify(&p)

			s, err := New(p)
			if tc.wantErrText != "" {
				if err == nil {
					t.Fatalf("New(p) = (%v, nil), want error containing %q", s, tc.wantErrText)
				}
				if !strings.Contains(err.Error(), tc.wantErrText) {
					t.Errorf("New(p) error = %q, want error containing %q", err.Error(), tc.wantErrText)
				}
				return
			}

			if err != nil {
				t.Fatalf("New(validParams) returned unexpected error: %v", err)
			}
			if s == nil {
				t.Fatal("New(validParams) returned nil server")
			}
			if s.ExperimentHandler == nil || s.WorkUnitHandler == nil || s.ArtifactHandler == nil || s.StatusMessageHandler == nil {
				t.Fatal("New(validParams) left sub-handlers uninitialized")
			}
			if s.ExperimentHandler.server != s || s.WorkUnitHandler.server != s {
				t.Fatal("New(validParams) failed to wire server back-pointer")
			}
		})
	}
}
