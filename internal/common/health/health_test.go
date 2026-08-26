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
package health

import (
	"context"
	"errors"
	"testing"

	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"xmanager_cloud/internal/storage/spanner/emulator"
	migversion "xmanager_cloud/internal/storage/spanner/migrations/version"
)

func TestCheckSpanner(t *testing.T) {
	ctx := context.Background()

	t.Run("nil client returns ErrNilClient", func(t *testing.T) {
		if err := CheckSpanner(ctx, nil); !errors.Is(err, ErrNilClient) {
			t.Errorf("CheckSpanner(ctx, nil) = %v, want %v", err, ErrNilClient)
		}
	})

	t.Run("healthy client returns no error", func(t *testing.T) {
		clients := emulator.SetupWithEmulator(ctx, t)
		xidClient := clients.XIDClient
		// xidClient is automatically closed by Cleanup in emulator.SetupWithEmulator

		if err := CheckSpanner(ctx, xidClient); err != nil {
			t.Errorf("CheckSpanner(ctx, healthyClient) = %v, want nil", err)
		}
	})

	t.Run("closed client returns error", func(t *testing.T) {
		// We need a real client to close it.
		clients := emulator.SetupWithEmulator(ctx, t)
		xidClient := clients.XIDClient
		xidClient.Close()

		if err := CheckSpanner(ctx, xidClient); err == nil {
			t.Error("CheckSpanner(ctx, closedClient) succeeded, want error")
		}
	})

	t.Run("realSpannerHealthClient Close closes underlying client", func(t *testing.T) {
		clients := emulator.SetupWithEmulator(ctx, t)
		healthCl := NewSpannerHealthClient(clients.XIDClient)
		if closer, ok := healthCl.(interface{ Close() error }); ok {
			if err := closer.Close(); err != nil {
				t.Fatalf("unexpected error on Close: %v", err)
			}
		} else {
			t.Fatal("expected realSpannerHealthClient to implement Close()")
		}
	})
}

type mockSpannerHealthClient struct {
	version int64
	dirty   bool
	err     error
}

func (m mockSpannerHealthClient) QuerySchemaVersion(ctx context.Context) (int64, bool, error) {
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

func TestCheckKubernetes(t *testing.T) {
	t.Run("nil clientset returns ErrNilClient", func(t *testing.T) {
		if err := CheckKubernetes(nil); !errors.Is(err, ErrNilClient) {
			t.Errorf("CheckKubernetes(nil) = %v, want %v", err, ErrNilClient)
		}
	})

	t.Run("nil discovery returns ErrNilClient", func(t *testing.T) {
		mockCS := mockClientset{
			discoveryFunc: func() discovery.DiscoveryInterface {
				return nil
			},
		}
		if err := CheckKubernetes(mockCS); !errors.Is(err, ErrNilClient) {
			t.Errorf("CheckKubernetes(nil discovery) = %v, want %v", err, ErrNilClient)
		}
	})

	t.Run("discovery error returns error", func(t *testing.T) {
		expectedErr := errors.New("server version error")
		mockCS := mockClientset{
			discoveryFunc: func() discovery.DiscoveryInterface {
				return mockDiscovery{
					serverVersionFunc: func() (*version.Info, error) {
						return nil, expectedErr
					},
				}
			},
		}
		if err := CheckKubernetes(mockCS); err == nil || !errors.Is(err, expectedErr) {
			t.Errorf("CheckKubernetes(discovery error) = %v, want error wrapping %v", err, expectedErr)
		}
	})

	t.Run("healthy returns no error", func(t *testing.T) {
		mockCS := mockClientset{
			discoveryFunc: func() discovery.DiscoveryInterface {
				return mockDiscovery{
					serverVersionFunc: func() (*version.Info, error) {
						return &version.Info{}, nil
					},
				}
			},
		}
		if err := CheckKubernetes(mockCS); err != nil {
			t.Errorf("CheckKubernetes(healthy) = %v, want nil", err)
		}
	})
}

func TestCheckSpannerServices(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name       string
		versionOff int64
		dirty      bool
		dbErr      error
		isNil      bool
		wantErr    bool
	}{
		{
			name:    "success when version matches",
			wantErr: false,
		},
		{
			name:       "fails when version mismatch (higher)",
			versionOff: 1,
			wantErr:    true,
		},
		{
			name:       "fails when version mismatch (lower)",
			versionOff: -1,
			wantErr:    true,
		},
		{
			name:    "fails when dirty",
			dirty:   true,
			wantErr: true,
		},
		{
			name:    "fails on spanner error",
			dbErr:   errors.New("spanner error"),
			wantErr: true,
		},
		{
			name:    "fails when client is nil",
			isNil:   true,
			wantErr: true,
		},
	}

	services := []struct {
		name            string
		checkFn         func(context.Context, SpannerHealthClient) error
		expectedVersion int64
	}{
		{
			name:            "ESS",
			checkFn:         CheckSpannerESS,
			expectedVersion: migversion.EssExpectedSchemaVersion(),
		},
		{
			name:            "XID",
			checkFn:         CheckSpannerXID,
			expectedVersion: migversion.XidExpectedSchemaVersion(),
		},
	}

	for _, svc := range services {
		t.Run(svc.name, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					var client SpannerHealthClient
					if !tc.isNil {
						client = mockSpannerHealthClient{
							version: svc.expectedVersion + tc.versionOff,
							dirty:   tc.dirty,
							err:     tc.dbErr,
						}
					}
					err := svc.checkFn(ctx, client)
					if (err != nil) != tc.wantErr {
						t.Errorf("checkFn() error = %v, wantErr %v", err, tc.wantErr)
					}
				})
			}
		})
	}
}
