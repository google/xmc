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
	"sync"
	"sync/atomic"
	"testing"

	ctrl "golang/sigs_k8s_io/controller_runtime/v/v0_11/controllerruntime"
	"xmanager_cloud/kubernetes/actuator/k8s_client/k8sclient"
)

type fakeStartManager struct {
	ctrl.Manager
	startErr error
}

func (f *fakeStartManager) Start(ctx context.Context) error {
	if f.startErr != nil {
		return f.startErr
	}
	return nil
}

func TestManager_HealthCheckers_NilManager(t *testing.T) {
	var m *Manager
	k8s, sp := m.HealthCheckers()
	if k8s != nil {
		t.Errorf("HealthCheckers() k8sClient = %v, want nil", k8s)
	}
	if sp != nil {
		t.Errorf("HealthCheckers() spannerClient = %v, want nil", sp)
	}
}

func TestManager_HealthCheckers_NonNilManager(t *testing.T) {
	k8sClient := k8sclient.NewWithClients(nil, nil, k8sclient.ActuatorRuntimeConfig{}, nil, nil, nil)
	m := &Manager{
		k8sClient:     k8sClient,
		spannerClient: nil,
	}
	k8s, sp := m.HealthCheckers()
	if k8s != k8sClient {
		t.Errorf("HealthCheckers() k8sClient = %v, want %v", k8s, k8sClient)
	}
	if sp != nil {
		t.Errorf("HealthCheckers() spannerClient = %v, want nil", sp)
	}
}

func TestManager_Close(t *testing.T) {
	t.Run("nil manager", func(t *testing.T) {
		var m *Manager
		if err := m.Close(); err != nil {
			t.Errorf("Close() error = %v, want nil", err)
		}
	})

	t.Run("nil cleanup", func(t *testing.T) {
		m := &Manager{}
		if err := m.Close(); err != nil {
			t.Errorf("Close() error = %v, want nil", err)
		}
	})

	t.Run("cleanup succeeds", func(t *testing.T) {
		cleaned := false
		m := &Manager{
			cleanup: func() error {
				cleaned = true
				return nil
			},
		}
		if err := m.Close(); err != nil {
			t.Errorf("Close() error = %v, want nil", err)
		}
		if !cleaned {
			t.Error("Close() did not invoke cleanup function")
		}
	})

	t.Run("cleanup returns error", func(t *testing.T) {
		expectedErr := errors.New("cleanup failed")
		m := &Manager{
			cleanup: func() error {
				return expectedErr
			},
		}
		err := m.Close()
		if !errors.Is(err, expectedErr) {
			t.Errorf("Close() error = %v, want %v", err, expectedErr)
		}
	})

	t.Run("cleanup is idempotent", func(t *testing.T) {
		cleanCount := 0
		m := &Manager{
			cleanup: func() error {
				cleanCount++
				return nil
			},
		}
		if err := m.Close(); err != nil {
			t.Errorf("First Close() error = %v, want nil", err)
		}
		if err := m.Close(); err != nil {
			t.Errorf("Second Close() error = %v, want nil", err)
		}
		if cleanCount != 1 {
			t.Errorf("cleanup called %d times, want 1", cleanCount)
		}
	})

	t.Run("cleanup is concurrent safe", func(t *testing.T) {
		var cleanCount int32
		m := &Manager{
			cleanup: func() error {
				atomic.AddInt32(&cleanCount, 1)
				return nil
			},
		}
		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = m.Close()
			}()
		}
		wg.Wait()
		if got := atomic.LoadInt32(&cleanCount); got != 1 {
			t.Errorf("cleanup called %d times concurrently, want 1", got)
		}
	})
}

func TestInitSpannerAndStorage_EmptyDBName(t *testing.T) {
	sp, st, err := initSpannerAndStorage(context.Background(), Config{})
	if err != nil {
		t.Fatalf("initSpannerAndStorage() unexpected error: %v", err)
	}
	if sp != nil {
		t.Errorf("initSpannerAndStorage() spannerClient = %v, want nil", sp)
	}
	if st != nil {
		t.Errorf("initSpannerAndStorage() storage = %v, want nil", st)
	}
}

func TestStartManager(t *testing.T) {
	t.Run("start succeeds", func(t *testing.T) {
		mgr := &fakeStartManager{}
		if err := startManager(context.Background(), mgr); err != nil {
			t.Fatalf("startManager() unexpected error: %v", err)
		}
	})

	t.Run("start returns error", func(t *testing.T) {
		expectedErr := errors.New("start failed")
		mgr := &fakeStartManager{startErr: expectedErr}
		err := startManager(context.Background(), mgr)
		if !errors.Is(err, expectedErr) {
			t.Fatalf("startManager() error = %v, want %v", err, expectedErr)
		}
	})
}

func TestManager_Start(t *testing.T) {
	mgr := &fakeStartManager{}
	m := &Manager{
		ctrlMgr: mgr,
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("m.Start() unexpected error: %v", err)
	}
}

func TestInitCtrlManager_NoKubeconfig(t *testing.T) {
	t.Setenv("KUBECONFIG", "/nonexistent/kubeconfig")
	_, err := initCtrlManager(context.Background(), Config{})
	if err == nil {
		t.Log("initCtrlManager unexpectedly found kubeconfig")
	} else {
		t.Logf("initCtrlManager returned expected error on missing kubeconfig: %v", err)
	}
}

func TestManager_CtrlManager(t *testing.T) {
	t.Run("nil manager returns nil", func(t *testing.T) {
		var m *Manager
		if m.CtrlManager() != nil {
			t.Errorf("CtrlManager() = %v, want nil", m.CtrlManager())
		}
	})

	t.Run("non-nil manager returns ctrlMgr", func(t *testing.T) {
		fakeMgr := &fakeStartManager{}
		m := &Manager{ctrlMgr: fakeMgr}
		if m.CtrlManager() != fakeMgr {
			t.Errorf("CtrlManager() = %v, want %v", m.CtrlManager(), fakeMgr)
		}
	})
}
