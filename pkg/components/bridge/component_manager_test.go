// Copyright 2026 Google LLC
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

package bridge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"xmanager_cloud/pkg/components/bridge/helm"
)

func TestComponentManagerOptions(t *testing.T) {
	tests := []struct {
		name        string
		cfg         ComponentManagerConfig
		wantMetrics string
	}{
		{name: "defaults disable metrics", cfg: ComponentManagerConfig{Namespace: "xmc"}, wantMetrics: "0"},
		{
			name:        "leader-elected bridge with endpoints",
			cfg:         ComponentManagerConfig{Namespace: "xmc", LeaderElection: true, MetricsBindAddress: ":8080", HealthProbeBindAddress: ":8081"},
			wantMetrics: ":8080",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := ComponentManagerOptions(tt.cfg)
			if _, ok := opts.Cache.DefaultNamespaces["xmc"]; !ok || len(opts.Cache.DefaultNamespaces) != 1 {
				t.Errorf("Cache.DefaultNamespaces = %v, want only xmc", opts.Cache.DefaultNamespaces)
			}
			if opts.Metrics.BindAddress != tt.wantMetrics || opts.HealthProbeBindAddress != tt.cfg.HealthProbeBindAddress {
				t.Errorf("metrics/health addresses = %q/%q, want %q/%q", opts.Metrics.BindAddress, opts.HealthProbeBindAddress, tt.wantMetrics, tt.cfg.HealthProbeBindAddress)
			}
			if opts.LeaderElection != tt.cfg.LeaderElection || opts.LeaderElectionID != ComponentLeaseName || opts.LeaderElectionNamespace != "xmc" || !opts.LeaderElectionReleaseOnCancel {
				t.Errorf("leader election = %t on %s/%s (release on cancel %t), want %t on xmc/%s releasing on cancel",
					opts.LeaderElection, opts.LeaderElectionNamespace, opts.LeaderElectionID, opts.LeaderElectionReleaseOnCancel, tt.cfg.LeaderElection, ComponentLeaseName)
			}
			if opts.GracefulShutdownTimeout == nil || *opts.GracefulShutdownTimeout != componentGracefulShutdownTimeout {
				t.Errorf("GracefulShutdownTimeout = %v, want %v", opts.GracefulShutdownTimeout, componentGracefulShutdownTimeout)
			}
			if helm.HelmShutdownTimeout >= componentGracefulShutdownTimeout {
				t.Errorf("HelmShutdownTimeout = %v, want less than the graceful shutdown timeout %v so Helm work stops before the Lease is released", helm.HelmShutdownTimeout, componentGracefulShutdownTimeout)
			}
		})
	}
}

// syncStateCache is a cache.Cache whose WaitForCacheSync reports synced.
type syncStateCache struct {
	cache.Cache
	synced bool
}

func (c syncStateCache) WaitForCacheSync(context.Context) bool { return c.synced }

func TestCacheSyncedChecker(t *testing.T) {
	for _, synced := range []bool{true, false} {
		err := cacheSyncedChecker(syncStateCache{synced: synced})(httptest.NewRequest(http.MethodGet, "/readyz", nil))
		if (err == nil) != synced {
			t.Errorf("cacheSyncedChecker() with synced=%t: error = %v", synced, err)
		}
	}
}

// informerRecorder is a cache.Informers that records GetInformer calls.
type informerRecorder struct {
	cache.Informers
	gvks  []schema.GroupVersionKind
	block []*bool
}

func (r *informerRecorder) GetInformer(_ context.Context, obj client.Object, opts ...cache.InformerGetOption) (cache.Informer, error) {
	o := &cache.InformerGetOptions{}
	for _, opt := range opts {
		opt(o)
	}
	r.gvks = append(r.gvks, obj.GetObjectKind().GroupVersionKind())
	r.block = append(r.block, o.BlockUntilSynced)
	return nil, nil
}

// TestRegisterComponentInformer checks that the readiness check's cache gets the Component
// informer without blocking: a follower runs no controller that would register it.
func TestRegisterComponentInformer(t *testing.T) {
	r := &informerRecorder{}
	if err := registerComponentInformer(context.Background(), r); err != nil {
		t.Fatalf("registerComponentInformer() failed: %v", err)
	}
	if diff := cmp.Diff([]schema.GroupVersionKind{componentGVK}, r.gvks); diff != "" {
		t.Errorf("registered informers mismatch (-want +got):\n%s", diff)
	}
	if len(r.block) != 1 || r.block[0] == nil || *r.block[0] {
		t.Errorf("GetInformer BlockUntilSynced = %v, want false", r.block)
	}
}

// TestComponentManagerLeaderHandoff runs two leader-elected managers over one envtest namespace:
// only the holder of the ComponentLeaseName Lease reconciles, and stopping it hands the namespace
// over to the other without waiting for the Lease to expire. runControllerEnv sets a Lease
// duration longer than waitElected's timeout, so the takeover proves that the stopped leader
// released the Lease.
func TestComponentManagerLeaderHandoff(t *testing.T) {
	cfg := startComponentEnvtest(t)
	const namespace = "leader"
	createEnvtestNamespace(t, cfg, namespace)
	mgrCfg := ComponentManagerConfig{Namespace: namespace, LeaderElection: true}

	first := runControllerEnv(t, cfg, mgrCfg)
	waitElected(t, first, "first")
	firstHolder := waitLeaseHolder(t, cfg, namespace, "")
	second := runControllerEnv(t, cfg, mgrCfg)
	first.create(t, "le-app")
	waitReconciled(t, first, "le-app", "first")
	select {
	case <-second.mgr.Elected():
		t.Fatal("second manager was elected while the first holds the Lease")
	default:
	}
	if n := second.counter.snapshot()["le-app"]; n != 0 {
		t.Errorf("follower reconciled le-app %d times, want 0", n)
	}

	first.stop()
	waitElected(t, second, "second")
	waitReconciled(t, second, "le-app", "second")
	waitLeaseHolder(t, cfg, namespace, firstHolder)
}

// waitElected waits until env's manager holds the Lease.
func waitElected(t *testing.T, env *controllerEnv, desc string) {
	t.Helper()
	select {
	case <-env.mgr.Elected():
	case <-time.After(30 * time.Second):
		t.Fatalf("%s manager was not elected", desc)
	}
}

// waitReconciled waits until env's controller has reconciled the Component name.
func waitReconciled(t *testing.T, env *controllerEnv, name, desc string) {
	t.Helper()
	if err := wait.PollUntilContextTimeout(context.Background(), 50*time.Millisecond, 20*time.Second, true, func(context.Context) (bool, error) {
		return env.counter.snapshot()[name] > 0, nil
	}); err != nil {
		t.Fatalf("%s manager did not reconcile %s: %v", desc, name, err)
	}
}

// waitLeaseHolder waits until the ComponentLeaseName Lease of namespace has a holder other than
// previous and returns it.
func waitLeaseHolder(t *testing.T, cfg *rest.Config, namespace, previous string) string {
	t.Helper()
	leases := kubernetes.NewForConfigOrDie(cfg).CoordinationV1().Leases(namespace)
	var holder string
	if err := wait.PollUntilContextTimeout(context.Background(), 100*time.Millisecond, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		lease, err := leases.Get(ctx, ComponentLeaseName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		holder = ""
		if lease.Spec.HolderIdentity != nil {
			holder = *lease.Spec.HolderIdentity
		}
		return holder != "" && holder != previous, nil
	}); err != nil {
		t.Fatalf("Lease %s/%s holder did not change from %q: %v", namespace, ComponentLeaseName, previous, err)
	}
	return holder
}
