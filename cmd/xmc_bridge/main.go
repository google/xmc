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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
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
	// CMSAddress is the gRPC endpoint address of the Component Manager Service.
	CMSAddress string
	// Namespace is the target Kubernetes namespace to watch and reconcile Component resources.
	Namespace string
	// ReleaseName is the Helm release name for 1P component actuation.
	ReleaseName string
	// ProbeInterval is the duration interval between periodic reconciliation sweeps.
	ProbeInterval time.Duration
	// ProbeTimeout is the timeout duration for individual component probe and reconciliation passes.
	ProbeTimeout time.Duration
	// Kubeconfig is the optional explicit path to a kubeconfig file.
	Kubeconfig string
	// ChartPath is the optional filesystem path to the Helm chart to actuate.
	ChartPath string
}

// ComponentStatus defines the structured representation of .status in the Component CRD.
type ComponentStatus struct {
	ObservedGeneration int64                `json:"observedGeneration"`
	State              string               `json:"state"`
	Access             *ComponentAccess     `json:"access,omitempty"`
	Conditions         []ComponentCondition `json:"conditions,omitempty"`
}

// ComponentAccess defines the access configuration in .status.access.
type ComponentAccess struct {
	EnvVars map[string]string `json:"envVars,omitempty"`
}

// ComponentCondition defines a condition entry matching standard RFC3339 timestamps.
type ComponentCondition struct {
	Type               string `json:"type"`
	Status             string `json:"status"`
	ObservedGeneration int64  `json:"observedGeneration"`
	Reason             string `json:"reason"`
	Message            string `json:"message"`
	LastTransitionTime string `json:"lastTransitionTime"`
}

// ParseConfig parses command line flags and environment variables into a validated Config object.
// args typically receives os.Args[1:]. Default values fall back to corresponding environment
// variables (CMS_ADDRESS, NAMESPACE, PROBE_INTERVAL, PROBE_TIMEOUT, KUBECONFIG) if flags are omitted.
// Returns an error if flag parsing fails or if intervals/timeouts are non-positive.
func ParseConfig(args []string) (*Config, error) {
	defaultCMS := os.Getenv("CMS_ADDRESS")
	defaultNS := os.Getenv("NAMESPACE")
	defaultReleaseName := os.Getenv("HELM_RELEASE_NAME")
	defaultProbeInterval := 5 * time.Second
	if val := os.Getenv("PROBE_INTERVAL"); val != "" {
		d, err := time.ParseDuration(val)
		if err != nil {
			return nil, fmt.Errorf("invalid PROBE_INTERVAL %q: %w", val, err)
		}
		defaultProbeInterval = d
	}
	defaultProbeTimeout := 2 * time.Second
	if val := os.Getenv("PROBE_TIMEOUT"); val != "" {
		d, err := time.ParseDuration(val)
		if err != nil {
			return nil, fmt.Errorf("invalid PROBE_TIMEOUT %q: %w", val, err)
		}
		defaultProbeTimeout = d
	}
	defaultKubeconfig := os.Getenv("KUBECONFIG")
	defaultChartPath := os.Getenv("HELM_CHART_PATH")

	fs := flag.NewFlagSet("xmc_bridge", flag.ContinueOnError)
	cmsAddress := fs.String("cms-address", defaultCMS, "CMS gRPC server address")
	namespace := fs.String("namespace", defaultNS, "Kubernetes namespace to watch for Component resources")
	releaseName := fs.String("release-name", defaultReleaseName, "Helm release name for 1P component actuation")
	probeInterval := fs.Duration("probe-interval", defaultProbeInterval, "Interval duration between dynamic reconciliation sweeps")
	probeTimeout := fs.Duration("probe-timeout", defaultProbeTimeout, "Timeout duration for individual component probe and reconcile passes")
	kubeconfig := fs.String("kubeconfig", defaultKubeconfig, "Path to kubeconfig file")
	chartPath := fs.String("chart-path", defaultChartPath, "Path to the Helm chart directory or tarball")

	if err := fs.Parse(args); err != nil {
		return nil, fmt.Errorf("failed to parse command line flags: %w", err)
	}
	if *cmsAddress == "" {
		return nil, errors.New("cms-address must be specified")
	}
	if *namespace == "" {
		return nil, errors.New("namespace must be specified")
	}
	if *releaseName == "" {
		return nil, errors.New("release-name must be specified")
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
		ReleaseName:   *releaseName,
		ProbeInterval: *probeInterval,
		ProbeTimeout:  *probeTimeout,
		Kubeconfig:    *kubeconfig,
		ChartPath:     *chartPath,
	}, nil
}

// BuildKubeConfig constructs a Kubernetes *rest.Config using the following resolution order:
// 1. Explicit kubeconfigPath (if non-empty).
// 2. In-cluster pod service account environment (rest.InClusterConfig).
// 3. Standard deferred clientcmd loading rules (KUBECONFIG environment variable or ~/.kube/config).
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

// Controller orchestrates dynamic polling and reconciliation sweeps across Kubernetes
// Component custom resources (xmc.dev/v1alpha1) within a specific namespace.
type Controller struct {
	dynamicClient dynamic.Interface
	reconciler    *bridge.Reconciler
	namespace     string
	probeInterval time.Duration
	probeTimeout  time.Duration
}

// NewController creates a new Controller instance.
// client is the dynamic Kubernetes client used to list and update Component custom resources.
// r is the core bridge Reconciler handling state synchronization and lifecycle logic.
// ns is the target namespace to reconcile.
// interval is the duration between periodic namespace reconciliation sweeps.
// timeout is the maximum duration allocated per component reconciliation pass.
func NewController(client dynamic.Interface, r *bridge.Reconciler, ns string, interval time.Duration, timeout time.Duration) *Controller {
	return &Controller{
		dynamicClient: client,
		reconciler:    r,
		namespace:     ns,
		probeInterval: interval,
		probeTimeout:  timeout,
	}
}

// Run executes an immediate initial reconciliation pass and then periodically triggers
// reconciliation sweeps on every ticker tick until ctx is cancelled.
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

// RunReconcilePass lists all Component resources in the configured namespace and executes
// reconciliation on each item sequentially, checking context cancellation between iterations.
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

// reconcileItem performs a bounded reconciliation pass on a single unstructured Component resource.
// It establishes a per-item timeout context (probeTimeout), calls Reconciler.Reconcile, and updates
// the custom resource's .status subresource with the resulting state and conditions.
func (c *Controller) reconcileItem(ctx context.Context, item *unstructured.Unstructured) {
	name := item.GetName()
	reconcileCtx, cancel := context.WithTimeout(ctx, c.probeTimeout)
	defer cancel()

	res, err := c.reconciler.Reconcile(reconcileCtx, item)
	if err != nil {
		logging.Error(ctx, "Error reconciling component", "name", name, "error", err)
		c.handleReconcileFailure(ctx, item, res)
		return
	}
	if res == nil {
		logging.Error(ctx, "Reconciler returned nil result without error", "name", name)
		return
	}
	updatedItem, err := c.syncFinalizers(ctx, item, res)
	if err != nil {
		// If syncFinalizers fails (e.g. API conflict or network blip), log and return.
		// Level-triggered reconciliation will automatically pick up the custom resource
		// on the subsequent pass: if detaching finalizer, the CR remains present in K8s
		// and deletion will be retried idempotently without leaking resources.
		logging.Error(ctx, "Error syncing finalizers for component", "name", name, "error", err)
		return
	}
	if res.RemoveFinalizer {
		return
	}
	if err := c.syncStatus(ctx, updatedItem, res); err != nil {
		logging.Error(ctx, "Error syncing status for component", "name", name, "error", err)
	}
	logging.Debug(ctx, "Item reconciliation pass finished", "name", name, "state", res.State.String(), "observedGeneration", res.ObservedGeneration)
}

// handleReconcileFailure handles status and finalizer synchronization when Reconcile returns an error
// but produced a non-nil ReconcileResult (e.g. Setting Degraded=True status or persisting finalizers).
func (c *Controller) handleReconcileFailure(ctx context.Context, item *unstructured.Unstructured, res *bridge.ReconcileResult) {
	if res == nil {
		return
	}
	name := item.GetName()
	updatedItem, syncErr := c.syncFinalizers(ctx, item, res)
	if syncErr != nil {
		logging.Error(ctx, "Error syncing finalizers after reconciliation failure", "name", name, "error", syncErr)
		return
	}
	if res.RemoveFinalizer {
		return
	}
	if err := c.syncStatus(ctx, updatedItem, res); err != nil {
		logging.Error(ctx, "Error syncing status after reconciliation failure", "name", name, "error", err)
	}
}

// syncFinalizers attaches the deletion protection finalizer if res.AddFinalizer is true,
// or removes it if res.RemoveFinalizer is true, updating the custom resource in Kubernetes.
// If the Kubernetes API update fails, the error is returned so that the controller can retry
// on the next pass while ensuring the finalizer remains in place to prevent premature deletion.
func (c *Controller) syncFinalizers(ctx context.Context, item *unstructured.Unstructured, res *bridge.ReconcileResult) (*unstructured.Unstructured, error) {
	if res == nil {
		return item, nil
	}
	hasFin := false
	for _, f := range item.GetFinalizers() {
		if f == bridge.FinalizerDeletionProtection {
			hasFin = true
			break
		}
	}
	ns := item.GetNamespace()
	if ns == "" {
		ns = c.namespace
	}
	if res.AddFinalizer && !hasFin {
		item.SetFinalizers(append(item.GetFinalizers(), bridge.FinalizerDeletionProtection))
		logging.Info(ctx, "Attaching deletion protection finalizer", "name", item.GetName())
		return c.dynamicClient.Resource(componentGVR).Namespace(ns).Update(ctx, item, metav1.UpdateOptions{})
	}
	if res.RemoveFinalizer && hasFin {
		var updated []string
		for _, f := range item.GetFinalizers() {
			if f != bridge.FinalizerDeletionProtection {
				updated = append(updated, f)
			}
		}
		item.SetFinalizers(updated)
		logging.Info(ctx, "Removing deletion protection finalizer", "name", item.GetName())
		return c.dynamicClient.Resource(componentGVR).Namespace(ns).Update(ctx, item, metav1.UpdateOptions{})
	}
	return item, nil
}

// syncStatus serializes the ReconcileResult into the .status map of item and persists it
// via the Kubernetes dynamic client UpdateStatus API. It uses a deep copy to avoid local
// object mutation if the API server update fails.
func (c *Controller) syncStatus(ctx context.Context, item *unstructured.Unstructured, res *bridge.ReconcileResult) error {
	ns := item.GetNamespace()
	if ns == "" {
		ns = c.namespace
	}
	itemCopy := item.DeepCopy()
	itemCopy.Object["status"] = BuildStatusMap(res)
	updated, err := c.dynamicClient.Resource(componentGVR).Namespace(ns).UpdateStatus(ctx, itemCopy, metav1.UpdateOptions{})
	if err != nil {
		return err
	}
	item.Object = updated.Object
	logging.Debug(ctx, "Updated Component status in Kubernetes", "name", item.GetName(), "state", res.State.String(), "observedGeneration", res.ObservedGeneration)
	return nil
}

// BuildStatus converts a ReconcileResult into a typed ComponentStatus matching the Component CRD status schema,
// including observedGeneration, state string, access envVars (if present), and standard RFC3339 condition list.
func BuildStatus(res *bridge.ReconcileResult) ComponentStatus {
	status := ComponentStatus{
		ObservedGeneration: res.ObservedGeneration,
		State:              res.State.String(),
	}
	if res.Access != nil && len(res.Access.EnvVars) > 0 {
		status.Access = &ComponentAccess{EnvVars: res.Access.EnvVars}
	}
	if len(res.Conditions) > 0 {
		status.Conditions = make([]ComponentCondition, 0, len(res.Conditions))
		for _, c := range res.Conditions {
			status.Conditions = append(status.Conditions, ComponentCondition{
				Type:               c.Type,
				Status:             string(c.Status),
				ObservedGeneration: c.ObservedGeneration,
				Reason:             c.Reason,
				Message:            c.Message,
				LastTransitionTime: c.LastTransitionTime.Format(time.RFC3339),
			})
		}
	}
	return status
}

// BuildStatusMap converts a ReconcileResult into an unstructured map matching the Component CRD status schema.
func BuildStatusMap(res *bridge.ReconcileResult) map[string]any {
	status := BuildStatus(res)
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&status)
	if err != nil {
		fallback := ComponentStatus{
			ObservedGeneration: res.ObservedGeneration,
			State:              res.State.String(),
		}
		fallbackMap, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(&fallback)
		return fallbackMap
	}
	return m
}

// RunBridge initializes all dependencies (CMS gRPC client, Kubernetes dynamic client,
// WorkloadManager, and Reconciler) and starts the Controller main loop.
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
	k8sClient, err := kubernetes.NewForConfig(k8sCfg)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}
	helmOpts := []bridge.HelmManagerOption{
		bridge.WithKubeconfig(cfg.Kubeconfig),
	}
	if cfg.ChartPath != "" {
		helmOpts = append(helmOpts, bridge.WithChartPath(cfg.ChartPath))
	}
	helmMgr := bridge.NewK8sHelmManager(k8sClient, cfg.Namespace, cfg.ReleaseName, helmOpts...)
	defer helmMgr.Close()
	reconciler, err := bridge.NewReconciler(componentspb.NewComponentManagerServiceClient(conn), nil, helmMgr)
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
