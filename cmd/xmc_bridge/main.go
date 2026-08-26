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

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/pkg/components/bridge"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	componentGVR = schema.GroupVersionResource{
		Group:    "xmc.dev",
		Version:  "v1alpha1",
		Resource: "components",
	}
)

// Config holds runtime configuration options for the xmc-bridge controller.
type Config struct {
	CMSAddress    string
	Namespace     string
	ProbeInterval time.Duration
	ProbeTimeout  time.Duration
	Kubeconfig    string
}

// ParseConfig parses command line flags into a validated Config object.
func ParseConfig(args []string) (*Config, error) {
	defaultCMS := os.Getenv("CMS_ADDRESS")
	if defaultCMS == "" {
		defaultCMS = "localhost:50051"
	}
	defaultNS := os.Getenv("NAMESPACE")
	if defaultNS == "" {
		defaultNS = "xmc"
	}
	defaultProbeInterval := 5 * time.Second
	if val := os.Getenv("PROBE_INTERVAL"); val != "" {
		if d, err := time.ParseDuration(val); err == nil {
			defaultProbeInterval = d
		}
	}
	defaultProbeTimeout := 2 * time.Second
	if val := os.Getenv("PROBE_TIMEOUT"); val != "" {
		if d, err := time.ParseDuration(val); err == nil {
			defaultProbeTimeout = d
		}
	}
	defaultKubeconfig := os.Getenv("KUBECONFIG")

	fs := flag.NewFlagSet("xmc-bridge", flag.ContinueOnError)
	cmsAddress := fs.String("cms-address", defaultCMS, "CMS gRPC server address")
	namespace := fs.String("namespace", defaultNS, "Kubernetes namespace to watch for Component resources")
	probeInterval := fs.Duration("probe-interval", defaultProbeInterval, "Interval duration between dynamic reconciliation sweeps")
	probeTimeout := fs.Duration("probe-timeout", defaultProbeTimeout, "Timeout duration for individual component probe and reconcile passes")
	kubeconfig := fs.String("kubeconfig", defaultKubeconfig, "Path to kubeconfig file")

	if err := fs.Parse(args); err != nil {
		return nil, fmt.Errorf("failed to parse command line flags: %w", err)
	}
	if *probeInterval <= 0 {
		return nil, fmt.Errorf("probe-interval must be a positive duration, got %v", *probeInterval)
	}
	if *probeTimeout <= 0 {
		return nil, fmt.Errorf("probe-timeout must be a positive duration, got %v", *probeTimeout)
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
		return clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	}
	cfg, err := rest.InClusterConfig()
	if err == nil {
		return cfg, nil
	}
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &clientcmd.ConfigOverrides{}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides).ClientConfig()
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
	for i := range list.Items {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		c.reconcileItem(ctx, &list.Items[i])
	}
	return nil
}

func (c *Controller) reconcileItem(ctx context.Context, item *unstructured.Unstructured) {
	name := item.GetName()
	reconcileCtx, cancel := context.WithTimeout(ctx, c.probeTimeout)
	defer cancel()

	res, err := c.reconciler.Reconcile(reconcileCtx, item)
	if res != nil {
		if syncErr := c.syncStatus(ctx, item, res); syncErr != nil {
			logging.Error(ctx, "Error syncing status for component", "name", name, "error", syncErr)
		}
	}
	if err != nil {
		logging.Error(ctx, "Error reconciling component", "name", name, "error", err)
		return
	}
	logging.Debug(ctx, "Item reconciliation pass finished", "name", name, "state", res.State.String(), "observedGeneration", res.ObservedGeneration)
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
				"status":             string(c.Status),
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
	reconciler, err := bridge.NewReconciler(componentspb.NewComponentManagerServiceClient(conn), nil, nil)
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
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		logging.Fatal(ctx, "Invalid configuration", "error", err)
	}

	if err := RunBridge(ctx, cfg); err != nil && !errors.Is(err, context.Canceled) {
		logging.Fatal(ctx, "Bridge Controller terminated unexpectedly", "error", err)
	}
	logging.Info(ctx, "Bridge Controller exited cleanly")
}
