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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	ctrl "sigs.k8s.io/controller-runtime"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	v1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	v1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	"xmanager_cloud/cmd/actuator/k8s_client"
	constants "xmanager_cloud/kubernetes/common/constants"
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

func TestNewScheme(t *testing.T) {
	scheme, err := newScheme()
	if err != nil {
		t.Fatalf("newScheme() unexpected error: %v", err)
	}
	for _, obj := range []runtimeclient.Object{&corev1.Pod{}, &v1alpha2.JobSet{}, &v1beta1.Workload{}, &v1beta1.ClusterQueue{}} {
		if _, _, err := scheme.ObjectKinds(obj); err != nil {
			t.Errorf("newScheme() does not recognize %T: %v", obj, err)
		}
	}
}

func TestNewCacheOptions(t *testing.T) {
	opts, err := newCacheOptions()
	if err != nil {
		t.Fatalf("newCacheOptions() unexpected error: %v", err)
	}
	if len(opts.ByObject) != 2 {
		t.Errorf("newCacheOptions() expected 2 ByObject entries, got %d", len(opts.ByObject))
	}
	selectors := make(map[string]labels.Selector)
	for obj, byObj := range opts.ByObject {
		switch obj.(type) {
		case *v1alpha2.JobSet:
			selectors["jobset"] = byObj.Label
		case *corev1.Pod:
			selectors["pod"] = byObj.Label
		default:
			t.Errorf("newCacheOptions() has unexpected ByObject entry for %T", obj)
		}
	}

	tests := []struct {
		name   string
		object string
		labels labels.Set
		want   bool
	}{
		{name: "xmc-managed jobset", object: "jobset", labels: labels.Set{constants.ManagedByLabel: constants.XmcLabelValue}, want: true},
		{name: "foreign jobset", object: "jobset", labels: labels.Set{constants.ManagedByLabel: "someone-else"}, want: false},
		{name: "unlabeled jobset", object: "jobset", labels: labels.Set{}, want: false},
		{name: "jobset pod", object: "pod", labels: labels.Set{v1alpha2.JobSetNameKey: "js"}, want: true},
		{name: "non-jobset pod", object: "pod", labels: labels.Set{"app": "other"}, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sel, ok := selectors[tc.object]
			if !ok || sel == nil {
				t.Fatalf("newCacheOptions() has no label selector for %s", tc.object)
			}
			if got := sel.Matches(tc.labels); got != tc.want {
				t.Errorf("%s selector %q Matches(%v) = %t, want %t", tc.object, sel, tc.labels, got, tc.want)
			}
		})
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

func TestMetricsBindAddress(t *testing.T) {
	tests := []struct {
		name string
		port int
		want string
	}{
		{
			name: "zero port disables metrics server",
			port: 0,
			want: "0",
		},
		{
			name: "negative port disables metrics server",
			port: -1,
			want: "0",
		},
		{
			name: "positive port formats bind address",
			port: 8082,
			want: ":8082",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := metricsBindAddress(tt.port); got != tt.want {
				t.Errorf("metricsBindAddress(%d) = %q, want %q", tt.port, got, tt.want)
			}
		})
	}
}
