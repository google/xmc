// Copyright 2025 Google LLC
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
	"strings"
	"testing"
	"time"

	"golang/google_api/cloudidentity/v1beta1/cloudidentity"
	"xmanager_cloud/auth/auth"
	"xmanager_cloud/storage/spanner/emulator/emulator"
	"xmanager_cloud/storage/spanner/spanner"
	"xmanager_cloud/storage/storage"
	"xmanager_cloud/xid_service/config"
)

type testServerOpt func(p *Params)

func withConfig(cfg config.Config) testServerOpt {
	return func(p *Params) {
		p.Config = cfg
	}
}

func withStorage(s storage.Storage) testServerOpt {
	return func(p *Params) {
		p.Storage = s
	}
}

func withAuthorizer(a auth.AuthorizerInterface) testServerOpt {
	return func(p *Params) {
		p.Authorizer = a
	}
}

func createTestAuthorizer(ctx context.Context, t *testing.T) *auth.Authorizer {
	t.Helper()
	idService := &cloudidentity.Service{}
	authorizer, err := auth.NewAuthorizer(ctx, idService, 10*time.Minute, 1*time.Minute, []string{"test-audience"}, auth.DefaultIAPIssuer, true, nil)
	if err != nil {
		t.Fatalf("Failed to create mock authorizer: %v", err)
	}
	return authorizer
}

func newTestServer(t *testing.T, opts ...testServerOpt) *XIDServer {
	t.Helper()
	p := Params{}
	for _, opt := range opts {
		opt(&p)
	}
	if p.Storage == nil {
		p.Storage = &storage.MockStorage{}
	}
	if p.Authorizer == nil {
		p.Authorizer = createTestAuthorizer(context.Background(), t)
	}
	s, err := New(p)
	if err != nil {
		t.Fatalf("newTestServer failed: %v", err)
	}
	return s
}

func createXidServer(ctx context.Context, t *testing.T, opts ...testServerOpt) *XIDServer {
	t.Helper()
	xidClient, _ := emulator.Setup(ctx, t)
	st, err := spanner.NewStorage(xidClient)
	if err != nil {
		t.Fatalf("failed to create emulator storage: %v", err)
	}
	p := Params{
		Config:     config.Config{},
		Authorizer: createTestAuthorizer(ctx, t),
		Storage:    st,
	}
	for _, opt := range opts {
		opt(&p)
	}
	s, err := New(p)
	if err != nil {
		t.Fatalf("createXidServer failed: %v", err)
	}
	return s
}

func TestServer_New_Validation(t *testing.T) {
	t.Parallel()

	validParams := func() Params {
		return Params{
			Config:     config.Config{},
			Storage:    &storage.MockStorage{},
			Authorizer: createTestAuthorizer(context.Background(), t),
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
			if s.storage != p.Storage || s.auth != p.Authorizer {
				t.Fatal("New(validParams) did not set storage or authorizer correctly")
			}
		})
	}
}

func TestNewXIDServer_Alias(t *testing.T) {
	t.Parallel()
	p := Params{
		Storage:    &storage.MockStorage{},
		Authorizer: createTestAuthorizer(context.Background(), t),
	}
	s, err := NewXIDServer(p)
	if err != nil {
		t.Fatalf("NewXIDServer(p) returned unexpected error: %v", err)
	}
	if s == nil {
		t.Fatal("NewXIDServer(p) returned nil server")
	}
}
