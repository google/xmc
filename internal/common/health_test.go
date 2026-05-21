// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
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

	"golang/k8s_io/apimachinery/v/v0_23/pkg/version/version"
	"golang/k8s_io/client_go/v/v0_23/discovery/discovery"
	"golang/k8s_io/client_go/v/v0_23/kubernetes/kubernetes"
	"xmanager_cloud/storage/spanner/emulator/emulator"
)

func TestCheckSpanner(t *testing.T) {
	ctx := context.Background()

	t.Run("nil client returns ErrNilClient", func(t *testing.T) {
		if err := CheckSpanner(ctx, nil); !errors.Is(err, ErrNilClient) {
			t.Errorf("CheckSpanner(ctx, nil) = %v, want %v", err, ErrNilClient)
		}
	})

	t.Run("healthy client returns no error", func(t *testing.T) {
		xidClient, _, _ := emulator.SetupWithEmulator(ctx, t)
		// xidClient is automatically closed by Cleanup in emulator.SetupWithEmulator

		if err := CheckSpanner(ctx, xidClient); err != nil {
			t.Errorf("CheckSpanner(ctx, healthyClient) = %v, want nil", err)
		}
	})

	t.Run("closed client returns error", func(t *testing.T) {
		// We need a real client to close it.
		xidClient, _, _ := emulator.SetupWithEmulator(ctx, t)
		xidClient.Close()

		if err := CheckSpanner(ctx, xidClient); err == nil {
			t.Error("CheckSpanner(ctx, closedClient) succeeded, want error")
		}
	})
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
