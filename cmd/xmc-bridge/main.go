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

// Package main implements the xmc-bridge controller binary for XMC Components.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/bridge"
)

// Config holds runtime configuration options for the xmc-bridge controller.
type Config struct {
	CMSAddress    string
	Namespace     string
	ProbeInterval time.Duration
	ProbeTimeout  time.Duration
	Kubeconfig    string
}

// ParseFlags extracts configuration from command line arguments.
func ParseFlags(args []string) (*Config, error) {
	fs := flag.NewFlagSet("xmc-bridge", flag.ContinueOnError)
	cfg := &Config{}

	fs.StringVar(&cfg.CMSAddress, "cms-address", "experiment-state-server:50051", "Address of the ComponentManagerService (CMS) gRPC endpoint.")
	fs.StringVar(&cfg.Namespace, "namespace", "xmc", "Kubernetes namespace to watch for Component CRs (empty watches all namespaces).")
	fs.DurationVar(&cfg.ProbeInterval, "probe-interval", 15*time.Second, "Periodic interval to re-reconcile and probe active components.")
	fs.DurationVar(&cfg.ProbeTimeout, "probe-timeout", 5*time.Second, "Timeout for health probes.")
	fs.StringVar(&cfg.Kubeconfig, "kubeconfig", "", "Path to kubeconfig file (optional, falls back to in-cluster or ~/.kube/config).")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	return cfg, nil
}

// BuildKubeConfig creates a Kubernetes rest.Config from explicit path, in-cluster env, or default home kubeconfig.
func BuildKubeConfig(kubeconfigPath string) (*rest.Config, error) {
	if kubeconfigPath != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	}
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}
	if home := homedir.HomeDir(); home != "" {
		kubePath := filepath.Join(home, ".kube", "config")
		if _, err := os.Stat(kubePath); err == nil {
			return clientcmd.BuildConfigFromFlags("", kubePath)
		}
	}
	return nil, errors.New("could not locate valid kubeconfig: not running in-cluster and no kubeconfig specified")
}

// Controller coordinates watching and reconciling Component CRs.
type Controller struct {
	dynClient     dynamic.Interface
	cmsClient     bridge.CMSClient
	reconciler    *bridge.Reconciler
	namespace     string
	probeInterval time.Duration
}

// NewController instantiates a new controller.
func NewController(
	dynClient dynamic.Interface,
	cmsClient bridge.CMSClient,
	reconciler *bridge.Reconciler,
	namespace string,
	probeInterval time.Duration,
) *Controller {
	if probeInterval <= 0 {
		probeInterval = 15 * time.Second
	}
	return &Controller{
		dynClient:     dynClient,
		cmsClient:     cmsClient,
		reconciler:    reconciler,
		namespace:     namespace,
		probeInterval: probeInterval,
	}
}

// Run executes the controller loop until the context is canceled.
func (c *Controller) Run(ctx context.Context) error {
	log.Printf("Starting xmc-bridge controller watching namespace %q (probeInterval: %v)", c.namespace, c.probeInterval)

	ticker := time.NewTicker(c.probeInterval)
	defer ticker.Stop()

	if err := c.ReconcileAll(ctx); err != nil {
		log.Printf("Warning: initial reconciliation sweep encountered errors: %v", err)
	}

	var watcher watch.Interface
	defer func() {
		if watcher != nil {
			watcher.Stop()
		}
	}()

	watcher, err := c.startWatcher(ctx)
	if err != nil {
		log.Printf("Warning: failed to start CR watcher, relying on periodic ticker: %v", err)
	}

	for {
		select {
		case <-ctx.Done():
			log.Printf("Shutting down xmc-bridge controller")
			return ctx.Err()

		case <-ticker.C:
			c.handlePeriodicTick(ctx, &watcher)

		case event, ok := <-c.watcherResultChan(watcher):
			if !ok {
				c.handleWatchChannelClosed(ctx, &watcher)
				continue
			}
			c.handleWatchEvent(ctx, &watcher, event)
		}
	}
}

func (c *Controller) handlePeriodicTick(ctx context.Context, watcher *watch.Interface) {
	if *watcher == nil {
		if w, err := c.startWatcher(ctx); err == nil {
			log.Printf("Successfully re-established CR watcher")
			*watcher = w
		}
	}
	if err := c.ReconcileAll(ctx); err != nil {
		log.Printf("Error during periodic reconciliation sweep: %v", err)
	}
}

func (c *Controller) handleWatchChannelClosed(ctx context.Context, watcher *watch.Interface) {
	log.Printf("CR watcher channel closed, resetting watcher...")
	if *watcher != nil {
		(*watcher).Stop()
		*watcher = nil
	}
	w, err := c.startWatcher(ctx)
	if err != nil {
		log.Printf("Warning: failed to restart CR watcher, will retry on next tick: %v", err)
		return
	}
	*watcher = w
	if err := c.ReconcileAll(ctx); err != nil {
		log.Printf("Error during re-sync reconciliation sweep: %v", err)
	}
}

func (c *Controller) handleWatchEvent(ctx context.Context, watcher *watch.Interface, event watch.Event) {
	switch event.Type {
	case watch.Added, watch.Modified:
		if obj, ok := event.Object.(*unstructured.Unstructured); ok {
			c.ReconcileObject(ctx, obj)
		}
	case watch.Error:
		log.Printf("Received watch.Error event: %v", event.Object)
		if *watcher != nil {
			(*watcher).Stop()
			*watcher = nil
		}
		if w, err := c.startWatcher(ctx); err == nil {
			*watcher = w
			_ = c.ReconcileAll(ctx)
		}
	}
}

func (c *Controller) watcherResultChan(w watch.Interface) <-chan watch.Event {
	if w == nil {
		return nil
	}
	return w.ResultChan()
}

func (c *Controller) startWatcher(ctx context.Context) (watch.Interface, error) {
	if c.dynClient == nil {
		return nil, errors.New("dynamic client is uninitialized")
	}
	if c.namespace != "" {
		return c.dynClient.Resource(bridge.ComponentGVR).Namespace(c.namespace).Watch(ctx, metav1.ListOptions{})
	}
	return c.dynClient.Resource(bridge.ComponentGVR).Watch(ctx, metav1.ListOptions{})
}

// ReconcileAll lists all Component CRs and reconciles each one.
func (c *Controller) ReconcileAll(ctx context.Context) error {
	if c.dynClient == nil {
		return nil
	}
	var list *unstructured.UnstructuredList
	var err error
	if c.namespace != "" {
		list, err = c.dynClient.Resource(bridge.ComponentGVR).Namespace(c.namespace).List(ctx, metav1.ListOptions{})
	} else {
		list, err = c.dynClient.Resource(bridge.ComponentGVR).List(ctx, metav1.ListOptions{})
	}
	if err != nil {
		return err
	}
	for i := range list.Items {
		c.ReconcileObject(ctx, &list.Items[i])
	}
	return nil
}

// ReconcileObject reconciles a single Component unstructured object.
func (c *Controller) ReconcileObject(ctx context.Context, obj *unstructured.Unstructured) {
	if obj == nil || c.reconciler == nil {
		return
	}
	res, err := c.reconciler.Reconcile(ctx, obj)
	if err != nil {
		log.Printf("Failed to reconcile component %s/%s: %v", obj.GetNamespace(), obj.GetName(), err)
		return
	}
	log.Printf("Reconciled component %s/%s: state=%s, observedGen=%d, conditions=%d",
		res.Namespace, res.Name, res.State, res.ObservedGeneration, len(res.Conditions))
}

func main() {
	cfg, err := ParseFlags(os.Args[1:])
	if err != nil {
		log.Fatalf("Failed to parse flags: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	k8sConfig, err := BuildKubeConfig(cfg.Kubeconfig)
	if err != nil {
		log.Fatalf("Failed to initialize Kubernetes config: %v", err)
	}

	dynClient, err := dynamic.NewForConfig(k8sConfig)
	if err != nil {
		log.Fatalf("Failed to create dynamic Kubernetes client: %v", err)
	}

	grpcConn, err := grpc.NewClient(
		cfg.CMSAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatalf("Failed to connect to CMS at %s: %v", cfg.CMSAddress, err)
	}
	defer grpcConn.Close()

	cmsClient := componentspb.NewComponentManagerServiceClient(grpcConn)
	prober := bridge.New(dynClient)
	reconciler, err := bridge.NewReconciler(
		cmsClient,
		prober,
		dynClient,
		bridge.WithReconcilerProbeTimeout(cfg.ProbeTimeout),
	)
	if err != nil {
		log.Fatalf("Failed to create reconciler: %v", err)
	}

	controller := NewController(dynClient, cmsClient, reconciler, cfg.Namespace, cfg.ProbeInterval)
	if err := controller.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("Controller run terminated with error: %v", err)
	}
}
