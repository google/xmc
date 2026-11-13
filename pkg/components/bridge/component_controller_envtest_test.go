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

// Controller-runtime envtest integration suite for ComponentController, ComponentManagerOptions and
// AddComponentHealthChecks.
// Production targets: component_controller.go and component_manager.go.
// Test type: hermetic Kubernetes control-plane integration test (runs against a real kube-apiserver
// and etcd started via sigs.k8s.io/controller-runtime/pkg/envtest). The pure unit tests live in
// component_controller_test.go and component_manager_test.go; the envtest harness defined here
// (runControllerEnv) is shared with the Lease handoff test in component_manager_test.go to test
// real informer cache wiring, watch predicates, Lease handoff, and Server-Side Apply.
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
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
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

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/cms"
	"xmanager_cloud/pkg/components/store"
	"xmanager_cloud/pkg/components/store/memory"
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

// intentObserver is a CMSClient that calls the hook set by setOnSet, if any, before delegating
// each SetComponentIntent.
type intentObserver struct {
	CMSClient
	mu    sync.Mutex
	onSet func(context.Context)
}

func (o *intentObserver) setOnSet(f func(context.Context)) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.onSet = f
}

func (o *intentObserver) SetComponentIntent(ctx context.Context, in *componentspb.SetComponentIntentRequest, opts ...grpc.CallOption) (*componentspb.SetComponentIntentResponse, error) {
	o.mu.Lock()
	onSet := o.onSet
	o.mu.Unlock()
	if onSet != nil {
		onSet(ctx)
	}
	return o.CMSClient.SetComponentIntent(ctx, in, opts...)
}

// epochOverrideCMS is a CMSClient that, once epoch is set, declares that epoch instead of the
// one in each DeclareClusterEpoch request and sends sourced requests with their source cluster at
// that epoch. It simulates an operator raising the bridge's ownership epoch without mutating the
// ClusterFence's immutable identity.
type epochOverrideCMS struct {
	CMSClient
	epoch atomic.Int64
}

func (o *epochOverrideCMS) DeclareClusterEpoch(ctx context.Context, in *componentspb.DeclareClusterEpochRequest, opts ...grpc.CallOption) (*componentspb.DeclareClusterEpochResponse, error) {
	if epoch := o.epoch.Load(); epoch > 0 {
		id := in.GetCluster()
		in = &componentspb.DeclareClusterEpochRequest{Cluster: &componentspb.ClusterIdentity{Uid: id.GetUid(), Name: id.GetName(), Epoch: epoch}}
	}
	return o.CMSClient.DeclareClusterEpoch(ctx, in, opts...)
}

func (o *epochOverrideCMS) SetComponentIntent(ctx context.Context, in *componentspb.SetComponentIntentRequest, opts ...grpc.CallOption) (*componentspb.SetComponentIntentResponse, error) {
	if src := o.sourceAtEpoch(in.GetSource()); src != in.GetSource() {
		in = proto.Clone(in).(*componentspb.SetComponentIntentRequest)
		in.Source = src
	}
	return o.CMSClient.SetComponentIntent(ctx, in, opts...)
}

func (o *epochOverrideCMS) UpdateObservedGeneration(ctx context.Context, in *componentspb.UpdateObservedGenerationRequest, opts ...grpc.CallOption) (*componentspb.UpdateObservedGenerationResponse, error) {
	if src := o.sourceAtEpoch(in.GetSource()); src != in.GetSource() {
		in = proto.Clone(in).(*componentspb.UpdateObservedGenerationRequest)
		in.Source = src
	}
	return o.CMSClient.UpdateObservedGeneration(ctx, in, opts...)
}

func (o *epochOverrideCMS) PrepareDeactivation(ctx context.Context, in *componentspb.PrepareDeactivationRequest, opts ...grpc.CallOption) (*componentspb.PrepareDeactivationResponse, error) {
	if src := o.sourceAtEpoch(in.GetSource()); src != in.GetSource() {
		in = proto.Clone(in).(*componentspb.PrepareDeactivationRequest)
		in.Source = src
	}
	return o.CMSClient.PrepareDeactivation(ctx, in, opts...)
}

func (o *epochOverrideCMS) CompleteDeactivation(ctx context.Context, in *componentspb.CompleteDeactivationRequest, opts ...grpc.CallOption) (*componentspb.CompleteDeactivationResponse, error) {
	if src := o.sourceAtEpoch(in.GetSource()); src != in.GetSource() {
		in = proto.Clone(in).(*componentspb.CompleteDeactivationRequest)
		in.Source = src
	}
	return o.CMSClient.CompleteDeactivation(ctx, in, opts...)
}

// sourceAtEpoch returns a copy of src with its cluster at the overriding epoch, or src itself
// when no epoch is set or src names no cluster.
func (o *epochOverrideCMS) sourceAtEpoch(src *componentspb.RequestSource) *componentspb.RequestSource {
	epoch := o.epoch.Load()
	if epoch <= 0 || src.GetCluster() == nil {
		return src
	}
	out := proto.Clone(src).(*componentspb.RequestSource)
	out.Cluster.Epoch = epoch
	return out
}

// controllerEnv is a running ComponentController over one envtest namespace.
type controllerEnv struct {
	namespace string
	client    dynamic.ResourceInterface
	counter   *reconcileCounter
	// reads counts the Component reads the manager sends to the API server.
	reads *apiserverReadCounter
	// cms is the CMS client of the controller, backed by store.
	cms   *intentObserver
	store store.Store
	fence *ClusterFence
	// fenceCMS is the CMS client of fence; nil when the env runs without a fence.
	fenceCMS *epochOverrideCMS
	mgr      manager.Manager
	// stop stops the manager and waits for it to exit. It is idempotent.
	stop func()
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
	t.Run("CreateDeleteRaceLeaksNothing", func(t *testing.T) {
		testCreateDeleteRaceLeaksNothing(t, startControllerEnv(t, cfg, "race"))
	})
	t.Run("ComponentReadsAreCacheBacked", func(t *testing.T) {
		testComponentReadsAreCacheBacked(t, startControllerEnv(t, cfg, "cache"))
	})
	t.Run("FencedStandbyAndTakeover", func(t *testing.T) {
		testFencedStandbyAndTakeover(t, cfg)
	})
	t.Run("SameClusterRecreateAdoptsCMSRecord", func(t *testing.T) {
		testSameClusterRecreateAdoptsCMSRecord(t, cfg)
	})
	t.Run("ConflictingCRDeletionPreservesOwner", func(t *testing.T) {
		testConflictingCRDeletionPreservesOwner(t, cfg)
	})
}

// startControllerEnv creates namespace and runs a manager with a counted ComponentController for
// it until the test ends.
func startControllerEnv(t *testing.T, cfg *rest.Config, namespace string) *controllerEnv {
	t.Helper()
	createEnvtestNamespace(t, cfg, namespace)
	return runControllerEnv(t, cfg, ComponentManagerConfig{Namespace: namespace})
}

func createEnvtestNamespace(t *testing.T, cfg *rest.Config, namespace string) {
	t.Helper()
	kubeClient := kubernetes.NewForConfigOrDie(cfg)
	if _, err := kubeClient.CoreV1().Namespaces().Create(context.Background(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("failed to create namespace %q: %v", namespace, err)
	}
}

// runControllerEnv runs a manager for mgrCfg with a counted ComponentController until it is
// stopped or the test ends.
func runControllerEnv(t *testing.T, cfg *rest.Config, mgrCfg ComponentManagerConfig) *controllerEnv {
	t.Helper()
	return runControllerEnvWithStore(t, cfg, mgrCfg, memory.NewMemoryStore(), nil)
}

func runControllerEnvWithStore(t *testing.T, cfg *rest.Config, mgrCfg ComponentManagerConfig, cmsStore store.Store, id *componentspb.ClusterIdentity) *controllerEnv {
	t.Helper()
	opts := ComponentManagerOptions(mgrCfg)
	opts.Controller.SkipNameValidation = ptr.To(true)
	if mgrCfg.LeaderElection {
		// Longer than every wait of these tests, so a takeover proves that the stopped leader
		// released the Lease instead of letting it expire.
		opts.LeaseDuration = ptr.To(60 * time.Second)
	}
	reads := &apiserverReadCounter{}
	mgr, err := manager.New(reads.wrap(cfg), opts)
	if err != nil {
		t.Fatalf("manager.New() failed: %v", err)
	}
	writer := dynamic.NewForConfigOrDie(cfg)
	cmsClient := &intentObserver{CMSClient: NewCMSClientAdapter(cms.NewServer(cmsStore, cms.WithAuthorizer(cms.NewDisabledAuthorizer())))}
	var recOpts []ReconcilerOption
	recOpts = append(recOpts, WithLiveUIDReader(NewAPIReaderLiveUID(mgr.GetAPIReader())))
	var recCMS CMSClient = cmsClient
	var fence *ClusterFence
	var fenceCMS *epochOverrideCMS
	if id != nil {
		fenceCMS = &epochOverrideCMS{CMSClient: cmsClient}
		recCMS = fenceCMS
		fence, err = NewClusterFence(fenceCMS, id, ClusterFenceOptions{HeartbeatInterval: time.Hour})
		if err != nil {
			t.Fatalf("NewClusterFence() failed: %v", err)
		}
		if err := mgr.Add(fence); err != nil {
			t.Fatalf("mgr.Add(fence) failed: %v", err)
		}
		recOpts = append(recOpts, WithClusterFence(fence))
	}
	r, err := NewReconciler(recCMS, nil, nil, recOpts...)
	if err != nil {
		t.Fatalf("NewReconciler() failed: %v", err)
	}
	c := NewComponentController(mgr.GetClient(), r, ComponentControllerOptions{Namespace: mgrCfg.Namespace, ProbeInterval: envtestProbeInterval, ReconcileTimeout: 10 * time.Second})
	counter := &reconcileCounter{inner: c, counts: map[string]int{}}
	if err := c.newControllerBuilder(mgr).Complete(counter); err != nil {
		t.Fatalf("failed to set up controller: %v", err)
	}
	if err := AddComponentHealthChecks(context.Background(), mgr); err != nil {
		t.Fatalf("AddComponentHealthChecks() failed: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- mgr.Start(ctx) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			if err := <-done; err != nil {
				t.Errorf("manager stopped with error: %v", err)
			}
		})
	}
	t.Cleanup(stop)
	return &controllerEnv{namespace: mgrCfg.Namespace, client: writer.Resource(ComponentGVR).Namespace(mgrCfg.Namespace), counter: counter, reads: reads, cms: cmsClient, store: cmsStore, fence: fence, fenceCMS: fenceCMS, mgr: mgr, stop: stop}
}

func testFencedStandbyAndTakeover(t *testing.T, cfg *rest.Config) {
	ctx := context.Background()
	const ns = "fenced"
	createEnvtestNamespace(t, cfg, ns)
	cmsStore := memory.NewMemoryStore()
	if _, err := cmsStore.DeclareClusterEpoch(ctx, store.ClusterIdentity{UID: testPrimaryClusterUID, Name: "primary", Epoch: 2}); err != nil {
		t.Fatalf("DeclareClusterEpoch(primary) failed: %v", err)
	}

	env := runControllerEnvWithStore(t, cfg, ComponentManagerConfig{Namespace: ns}, cmsStore, &componentspb.ClusterIdentity{
		Uid: testStandbyClusterUID, Name: "standby", Epoch: 2,
	})
	env.create(t, "env-fenced-db")
	env.waitFor(t, "env-fenced-db", "fenced SourceConflict", func(u *unstructured.Unstructured) bool {
		if u == nil {
			return false
		}
		conds := ParseConditionsFromUnstructured(u.Object["status"])
		sc := findCondition(conds, ConditionTypeSourceConflict)
		return readyReason(u) == ReasonSourceConflict && sc != nil && sc.Status == ConditionStatusTrue && sc.Reason == ReasonClusterFenced
	})
	env.assertNoCMSRecords(t)

	// Take over the CMS fence by declaring the standby at epoch 3 (without mutating the fence's
	// immutable identity): re-declaring signals fence.Changes() and reconciles env-fenced-db to
	// Ready without touching the CR.
	env.fenceCMS.epoch.Store(3)
	if err := env.fence.Declare(ctx); err != nil {
		t.Fatalf("fence.Declare(epoch 3) failed: %v", err)
	}
	env.waitFor(t, "env-fenced-db", "Ready with SourceConflict pruned", func(u *unstructured.Unstructured) bool {
		if u == nil {
			return false
		}
		conds := ParseConditionsFromUnstructured(u.Object["status"])
		return readyReason(u) == ReasonComponentHealthy && findCondition(conds, ConditionTypeSourceConflict) == nil
	})
}

func testSameClusterRecreateAdoptsCMSRecord(t *testing.T, cfg *rest.Config) {
	ctx := context.Background()
	const (
		ns     = "recreate"
		oldUID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	)
	createEnvtestNamespace(t, cfg, ns)
	cmsStore := memory.NewMemoryStore()
	if _, err := cmsStore.DeclareClusterEpoch(ctx, store.ClusterIdentity{UID: testPrimaryClusterUID, Name: "primary", Epoch: 1}); err != nil {
		t.Fatalf("DeclareClusterEpoch() failed: %v", err)
	}
	// Pre-seed CMS with an active record owned by an earlier CR incarnation (oldUID) in the same
	// cluster and namespace at generation 3, simulating a CR recreated while its CMS record remained.
	var seededID int64
	for i, ver := range []string{"1.0.1", "1.0.2", "1.0.3"} {
		gen := int64(i + 1)
		rec, err := cmsStore.SetSourcedComponentIntent(ctx, "env-recreate-db", &componentspb.ComponentSpec{
			Type:           componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
			Version:        ver,
			Stability:      componentspb.ComponentStability_COMPONENT_STABILITY_STABLE,
			DeletionPolicy: componentspb.DeletionPolicy_DELETION_POLICY_DELETE,
		}, nil, store.RequestSource{
			UID: oldUID, Generation: gen, Namespace: ns, ClusterUID: testPrimaryClusterUID, Epoch: 1,
		})
		if err != nil {
			t.Fatalf("SetSourcedComponentIntent(gen %d) failed: %v", gen, err)
		}
		seededID = rec.ComponentID
	}

	env := runControllerEnvWithStore(t, cfg, ComponentManagerConfig{Namespace: ns}, cmsStore, &componentspb.ClusterIdentity{
		Uid: testPrimaryClusterUID, Name: "primary", Epoch: 1,
	})
	env.createSpec(t, "env-recreate-db", map[string]any{
		"componentType":      "COMPONENT_TYPE_CUSTOM_1P_COMPONENT",
		"enabled":            true,
		"deletionProtection": false,
		"version":            "1.0.3",
	})
	var liveUID string
	env.waitFor(t, "env-recreate-db", "Ready with componentGeneration 3", func(u *unstructured.Unstructured) bool {
		if u == nil {
			return false
		}
		liveUID = string(u.GetUID())
		return readyReason(u) == ReasonComponentHealthy && ParseComponentGenerationFromUnstructured(u.Object["status"]) == 3
	})
	rec, err := cmsStore.GetComponent(ctx, "env-recreate-db")
	if err != nil {
		t.Fatalf("GetComponent() failed: %v", err)
	}
	if rec.ComponentID != seededID || rec.Owner == nil || rec.Owner.UID != liveUID {
		t.Errorf("CMS record after recreate = (id=%d, owner=%+v), want id=%d and owner UID %q", rec.ComponentID, rec.Owner, seededID, liveUID)
	}
}

func testConflictingCRDeletionPreservesOwner(t *testing.T, cfg *rest.Config) {
	ctx := context.Background()
	const nsOwner, nsDupe, name = "del-owner", "del-dupe", "env-shared-comp"
	createEnvtestNamespace(t, cfg, nsOwner)
	createEnvtestNamespace(t, cfg, nsDupe)
	cmsStore := memory.NewMemoryStore()
	clusterID := &componentspb.ClusterIdentity{Uid: testPrimaryClusterUID, Name: "primary", Epoch: 1}

	envOwner := runControllerEnvWithStore(t, cfg, ComponentManagerConfig{Namespace: nsOwner}, cmsStore, clusterID)
	envOwner.create(t, name)
	envOwner.waitFor(t, name, "Ready in owner namespace", func(u *unstructured.Unstructured) bool {
		return readyReason(u) == ReasonComponentHealthy
	})

	envDupe := runControllerEnvWithStore(t, cfg, ComponentManagerConfig{Namespace: nsDupe}, cmsStore, clusterID)
	envDupe.create(t, name)
	envDupe.waitFor(t, name, "SourceNamespaceConflict in duplicate namespace", func(u *unstructured.Unstructured) bool {
		return readyReason(u) == ReasonSourceConflict
	})

	// Deleting the conflicting CR in nsDupe must release its finalizer, record a Warning Event,
	// and preserve the active CMS record owned by nsOwner.
	if err := envDupe.client.Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		t.Fatalf("failed to delete conflicting Component in %s: %v", nsDupe, err)
	}
	envDupe.waitFor(t, name, "gone from duplicate namespace", func(u *unstructured.Unstructured) bool { return u == nil })

	if _, err := cmsStore.GetComponent(ctx, name); err != nil {
		t.Fatalf("expected CMS record %q to remain active after conflicting CR deletion, got: %v", name, err)
	}
	kubeClient := kubernetes.NewForConfigOrDie(cfg)
	events, err := kubeClient.CoreV1().Events(nsDupe).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("failed to list Events in %s: %v", nsDupe, err)
	}
	foundWarning := false
	for _, ev := range events.Items {
		if ev.Type == corev1.EventTypeWarning && ev.Reason == ReasonStaleDeletion && strings.Contains(ev.Message, "Skipped CMS deletion") {
			foundWarning = true
			break
		}
	}
	if !foundWarning {
		t.Errorf("expected Warning Event with reason %q in %s, got %+v", ReasonStaleDeletion, nsDupe, events.Items)
	}

	// Deleting the active owner CR in nsOwner must delete the CMS record and release its finalizer.
	if err := envOwner.client.Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		t.Fatalf("failed to delete owner Component in %s: %v", nsOwner, err)
	}
	envOwner.waitFor(t, name, "gone from owner namespace", func(u *unstructured.Unstructured) bool { return u == nil })
	envOwner.assertNoCMSRecords(t)
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

// testDeletionReleasesFinalizer deletes Components with and without deletionProtection: the
// deletion event alone must drive the bridge to clean up CMS and release its finalizer. Admission
// policy, absent from envtest, is the only veto on protected deletions, so a deletion that reaches
// the bridge, such as one issued by the namespace controller, is always carried out.
func testDeletionReleasesFinalizer(t *testing.T, env *controllerEnv) {
	ctx := context.Background()
	for _, tc := range []struct {
		name      string
		protected bool
	}{{"env-del", false}, {"env-del-protected", true}} {
		name := tc.name
		env.createSpec(t, name, map[string]any{"componentType": "COMPONENT_TYPE_CUSTOM_1P_COMPONENT", "enabled": true, "deletionProtection": tc.protected, "version": "1.0.0"})
		env.waitFor(t, name, "Ready with finalizer", func(u *unstructured.Unstructured) bool {
			return readyReason(u) == ReasonComponentHealthy && HasCleanupFinalizer(u)
		})
		if err := env.client.Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
			t.Fatalf("failed to delete %s: %v", name, err)
		}
		env.waitFor(t, name, "gone", func(u *unstructured.Unstructured) bool { return u == nil })
	}
	env.assertNoCMSRecords(t)
}

// testCreateDeleteRaceLeaksNothing deletes a Component from inside the first CMS intent write for
// it, the latest point at which a deletion can race the bridge's first side effect. That write
// must already be guarded by the stored cleanup finalizer, and the deletion must then wait for the
// CMS cleanup: a foreign finalizer keeps the Component around after the bridge releases its own,
// which must not happen before the CMS record is gone.
func testCreateDeleteRaceLeaksNothing(t *testing.T, env *controllerEnv) {
	ctx := context.Background()
	const name, hold = "env-race", "test.xmc.dev/hold"
	var calls, unguarded, unheld atomic.Int32
	env.cms.setOnSet(func(ctx context.Context) {
		if u, err := env.client.Get(ctx, name, metav1.GetOptions{}); err != nil || !HasCleanupFinalizer(u) {
			unguarded.Add(1)
		}
		if calls.Add(1) != 1 {
			return
		}
		if err := env.client.Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
			t.Errorf("failed to delete %s during its first CMS intent write: %v", name, err)
		}
		if u, err := env.client.Get(ctx, name, metav1.GetOptions{}); err != nil || u.GetDeletionTimestamp() == nil || !HasCleanupFinalizer(u) {
			unheld.Add(1)
		}
	})
	env.createSpec(t, name, map[string]any{"componentType": "COMPONENT_TYPE_CUSTOM_1P_COMPONENT", "enabled": true, "deletionProtection": false, "version": "1.0.0"}, hold)
	env.waitFor(t, name, "Terminating without the cleanup finalizer", func(u *unstructured.Unstructured) bool {
		return u != nil && u.GetDeletionTimestamp() != nil && !HasCleanupFinalizer(u)
	})
	env.assertNoCMSRecords(t)

	if _, err := env.client.Patch(ctx, name, types.MergePatchType, []byte(`{"metadata":{"finalizers":null}}`), metav1.PatchOptions{}); err != nil {
		t.Fatalf("failed to strip the %s finalizer: %v", hold, err)
	}
	env.waitFor(t, name, "gone", func(u *unstructured.Unstructured) bool { return u == nil })
	env.counter.waitSettled(t)

	if calls.Load() == 0 {
		t.Error("no CMS intent write observed, want the deletion to race the first one")
	}
	if got := unguarded.Load(); got != 0 {
		t.Errorf("CMS intent writes without the stored cleanup finalizer = %d, want 0", got)
	}
	if got := unheld.Load(); got != 0 {
		t.Errorf("deletions not held by the cleanup finalizer = %d, want 0", got)
	}
	env.assertNoCMSRecords(t)
}

// assertNoCMSRecords checks that the CMS of env holds no live Component record.
func (env *controllerEnv) assertNoCMSRecords(t *testing.T) {
	t.Helper()
	recs, err := env.store.ListComponents(context.Background())
	if err != nil {
		t.Fatalf("ListComponents() failed: %v", err)
	}
	for _, rec := range recs {
		t.Errorf("CMS record %q (state %v) outlived its Component", rec.Name, rec.State)
	}
}

// create creates an enabled, unprotected 1P Component name that depends on deps.
func (env *controllerEnv) create(t *testing.T, name string, deps ...string) {
	t.Helper()
	spec := map[string]any{"componentType": "COMPONENT_TYPE_CUSTOM_1P_COMPONENT", "enabled": true, "deletionProtection": false, "version": "1.0.0"}
	if len(deps) > 0 {
		spec["dependencies"] = deps
	}
	env.createSpec(t, name, spec)
}

// createSpec creates Component name with spec and finalizers.
func (env *controllerEnv) createSpec(t *testing.T, name string, spec map[string]any, finalizers ...string) {
	t.Helper()
	clonedSpec := make(map[string]any, len(spec)+1)
	for k, v := range spec {
		clonedSpec[k] = v
	}
	if _, ok := clonedSpec["stability"]; !ok {
		clonedSpec["stability"] = "STABLE"
	}
	u := newTestComponent(name, env.namespace, finalizers...)
	u.Object["spec"] = clonedSpec
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
			//nolint:nilerr // Transient Get errors inside wait.PollUntilContextTimeout retry until deadline.
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
