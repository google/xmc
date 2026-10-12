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
	"maps"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"xmanager_cloud/pkg/components/cms"
	"xmanager_cloud/pkg/components/store"
)

// envtestProbeInterval is long enough that no Component is re-probed during a test: every
// reconcile a test waits for must be triggered by a watch event.
const envtestProbeInterval = time.Hour

// reconcileCounter counts the reconciles of each Component name before delegating to inner.
type reconcileCounter struct {
	inner  reconcile.Reconciler
	mu     sync.Mutex
	counts map[string]int
}

func (c *reconcileCounter) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	c.mu.Lock()
	c.counts[req.Name]++
	c.mu.Unlock()
	return c.inner.Reconcile(ctx, req)
}

func (c *reconcileCounter) snapshot() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return maps.Clone(c.counts)
}

// waitSettled waits until the counts are unchanged over a 500ms poll interval, so that reconciles
// triggered by earlier writes have finished, and returns them.
func (c *reconcileCounter) waitSettled(t *testing.T) map[string]int {
	t.Helper()
	prev := c.snapshot()
	if err := wait.PollUntilContextTimeout(context.Background(), 500*time.Millisecond, 10*time.Second, false, func(context.Context) (bool, error) {
		cur := c.snapshot()
		settled := maps.Equal(cur, prev)
		prev = cur
		return settled, nil
	}); err != nil {
		t.Fatalf("reconcile counts did not settle: %v; last: %v", err, prev)
	}
	return prev
}

// apiserverReadCounter counts the Component GET and LIST requests, watches excluded, that a
// rest.Config returned by wrap sends to the API server.
type apiserverReadCounter struct {
	n atomic.Int64
}

func (c *apiserverReadCounter) wrap(cfg *rest.Config) *rest.Config {
	cfg = rest.CopyConfig(cfg)
	cfg.WrapTransport = func(rt http.RoundTripper) http.RoundTripper {
		return roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			isComponentPath := strings.Contains(req.URL.Path, "/"+ComponentGVR.Resource)
			if req.Method == http.MethodGet && isComponentPath && req.URL.Query().Get("watch") != "true" {
				c.n.Add(1)
			}
			return rt.RoundTrip(req)
		})
	}
	return cfg
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// controllerEnv is a running ComponentController over one envtest namespace.
type controllerEnv struct {
	namespace string
	client    dynamic.ResourceInterface
	counter   *reconcileCounter
	// reads counts the Component reads the manager sends to the API server.
	reads *apiserverReadCounter
}

// TestComponentControllerEnvtest runs the ComponentController in a controller-runtime manager
// against an envtest API server and an in-memory CMS, with re-probing effectively disabled.
func TestComponentControllerEnvtest(t *testing.T) {
	ctrl.SetLogger(testr.New(t))
	cfg := startComponentEnvtest(t)

	t.Run("DependencyReadinessActivatesDependentWithoutLoops", func(t *testing.T) {
		testDependencyReadinessActivatesDependent(t, startControllerEnv(t, cfg, "deps"))
	})
	t.Run("DeletionReleasesFinalizer", func(t *testing.T) {
		testDeletionReleasesFinalizer(t, startControllerEnv(t, cfg, "deletion"))
	})
	t.Run("ComponentReadsAreCacheBacked", func(t *testing.T) {
		testComponentReadsAreCacheBacked(t, startControllerEnv(t, cfg, "cache"))
	})
}

// startControllerEnv creates namespace and runs a manager with a counted ComponentController for
// it until the test ends.
func startControllerEnv(t *testing.T, cfg *rest.Config, namespace string) *controllerEnv {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	kubeClient := kubernetes.NewForConfigOrDie(cfg)
	if _, err := kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("failed to create namespace %q: %v", namespace, err)
	}
	opts := ComponentManagerOptions(namespace)
	opts.Controller.SkipNameValidation = ptr.To(true)
	reads := &apiserverReadCounter{}
	mgr, err := manager.New(reads.wrap(cfg), opts)
	if err != nil {
		t.Fatalf("manager.New() failed: %v", err)
	}
	writer := dynamic.NewForConfigOrDie(cfg)
	r, err := NewReconciler(NewCMSClientAdapter(cms.NewServer(store.NewMemoryStore())), nil, nil)
	if err != nil {
		t.Fatalf("NewReconciler() failed: %v", err)
	}
	c := NewComponentController(mgr.GetClient(), writer, r, ComponentControllerOptions{ProbeInterval: envtestProbeInterval, ReconcileTimeout: 10 * time.Second})
	counter := &reconcileCounter{inner: c, counts: map[string]int{}}
	if err := c.newControllerBuilder(mgr).Complete(counter); err != nil {
		t.Fatalf("failed to set up controller: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- mgr.Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("manager stopped with error: %v", err)
		}
	})
	return &controllerEnv{namespace: namespace, client: writer.Resource(ComponentGVR).Namespace(namespace), counter: counter, reads: reads}
}

// testComponentReadsAreCacheBacked checks that once the manager's cache has synced, neither
// Reconcile's Get and List nor the dependents watch's List reach the API server.
func testComponentReadsAreCacheBacked(t *testing.T, env *controllerEnv) {
	env.create(t, "env-cached-db")
	env.waitFor(t, "env-cached-db", "Ready", func(u *unstructured.Unstructured) bool { return readyReason(u) == ReasonComponentHealthy })
	before := env.reads.n.Load()

	env.create(t, "env-cached-app", "env-cached-db")
	env.waitFor(t, "env-cached-app", "Ready", func(u *unstructured.Unstructured) bool { return readyReason(u) == ReasonComponentHealthy })
	env.counter.waitSettled(t)

	if got := env.reads.n.Load() - before; got != 0 {
		t.Errorf("Component GET/LIST requests sent to the API server after the cache synced = %d, want 0", got)
	}
}

// testDependencyReadinessActivatesDependent creates a dependent before its dependency: the
// dependent must activate as soon as the dependency is Ready, through the dependents watch. A
// later pass of the dependent must not re-enqueue the dependency, which does not depend on it.
func testDependencyReadinessActivatesDependent(t *testing.T, env *controllerEnv) {
	ctx := context.Background()
	env.create(t, "env-app", "env-db")
	env.waitFor(t, "env-app", "gated", func(u *unstructured.Unstructured) bool {
		return readyReason(u) == ReasonDependenciesNotReady
	})
	env.create(t, "env-db")
	for _, name := range []string{"env-db", "env-app"} {
		env.waitFor(t, name, "Ready", func(u *unstructured.Unstructured) bool { return readyReason(u) == ReasonComponentHealthy })
	}

	before := env.counter.waitSettled(t)
	patch := []byte(`{"metadata":{"labels":{"xmc.dev/touched":"true"}}}`)
	if _, err := env.client.Patch(ctx, "env-app", types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
		t.Fatalf("failed to label env-app: %v", err)
	}
	if err := wait.PollUntilContextTimeout(ctx, 50*time.Millisecond, 10*time.Second, true, func(context.Context) (bool, error) {
		return env.counter.snapshot()["env-app"] > before["env-app"], nil
	}); err != nil {
		t.Fatalf("env-app was not reconciled after its label changed: %v", err)
	}
	// Give a hypothetical event loop time to show: any further reconcile fails the test.
	want := map[string]int{"env-app": before["env-app"] + 1, "env-db": before["env-db"]}
	err := wait.PollUntilContextTimeout(ctx, 50*time.Millisecond, 2*time.Second, true, func(context.Context) (bool, error) {
		after := env.counter.snapshot()
		return after["env-app"] != want["env-app"] || after["env-db"] != want["env-db"], nil
	})
	if err == nil {
		t.Errorf("reconciles after labeling env-app = %v, want %v (before: %v)", env.counter.snapshot(), want, before)
	}
}

// testDeletionReleasesFinalizer deletes an unprotected Component: the deletion event alone must
// drive the bridge to release its finalizer.
func testDeletionReleasesFinalizer(t *testing.T, env *controllerEnv) {
	ctx := context.Background()
	env.create(t, "env-del")
	env.waitFor(t, "env-del", "Ready with finalizer", func(u *unstructured.Unstructured) bool {
		return readyReason(u) == ReasonComponentHealthy && hasDeletionProtectionFinalizer(u.GetFinalizers())
	})
	if err := env.client.Delete(ctx, "env-del", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("failed to delete env-del: %v", err)
	}
	env.waitFor(t, "env-del", "gone", func(u *unstructured.Unstructured) bool { return u == nil })
}

// create creates an enabled, unprotected 1P Component name that depends on deps.
func (env *controllerEnv) create(t *testing.T, name string, deps ...string) {
	t.Helper()
	spec := map[string]any{"componentType": "COMPONENT_TYPE_CUSTOM_1P_COMPONENT", "enabled": true, "deletionProtection": false, "version": "1.0.0"}
	if len(deps) > 0 {
		spec["dependencies"] = deps
	}
	u := newTestComponent(name, env.namespace)
	u.Object["spec"] = spec
	if _, err := env.client.Create(context.Background(), u, metav1.CreateOptions{}); err != nil {
		t.Fatalf("failed to create Component %s: %v", name, err)
	}
}

// waitFor waits until cond holds for Component name, which is nil once it no longer exists.
func (env *controllerEnv) waitFor(t *testing.T, name, desc string, cond func(*unstructured.Unstructured) bool) {
	t.Helper()
	var last *unstructured.Unstructured
	err := wait.PollUntilContextTimeout(context.Background(), 100*time.Millisecond, 20*time.Second, true, func(ctx context.Context) (bool, error) {
		u, err := env.client.Get(ctx, name, metav1.GetOptions{})
		switch {
		case apierrors.IsNotFound(err):
			last = nil
		case err != nil:
			return false, nil
		default:
			last = u
		}
		return cond(last), nil
	})
	if err != nil {
		t.Fatalf("timed out waiting for Component %s/%s to be %s; last object: %v", env.namespace, name, desc, last)
	}
}

// readyReason returns the reason of the Ready condition of u, or "" if u is nil or has none.
func readyReason(u *unstructured.Unstructured) string {
	if u == nil {
		return ""
	}
	ready := findCondition(ParseConditionsFromUnstructured(u.Object["status"]), ConditionTypeReady)
	if ready == nil || ready.ObservedGeneration != u.GetGeneration() {
		return ""
	}
	return ready.Reason
}
