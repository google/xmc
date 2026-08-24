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

// Package main implements the entrypoint daemon for the XMC GitOps Bridge Controller.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/pkg/components/bridge"
)

var componentGVR = schema.GroupVersionResource{
	Group:    "xmc.dev",
	Version:  "v1alpha1",
	Resource: "components",
}

// Config holds CLI flags for the bridge controller daemon.
type Config struct {
	CMSAddress    string
	Namespace     string
	ProbeInterval time.Duration
	ProbeTimeout  time.Duration
	Kubeconfig    string
}

// ParseConfig parses command line flags from the given argument slice.
func ParseConfig(args []string) (*Config, error) {
	fs := flag.NewFlagSet("xmc-bridge", flag.ContinueOnError)
	cmsAddress := fs.String("cms-address", "localhost:50051", "gRPC address of the CMS")
	namespace := fs.String("namespace", "xmc", "Kubernetes namespace to watch")
	probeInterval := fs.Duration("probe-interval", 5*time.Second, "Interval between reconciliation passes")
	probeTimeout := fs.Duration("probe-timeout", 2*time.Second, "Timeout for health probes")
	kubeconfig := fs.String("kubeconfig", "", "Path to kubeconfig file")

	if err := fs.Parse(args); err != nil {
		return nil, fmt.Errorf("failed to parse flags: %w", err)
	}
	return &Config{
		CMSAddress:    *cmsAddress,
		Namespace:     *namespace,
		ProbeInterval: *probeInterval,
		ProbeTimeout:  *probeTimeout,
		Kubeconfig:    *kubeconfig,
	}, nil
}

// BuildKubeConfig builds a Kubernetes client config from a path or in-cluster environment.
func BuildKubeConfig(kubeconfigPath string) (*rest.Config, error) {
	if kubeconfigPath != "" {
		cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
		if err != nil {
			return nil, fmt.Errorf("failed to build kubeconfig from path %q: %w", kubeconfigPath, err)
		}
		return cfg, nil
	}
	if envPath := os.Getenv("KUBECONFIG"); envPath != "" {
		cfg, err := clientcmd.BuildConfigFromFlags("", envPath)
		if err != nil {
			return nil, fmt.Errorf("failed to build kubeconfig from KUBECONFIG env %q: %w", envPath, err)
		}
		return cfg, nil
	}
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to build in-cluster kubeconfig: %w", err)
	}
	return cfg, nil
}

// Controller orchestrates dynamic polling and reconciliation of Component custom resources.
type Controller struct {
	dynamicClient dynamic.Interface
	reconciler    *bridge.Reconciler
	namespace     string
	probeInterval time.Duration
	probeTimeout  time.Duration
}

// NewController creates a new Controller instance.
func NewController(client dynamic.Interface, r *bridge.Reconciler, ns string, interval time.Duration, timeout time.Duration) *Controller {
	return &Controller{
		dynamicClient: client,
		reconciler:    r,
		namespace:     ns,
		probeInterval: interval,
		probeTimeout:  timeout,
	}
}

// Run starts the reconciliation loop and blocks until context cancellation.
func (c *Controller) Run(ctx context.Context) error {
	logging.Info(ctx, "Starting XMC Bridge Controller", "namespace", c.namespace, "probeInterval", c.probeInterval, "probeTimeout", c.probeTimeout)
	ticker := time.NewTicker(c.probeInterval)
	defer ticker.Stop()

	if err := c.RunReconcilePass(ctx); err != nil {
		logging.Warn(ctx, "Initial reconciliation sweep failed", "error", err)
	}
	for {
		select {
		case <-ctx.Done():
			logging.Info(ctx, "Bridge Controller context cancelled, stopping run loop")
			return ctx.Err()
		case <-ticker.C:
			logging.Debug(ctx, "Triggering periodic reconciliation sweep", "namespace", c.namespace)
			if err := c.RunReconcilePass(ctx); err != nil {
				logging.Warn(ctx, "Periodic reconciliation sweep failed", "error", err)
			}
		}
	}
}

// RunReconcilePass executes a single sweep across all Component resources in the namespace.
func (c *Controller) RunReconcilePass(ctx context.Context) error {
	list, err := c.dynamicClient.Resource(componentGVR).Namespace(c.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list Component resources: %w", err)
	}
	logging.Debug(ctx, "Reconciliation pass found components", "namespace", c.namespace, "count", len(list.Items))
	for _, item := range list.Items {
		c.reconcileItem(ctx, &item)
	}
	return nil
}

func (c *Controller) reconcileItem(ctx context.Context, item *unstructured.Unstructured) {
	name := item.GetName()
	reconcileCtx, cancel := context.WithTimeout(ctx, c.probeTimeout)
	defer cancel()

	res, err := c.reconciler.Reconcile(reconcileCtx, item)
	if err != nil {
		logging.Error(ctx, "Error reconciling component", "name", name, "error", err)
		return
	}
	updatedItem, err := c.syncFinalizers(ctx, item, res)
	if err != nil {
		logging.Error(ctx, "Error syncing finalizers for component", "name", name, "error", err)
		return
	}
	if err := c.syncStatus(ctx, updatedItem, res); err != nil {
		logging.Error(ctx, "Error syncing status for component", "name", name, "error", err)
	}
	logging.Debug(ctx, "Item reconciliation pass finished", "name", name, "state", res.State.String(), "observedGeneration", res.ObservedGeneration)
}

func (c *Controller) syncFinalizers(ctx context.Context, item *unstructured.Unstructured, res *bridge.ReconcileResult) (*unstructured.Unstructured, error) {
	hasFin := false
	for _, f := range item.GetFinalizers() {
		if f == bridge.FinalizerDeletionProtection {
			hasFin = true
			break
		}
	}
	if res.AddFinalizer && !hasFin {
		item.SetFinalizers(append(item.GetFinalizers(), bridge.FinalizerDeletionProtection))
		logging.Info(ctx, "Attaching deletion protection finalizer", "name", item.GetName())
		return c.dynamicClient.Resource(componentGVR).Namespace(c.namespace).Update(ctx, item, metav1.UpdateOptions{})
	} else if res.RemoveFinalizer && hasFin {
		var updated []string
		for _, f := range item.GetFinalizers() {
			if f != bridge.FinalizerDeletionProtection {
				updated = append(updated, f)
			}
		}
		item.SetFinalizers(updated)
		logging.Info(ctx, "Removing deletion protection finalizer", "name", item.GetName())
		return c.dynamicClient.Resource(componentGVR).Namespace(c.namespace).Update(ctx, item, metav1.UpdateOptions{})
	}
	return item, nil
}

func (c *Controller) syncStatus(ctx context.Context, item *unstructured.Unstructured, res *bridge.ReconcileResult) error {
	item.Object["status"] = BuildStatusMap(res)
	_, err := c.dynamicClient.Resource(componentGVR).Namespace(c.namespace).UpdateStatus(ctx, item, metav1.UpdateOptions{})
	if err == nil {
		logging.Debug(ctx, "Updated Component status in Kubernetes", "name", item.GetName(), "state", res.State.String(), "observedGeneration", res.ObservedGeneration)
	}
	return err
}

// BuildStatusMap serializes a ReconcileResult into an unstructured status map.
func BuildStatusMap(res *bridge.ReconcileResult) map[string]any {
	statusMap := map[string]any{
		"observedGeneration": res.ObservedGeneration,
		"state":              res.State.String(),
	}
	if res.Access != nil && len(res.Access.EnvVars) > 0 {
		statusMap["access"] = map[string]any{"envVars": res.Access.EnvVars}
	}
	if len(res.Conditions) > 0 {
		var conds []any
		for _, c := range res.Conditions {
			conds = append(conds, map[string]any{
				"type":               c.Type,
				"status":             c.Status,
				"observedGeneration": c.ObservedGeneration,
				"reason":             c.Reason,
				"message":            c.Message,
				"lastTransitionTime": c.LastTransitionTime.Format(time.RFC3339),
			})
		}
		statusMap["conditions"] = conds
	}
	return statusMap
}

// RunBridge executes the complete Bridge daemon lifecycle using the specified configuration.
func RunBridge(ctx context.Context, cfg *Config) error {
	logging.Info(ctx, "Connecting to Component Manager Service (CMS)", "address", cfg.CMSAddress)
	conn, err := grpc.NewClient(cfg.CMSAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("failed to connect to CMS at %q: %w", cfg.CMSAddress, err)
	}
	defer conn.Close()

	logging.Info(ctx, "Configuring Kubernetes dynamic client", "kubeconfig", cfg.Kubeconfig)
	k8sCfg, err := BuildKubeConfig(cfg.Kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to build Kubernetes client config: %w", err)
	}
	dynamicClient, err := dynamic.NewForConfig(k8sCfg)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes dynamic client: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(k8sCfg)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes clientset: %w", err)
	}
	wm, err := bridge.NewK8sWorkloadManager(clientset)
	if err != nil {
		return fmt.Errorf("failed to create workload manager: %w", err)
	}
	reconciler, err := bridge.NewReconciler(componentspb.NewComponentManagerServiceClient(conn), nil, wm)
	if err != nil {
		return fmt.Errorf("failed to initialize Reconciler: %w", err)
	}
	return NewController(dynamicClient, reconciler, cfg.Namespace, cfg.ProbeInterval, cfg.ProbeTimeout).Run(ctx)
}

func main() {
	cfg, err := ParseConfig(os.Args[1:])
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	logger := logging.NewGCPLogger(slog.LevelInfo, "")
	ctx = logging.WithLogger(ctx, logger)

	if err != nil {
		logging.Fatal(ctx, "Invalid configuration", "error", err)
	}

	if err := RunBridge(ctx, cfg); err != nil && !errors.Is(err, context.Canceled) {
		logging.Fatal(ctx, "Bridge Controller terminated unexpectedly", "error", err)
	}
	logging.Info(ctx, "Bridge Controller exited cleanly")
}
