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
// Package clusterqueue implements a reconciler for ClusterQueue objects.
package clusterqueue

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/google/xmc/internal/logging"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	v1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
)

// NameCache holds list of ClusterQueue names.
type NameCache struct {
	rw    sync.RWMutex
	names map[string]struct{}
}

// NewNameCache creates a new NameCache.
func NewNameCache() *NameCache {
	return &NameCache{
		names: make(map[string]struct{}),
	}
}

// Add adds a ClusterQueue name to the cache.
func (c *NameCache) Add(name string) {
	c.rw.Lock()
	defer c.rw.Unlock()
	c.names[name] = struct{}{}
}

// Remove removes a ClusterQueue name from the cache.
func (c *NameCache) Remove(name string) {
	c.rw.Lock()
	defer c.rw.Unlock()
	delete(c.names, name)
}

// Names returns a list of ClusterQueue names.
func (c *NameCache) Names() []string {
	c.rw.RLock()
	defer c.rw.RUnlock()
	names := make([]string, 0, len(c.names))
	for name := range c.names {
		names = append(names, name)
	}
	return names
}

// Params holds dependencies for constructing a ClusterQueue Reconciler.
type Params struct {
	Client client.Client
	Scheme *runtime.Scheme
	Cache  *NameCache
}

// Reconciler reconciles a ClusterQueue object.
type Reconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Cache  *NameCache
}

// NewReconciler creates and validates a new ClusterQueue Reconciler.
func NewReconciler(p Params) (*Reconciler, error) {
	if p.Client == nil {
		return nil, errors.New("client is required")
	}
	if p.Scheme == nil {
		return nil, errors.New("scheme is required")
	}
	if p.Cache == nil {
		return nil, errors.New("cache is required")
	}
	return &Reconciler{
		Client: p.Client,
		Scheme: p.Scheme,
		Cache:  p.Cache,
	}, nil
}

// Reconcile will be called by the controller when a ClusterQueue is created or updated.
// It will add the ClusterQueue name to the cache.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logging.FromContext(ctx)
	var cq v1beta1.ClusterQueue
	if err := r.Get(logging.Strip(ctx), req.NamespacedName, &cq); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("ClusterQueue not found, assuming it was deleted", "clusterqueue", req.NamespacedName)
			r.Cache.Remove(req.Name)
			return ctrl.Result{}, nil
		}
		log.Error("Failed to get ClusterQueue", "error", err)
		return ctrl.Result{}, err
	}

	log.Info("ClusterQueue found", "clusterqueue", req.Name)
	r.Cache.Add(req.Name)

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	if r == nil {
		return errors.New("reconciler cannot be nil")
	}
	logging.Debug(ctx, "Setting up clusterqueue reconciler controller")
	err := ctrl.NewControllerManagedBy(mgr).
		Named("clusterqueue").
		For(&v1beta1.ClusterQueue{}, builder.WithPredicates(predicate.ResourceVersionChangedPredicate{})).
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create clusterqueue controller: %w", err)
	}
	return nil
}
