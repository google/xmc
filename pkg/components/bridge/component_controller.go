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
	"errors"
	"fmt"
	"slices"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"xmanager_cloud/internal/logging"
	"xmanager_cloud/pkg/components/naming"
)

// componentControllerName names the controller in logs and metrics.
const componentControllerName = "xmc-bridge-component"

var componentGVK = ComponentGVR.GroupVersion().WithKind("Component")

// ComponentControllerOptions configures a ComponentController.
type ComponentControllerOptions struct {
	// ProbeInterval is how long after a successful pass a Component is reconciled again, so that
	// workload health changes, which emit no Component event, are still observed.
	ProbeInterval time.Duration
	// ReconcileTimeout bounds the Reconciler pass of a single Component; persisting its result is
	// not bounded by it.
	ReconcileTimeout time.Duration
	// MaxConcurrentReconciles is the number of Components reconciled in parallel. Values below 1
	// mean 1.
	MaxConcurrentReconciles int
}

// ComponentController runs Reconciler as a controller-runtime controller over the Component CRs of
// the namespace its manager caches. A Component is reconciled when its spec, labels or annotations
// change, when its deletion starts, when the readiness of a Component it depends on changes, and
// after every successful pass: after the pass's explicit RequeueAfter when it sets one (30s while
// deactivation is blocked, the 5m CMS heartbeat once disabled), otherwise after ProbeInterval. A
// failed pass is retried with NewComponentRateLimiter backoff.
type ComponentController struct {
	client     client.Client
	reconciler *Reconciler
	opts       ComponentControllerOptions
}

// NewComponentController returns a ComponentController that reads and writes Components through
// c and runs r. c is normally the client of a manager built with ComponentManagerOptions, which
// serves the reads from the manager's informer cache.
func NewComponentController(c client.Client, r *Reconciler, opts ComponentControllerOptions) *ComponentController {
	return &ComponentController{client: c, reconciler: r, opts: opts}
}

// SetupWithManager registers the controller and its Component watches with mgr.
func (c *ComponentController) SetupWithManager(mgr manager.Manager) error {
	return c.newControllerBuilder(mgr).Complete(c)
}

func (c *ComponentController) newControllerBuilder(mgr manager.Manager) *builder.Builder {
	ownChanges := predicate.Or[client.Object](
		predicate.GenerationChangedPredicate{},
		predicate.LabelChangedPredicate{},
		predicate.AnnotationChangedPredicate{},
		deletionStarted(),
	)
	return builder.ControllerManagedBy(mgr).
		Named(componentControllerName).
		For(newComponentObject(), builder.WithPredicates(ownChanges)).
		Watches(newComponentObject(), handler.EnqueueRequestsFromMapFunc(c.mapDependents), builder.WithPredicates(dependencyReadinessChanged())).
		WithOptions(controller.Options{MaxConcurrentReconciles: c.opts.MaxConcurrentReconciles, RateLimiter: NewComponentRateLimiter()})
}

// Reconcile runs one Reconciler pass for the Component named by req, gated on a dependency
// snapshot of its namespace, and persists the result, also when the pass failed. It returns an
// error, and so a backoff retry, if the pass or the persistence failed.
func (c *ComponentController) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	item := newComponentObject()
	if err := c.client.Get(ctx, req.NamespacedName, item); err != nil {
		if apierrors.IsNotFound(err) {
			c.reconciler.ForgetComponent(req.Namespace, req.Name)
		}
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	// A rejected finalizer patch, typically a 422 because the cached Component is stale, is
	// retried with backoff like any other failure: a later pass reads the newer copy, and a
	// persistent rejection keeps surfacing as an error instead of being retried silently.
	item, err := c.ensureCleanupFinalizer(ctx, item)
	if item == nil || err != nil {
		return reconcile.Result{}, err
	}
	items, err := c.listComponents(ctx, req.Namespace)
	if err != nil {
		return reconcile.Result{}, err
	}
	res, passErr := c.runReconciler(ctx, item, NewDependencySnapshot(items))
	persistErr := PersistReconcileResult(ctx, c.client, item, res)
	if err := joinPassErrors(passErr, persistErr); err != nil {
		return reconcile.Result{}, err
	}
	if res == nil {
		logging.Warn(ctx, "Reconciler returned no result; re-probing after the probe interval", "namespace", req.Namespace, "name", req.Name)
	}
	if res != nil && res.RemoveFinalizer {
		return reconcile.Result{}, nil
	}
	return reconcile.Result{RequeueAfter: c.requeueDelay(res)}, nil
}

// requeueDelay returns the pass's explicit RequeueAfter when set, which is always honored, longer
// or shorter than ProbeInterval (e.g. the 30s DeactivationBlocked recheck or the 5m heartbeat of a
// disabled component); otherwise it returns ProbeInterval.
func (c *ComponentController) requeueDelay(res *ReconcileResult) time.Duration {
	if res != nil && res.RequeueAfter > 0 {
		return res.RequeueAfter
	}
	return c.opts.ProbeInterval
}

// ensureCleanupFinalizer returns item once FinalizerCMSCleanup is persisted on it, attaching the
// finalizer first if needed, so that no CMS or workload side effect runs before deletion is
// guaranteed to clean it up. It returns nil if there is nothing to reconcile: item no longer
// exists, or it is being deleted without the finalizer, so no side effect ever ran for it.
func (c *ComponentController) ensureCleanupFinalizer(ctx context.Context, item *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	if HasCleanupFinalizer(item) {
		return item, nil
	}
	if item.GetDeletionTimestamp() != nil {
		c.reconciler.ForgetComponent(item.GetNamespace(), item.GetName())
		return nil, nil
	}
	updated, err := AddCleanupFinalizer(ctx, c.client, item)
	if apierrors.IsNotFound(err) {
		c.reconciler.ForgetComponent(item.GetNamespace(), item.GetName())
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to attach finalizer: %w", err)
	}
	return updated, nil
}

func (c *ComponentController) runReconciler(ctx context.Context, item *unstructured.Unstructured, deps DependencyReadiness) (*ReconcileResult, error) {
	ctx, cancel := context.WithTimeout(ctx, c.opts.ReconcileTimeout)
	defer cancel()
	return c.reconciler.ReconcileWithDependencies(ctx, item, deps)
}

// joinPassErrors returns the error of a Reconciler pass and the error of persisting its result,
// joined only when both are set, so that a single failure is returned unwrapped.
func joinPassErrors(passErr, persistErr error) error {
	if passErr == nil {
		return persistErr
	}
	if persistErr == nil {
		return passErr
	}
	return errors.Join(passErr, persistErr)
}

// mapDependents returns a request for every other Component in obj's namespace that lists obj in
// spec.dependencies.
func (c *ComponentController) mapDependents(ctx context.Context, obj client.Object) []reconcile.Request {
	items, err := c.listComponents(ctx, obj.GetNamespace())
	if err != nil {
		logging.Error(ctx, "Failed to list Components to enqueue dependents", "name", obj.GetName(), "error", err)
		return nil
	}
	dep := naming.CanonicalComponentName(obj.GetName())
	var reqs []reconcile.Request
	for i := range items {
		spec, _, _ := ParseSpecFromUnstructured(items[i].Object["spec"])
		if items[i].GetName() != obj.GetName() && slices.Contains(spec.GetDependencies(), dep) {
			reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&items[i])})
		}
	}
	return reqs
}

func (c *ComponentController) listComponents(ctx context.Context, namespace string) ([]unstructured.Unstructured, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(ComponentGVR.GroupVersion().WithKind("ComponentList"))
	if err := c.client.List(ctx, list, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("failed to list Components in namespace %q: %w", namespace, err)
	}
	return list.Items, nil
}

func newComponentObject() *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(componentGVK)
	return u
}

// deletionStarted passes the update that sets a Component's deletionTimestamp.
func deletionStarted() predicate.Predicate {
	return predicate.Funcs{UpdateFunc: func(e event.UpdateEvent) bool {
		return e.ObjectOld.GetDeletionTimestamp() == nil && e.ObjectNew.GetDeletionTimestamp() != nil
	}}
}

// dependencyReadinessChanged passes the Component events that can change what its dependents see:
// creation, deletion, and updates that change its generation or its readiness as a dependency.
// Status writes that keep its readiness, such as condition timestamp refreshes, do not pass, so
// they never re-enqueue its dependents.
func dependencyReadinessChanged() predicate.Predicate {
	return predicate.Funcs{UpdateFunc: func(e event.UpdateEvent) bool {
		return e.ObjectOld.GetGeneration() != e.ObjectNew.GetGeneration() || dependencyUnreadyReason(e.ObjectOld) != dependencyUnreadyReason(e.ObjectNew)
	}}
}

// dependencyUnreadyReason returns why obj cannot be depended upon, as DependencySnapshot reports it,
// or "" if it can.
func dependencyUnreadyReason(obj client.Object) string {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok || u == nil {
		return "not a Component"
	}
	_, _, enabled := ParseSpecFromUnstructured(u.Object["spec"])
	return componentUnreadyReason(u, enabled)
}
