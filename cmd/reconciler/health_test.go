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
package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	healthzpkg "sigs.k8s.io/controller-runtime/pkg/healthz"
	"xmanager_cloud/kubernetes/actuator/k8s_client/k8sclient"
	migversion "xmanager_cloud/storage/spanner/migrations/version"
)

type mockSpannerHealthClient struct {
	version int64
	dirty   bool
	err     error
}

func (m *mockSpannerHealthClient) QuerySchemaVersion(ctx context.Context) (int64, bool, error) {
	return m.version, m.dirty, m.err
}

type mockDiscovery struct {
	discovery.DiscoveryInterface
	serverVersionFunc func() (*version.Info, error)
}

func (m mockDiscovery) ServerVersion() (*version.Info, error) {
	if m.serverVersionFunc != nil {
		return m.serverVersionFunc()
	}
	return nil, nil
}

type mockClientset struct {
	kubernetes.Interface
	discoveryFunc func() discovery.DiscoveryInterface
}

func (m mockClientset) Discovery() discovery.DiscoveryInterface {
	if m.discoveryFunc != nil {
		return m.discoveryFunc()
	}
	return nil
}

type fakeHealthManager struct {
	ctrl.Manager
	healthzErr error
	readyzErr  error
	healthzFn  healthzpkg.Checker
	readyzFn   healthzpkg.Checker
}

func (f *fakeHealthManager) AddHealthzCheck(name string, check healthzpkg.Checker) error {
	if f.healthzErr != nil {
		return f.healthzErr
	}
	f.healthzFn = check
	return nil
}

func (f *fakeHealthManager) AddReadyzCheck(name string, check healthzpkg.Checker) error {
	if f.readyzErr != nil {
		return f.readyzErr
	}
	f.readyzFn = check
	return nil
}

func TestActiveHealthChecker_Combinations(t *testing.T) {
	expectedSchemaVersion := migversion.EssExpectedSchemaVersion()

	mockCSSuccess := mockClientset{
		discoveryFunc: func() discovery.DiscoveryInterface {
			return mockDiscovery{
				serverVersionFunc: func() (*version.Info, error) {
					return &version.Info{Major: "1", Minor: "28"}, nil
				},
			}
		},
	}
	k8sClientSuccess := k8sclient.NewWithClients(mockCSSuccess, nil, k8sclient.ActuatorRuntimeConfig{}, nil, nil, nil)

	mockCSFailure := mockClientset{
		discoveryFunc: func() discovery.DiscoveryInterface {
			return mockDiscovery{
				serverVersionFunc: func() (*version.Info, error) {
					return nil, errors.New("cluster unreachable")
				},
			}
		},
	}
	k8sClientFailure := k8sclient.NewWithClients(mockCSFailure, nil, k8sclient.ActuatorRuntimeConfig{}, nil, nil, nil)

	tests := []struct {
		name      string
		k8s       *k8sclient.Client
		spClient  *mockSpannerHealthClient
		wantError bool
	}{
		{
			name:      "both clients nil",
			k8s:       nil,
			spClient:  nil,
			wantError: false,
		},
		{
			name:      "k8s nil, spanner success",
			k8s:       nil,
			spClient:  &mockSpannerHealthClient{version: expectedSchemaVersion, dirty: false, err: nil},
			wantError: false,
		},
		{
			name:      "k8s nil, spanner query error",
			k8s:       nil,
			spClient:  &mockSpannerHealthClient{version: 0, dirty: false, err: errors.New("spanner connection refused")},
			wantError: true,
		},
		{
			name:      "k8s nil, spanner dirty schema",
			k8s:       nil,
			spClient:  &mockSpannerHealthClient{version: expectedSchemaVersion, dirty: true, err: nil},
			wantError: true,
		},
		{
			name:      "k8s nil, spanner version mismatch",
			k8s:       nil,
			spClient:  &mockSpannerHealthClient{version: expectedSchemaVersion + 100, dirty: false, err: nil},
			wantError: true,
		},
		{
			name:      "k8s success, spanner nil",
			k8s:       k8sClientSuccess,
			spClient:  nil,
			wantError: false,
		},
		{
			name:      "k8s failure, spanner nil",
			k8s:       k8sClientFailure,
			spClient:  nil,
			wantError: true,
		},
		{
			name:      "both k8s and spanner success",
			k8s:       k8sClientSuccess,
			spClient:  &mockSpannerHealthClient{version: expectedSchemaVersion, dirty: false, err: nil},
			wantError: false,
		},
		{
			name:      "k8s failure with spanner success",
			k8s:       k8sClientFailure,
			spClient:  &mockSpannerHealthClient{version: expectedSchemaVersion, dirty: false, err: nil},
			wantError: true,
		},
		{
			name:      "k8s success with spanner failure",
			k8s:       k8sClientSuccess,
			spClient:  &mockSpannerHealthClient{version: 0, dirty: false, err: errors.New("spanner failure")},
			wantError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var sp spClientInterface
			if tc.spClient != nil {
				sp = tc.spClient
			}
			checker := activeHealthChecker(tc.k8s, sp)
			req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
			err := checker(req)
			if (err != nil) != tc.wantError {
				t.Fatalf("activeHealthChecker() error = %v, wantError = %v", err, tc.wantError)
			}
		})
	}
}

type spClientInterface = interface {
	QuerySchemaVersion(ctx context.Context) (int64, bool, error)
}

func TestRegisterHealthChecks(t *testing.T) {
	t.Run("nil manager returns error", func(t *testing.T) {
		err := registerHealthChecks(nil, nil, nil)
		if err == nil {
			t.Fatal("registerHealthChecks(nil, ...) expected error, got nil")
		}
	})

	t.Run("healthz registration failure", func(t *testing.T) {
		mgr := &fakeHealthManager{
			healthzErr: errors.New("healthz probe registration failed"),
		}
		err := registerHealthChecks(mgr, nil, nil)
		if err == nil {
			t.Fatal("registerHealthChecks() expected healthz error, got nil")
		}
	})

	t.Run("readyz registration failure", func(t *testing.T) {
		mgr := &fakeHealthManager{
			readyzErr: errors.New("readyz probe registration failed"),
		}
		err := registerHealthChecks(mgr, nil, nil)
		if err == nil {
			t.Fatal("registerHealthChecks() expected readyz error, got nil")
		}
	})

	t.Run("successful registration", func(t *testing.T) {
		mgr := &fakeHealthManager{}
		err := registerHealthChecks(mgr, nil, nil)
		if err != nil {
			t.Fatalf("registerHealthChecks() unexpected error: %v", err)
		}
		if mgr.healthzFn == nil {
			t.Fatal("healthzFn not registered")
		}
		if mgr.readyzFn == nil {
			t.Fatal("readyzFn not registered")
		}
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		if err := mgr.healthzFn(req); err != nil {
			t.Errorf("healthz check returned error: %v", err)
		}
	})
}
