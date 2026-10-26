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
)

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

func TestCheckSchemaVersion(t *testing.T) {
	ctx := context.Background()
	queryErr := errors.New("query failed")

	tests := []struct {
		name            string
		client          SchemaVersionClient
		expectedVersion int64
		wantErr         bool
		wantErrIs       error
	}{
		{
			name:            "version matches",
			client:          mockSpannerHealthClient{version: 5},
			expectedVersion: 5,
		},
		{
			name:            "version mismatch",
			client:          mockSpannerHealthClient{version: 4},
			expectedVersion: 5,
			wantErr:         true,
		},
		{
			name:            "dirty schema",
			client:          mockSpannerHealthClient{version: 5, dirty: true},
			expectedVersion: 5,
			wantErr:         true,
		},
		{
			name:            "query error is returned",
			client:          mockSpannerHealthClient{err: queryErr},
			expectedVersion: 5,
			wantErr:         true,
			wantErrIs:       queryErr,
		},
		{
			name:            "nil client",
			expectedVersion: 5,
			wantErr:         true,
			wantErrIs:       ErrNilClient,
		},
		{
			name:            "expected version not set",
			client:          mockSpannerHealthClient{},
			expectedVersion: 0,
			wantErr:         true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckSchemaVersion(ctx, tc.client, tc.expectedVersion)
			if (err != nil) != tc.wantErr {
				t.Fatalf("CheckSchemaVersion() error = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErrIs != nil && !errors.Is(err, tc.wantErrIs) {
				t.Errorf("CheckSchemaVersion() error = %v, want it to wrap %v", err, tc.wantErrIs)
			}
		})
	}
}
