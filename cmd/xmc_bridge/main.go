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
	"strconv"
	"syscall"
	"time"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/pkg/components/bridge"

	"github.com/go-logr/logr"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"k8s.io/client-go/discovery"
	memory "k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
)

// Config holds runtime configuration options for the xmc-bridge controller.
type Config struct {
	// CMSAddress is the gRPC endpoint address of the Component Manager Service.
	CMSAddress string
	// Namespace is the target Kubernetes namespace to watch and reconcile Component resources.
	Namespace string
	// ReleaseName is the Helm release name for 1P component actuation.
	ReleaseName string
	// ProbeInterval is how long after a successful reconcile pass a Component is re-probed.
	ProbeInterval time.Duration
	// ProbeTimeout is the timeout duration for an individual component health probe.
	ProbeTimeout time.Duration
	// ReconcileTimeout bounds one Component reconcile pass, including CMS calls and actuation.
	ReconcileTimeout time.Duration
	// MaxConcurrentReconciles is the number of Components reconciled in parallel.
	MaxConcurrentReconciles int
	// MetricsBindAddress is the address of the metrics endpoint; "0" disables it. The endpoint
	// has no authentication, so it defaults to a loopback address.
	MetricsBindAddress string
	// HealthProbeBindAddress is the address of the /healthz and /readyz endpoints.
	HealthProbeBindAddress string
	// Kubeconfig is the optional explicit path to a kubeconfig file.
	Kubeconfig string
	// ChartPath is the base directory containing per-component Helm charts (e.g. <chart-path>/components/<name>)
	// or a single fallback Helm chart directory/tarball.
	ChartPath string
}

// ComponentStatus defines the structured representation of .status in the Component CRD.
type ComponentStatus = bridge.ComponentStatus

// ComponentAccess defines the access configuration in .status.access.
type ComponentAccess = bridge.ComponentAccess

// ComponentCondition defines a condition entry matching standard RFC3339 timestamps.
type ComponentCondition = bridge.ComponentCondition

// ParseConfig parses command line flags and environment variables into a validated Config object.
// args typically receives os.Args[1:]. Default values fall back to corresponding environment
// variables (CMS_ADDRESS, NAMESPACE, PROBE_INTERVAL, PROBE_TIMEOUT, RECONCILE_TIMEOUT,
// MAX_CONCURRENT_RECONCILES, KUBECONFIG) if flags are omitted.
// Returns an error if flag parsing fails, if intervals/timeouts are non-positive, if
// max-concurrent-reconciles is below 1 or if health-probe-bind-address is empty.
func ParseConfig(args []string) (*Config, error) {
	defaultCMS := os.Getenv("CMS_ADDRESS")
	defaultNS := os.Getenv("NAMESPACE")
	defaultReleaseName := os.Getenv("HELM_RELEASE_NAME")
	defaultProbeInterval, err := durationFromEnv("PROBE_INTERVAL", 5*time.Second)
	if err != nil {
		return nil, err
	}
	defaultProbeTimeout, err := durationFromEnv("PROBE_TIMEOUT", 2*time.Second)
	if err != nil {
		return nil, err
	}
	defaultReconcileTimeout, err := durationFromEnv("RECONCILE_TIMEOUT", 60*time.Second)
	if err != nil {
		return nil, err
	}
	defaultMaxConcurrentReconciles, err := intFromEnv("MAX_CONCURRENT_RECONCILES", 1)
	if err != nil {
		return nil, err
	}
	defaultKubeconfig := os.Getenv("KUBECONFIG")
	defaultChartPath := os.Getenv("HELM_CHART_PATH")

	fs := flag.NewFlagSet("xmc_bridge", flag.ContinueOnError)
	cmsAddress := fs.String("cms-address", defaultCMS, "CMS gRPC server address")
	namespace := fs.String("namespace", defaultNS, "Kubernetes namespace to watch for Component resources")
	releaseName := fs.String("release-name", defaultReleaseName, "Helm release name for 1P component actuation")
	probeInterval := fs.Duration("probe-interval", defaultProbeInterval, "Delay after a successful reconcile pass before a Component is re-probed")
	probeTimeout := fs.Duration("probe-timeout", defaultProbeTimeout, "Timeout duration for an individual component health probe")
	reconcileTimeout := fs.Duration("reconcile-timeout", defaultReconcileTimeout, "Timeout duration for one Component reconcile pass")
	maxConcurrentReconciles := fs.Int("max-concurrent-reconciles", defaultMaxConcurrentReconciles, "Number of Components reconciled in parallel")
	metricsBindAddress := fs.String("metrics-bind-address", "127.0.0.1:8080", "Address of the unauthenticated metrics endpoint; 0 disables it")
	healthProbeBindAddress := fs.String("health-probe-bind-address", ":8081", "Address of the /healthz and /readyz endpoints")
	kubeconfig := fs.String("kubeconfig", defaultKubeconfig, "Path to kubeconfig file")
	chartPath := fs.String("chart-path", defaultChartPath, "Base path to per-component Helm charts (<chart-path>/components/<name>) or a fallback Helm chart directory/tarball")

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
	if *reconcileTimeout <= 0 {
		return nil, fmt.Errorf("reconcile-timeout must be a positive duration, got %v", *reconcileTimeout)
	}
	if *maxConcurrentReconciles < 1 {
		return nil, fmt.Errorf("max-concurrent-reconciles must be at least 1, got %d", *maxConcurrentReconciles)
	}
	if *healthProbeBindAddress == "" {
		return nil, errors.New("health-probe-bind-address must be specified")
	}
	return &Config{
		CMSAddress:              *cmsAddress,
		Namespace:               *namespace,
		ReleaseName:             *releaseName,
		ProbeInterval:           *probeInterval,
		ProbeTimeout:            *probeTimeout,
		ReconcileTimeout:        *reconcileTimeout,
		MaxConcurrentReconciles: *maxConcurrentReconciles,
		MetricsBindAddress:      *metricsBindAddress,
		HealthProbeBindAddress:  *healthProbeBindAddress,
		Kubeconfig:              *kubeconfig,
		ChartPath:               *chartPath,
	}, nil
}

// durationFromEnv returns the duration in the environment variable name, or def when it is unset.
func durationFromEnv(name string, def time.Duration) (time.Duration, error) {
	val := os.Getenv(name)
	if val == "" {
		return def, nil
	}
	d, err := time.ParseDuration(val)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", name, val, err)
	}
	return d, nil
}

// intFromEnv returns the integer in the environment variable name, or def when it is unset.
func intFromEnv(name string, def int) (int, error) {
	val := os.Getenv(name)
	if val == "" {
		return def, nil
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", name, val, err)
	}
	return n, nil
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

// BuildStatus converts a ReconcileResult into a typed ComponentStatus matching the Component CRD status schema,
// including observedGeneration, state string, access envVars (if present), and standard RFC3339 condition list.
func BuildStatus(res *bridge.ReconcileResult) ComponentStatus {
	return bridge.BuildStatus(res)
}

// BuildStatusMap converts a ReconcileResult into an unstructured map matching the Component CRD status schema.
func BuildStatusMap(res *bridge.ReconcileResult) map[string]any {
	return bridge.BuildStatusMap(res)
}

// RunBridge initializes all dependencies (CMS gRPC client, Kubernetes clients, WorkloadManager and
// Reconciler) and runs the Component controller until ctx is cancelled.
func RunBridge(ctx context.Context, cfg *Config) error {
	logging.Info(ctx, "Connecting to Component Manager Service (CMS)", "address", cfg.CMSAddress)
	conn, err := grpc.NewClient(
		cfg.CMSAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithPerRPCCredentials(bridge.NewCMSTokenCredentials(ctx)),
	)
	if err != nil {
		return fmt.Errorf("failed to connect to CMS at %q: %w", cfg.CMSAddress, err)
	}
	defer conn.Close()

	logging.Info(ctx, "Configuring Kubernetes clients", "kubeconfig", cfg.Kubeconfig)
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
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(k8sCfg)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes discovery client: %w", err)
	}
	cachedDiscovery := memory.NewMemCacheClient(discoveryClient)
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(cachedDiscovery)
	detector := bridge.NewK8sStatusDetector(dynamicClient, mapper)
	netProber := bridge.NewNetworkHealthProber(bridge.WithHealthProbeTimeout(cfg.ProbeTimeout))
	bridgeProber := bridge.NewBridgeProber(detector, netProber, cfg.ProbeTimeout)

	helmOpts := []bridge.HelmManagerOption{
		bridge.WithKubeconfig(cfg.Kubeconfig),
	}
	if cfg.ChartPath != "" {
		helmOpts = append(helmOpts, bridge.WithChartPath(cfg.ChartPath))
	}
	helmMgr := bridge.NewK8sHelmManager(k8sClient, cfg.Namespace, cfg.ReleaseName, helmOpts...)
	// The leader's runnable drains the Helm worker on a graceful shutdown. This stops the worker on every other
	// return path too: a follower that never led, an early error, or a lost Lease, where Helm work is cancelled
	// rather than awaited, so exit waits only for the cancelled Helm actions to return.
	defer helmMgr.Abort()
	reconciler, err := bridge.NewReconciler(componentspb.NewComponentManagerServiceClient(conn), bridgeProber, helmMgr)
	if err != nil {
		return fmt.Errorf("failed to initialize Reconciler: %w", err)
	}
	mgr, err := ctrl.NewManager(k8sCfg, bridge.ComponentManagerOptions(bridge.ComponentManagerConfig{
		Namespace:              cfg.Namespace,
		LeaderElection:         true,
		MetricsBindAddress:     cfg.MetricsBindAddress,
		HealthProbeBindAddress: cfg.HealthProbeBindAddress,
	}))
	if err != nil {
		return fmt.Errorf("failed to create controller manager: %w", err)
	}
	if err := bridge.AddComponentHealthChecks(ctx, mgr); err != nil {
		return err
	}
	// Helm hydration and the Helm worker's shutdown run only while this process holds the Lease.
	// A lost Lease stops the manager without a grace period; RunBridge then cancels in-flight Helm work
	// and returns, and the process exits, so that no Helm write outlives the Lease.
	if err := mgr.Add(bridge.NewHelmLeaderRunnable(helmMgr)); err != nil {
		return fmt.Errorf("failed to add Helm manager runnable: %w", err)
	}
	controller := bridge.NewComponentController(mgr.GetClient(), dynamicClient, reconciler, bridge.ComponentControllerOptions{
		ProbeInterval:           cfg.ProbeInterval,
		ReconcileTimeout:        cfg.ReconcileTimeout,
		MaxConcurrentReconciles: cfg.MaxConcurrentReconciles,
	})
	if err := controller.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("failed to set up Component controller: %w", err)
	}
	logging.Info(ctx, "Starting XMC Bridge Controller; reconciling once elected", "namespace", cfg.Namespace, "lease", bridge.ComponentLeaseName, "probeInterval", cfg.ProbeInterval, "probeTimeout", cfg.ProbeTimeout, "reconcileTimeout", cfg.ReconcileTimeout, "maxConcurrentReconciles", cfg.MaxConcurrentReconciles)
	return mgr.Start(ctx)
}

func main() {
	cfg, err := ParseConfig(os.Args[1:])
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	logger := logging.NewGCPLogger(slog.LevelInfo, "")
	ctrl.SetLogger(logr.FromSlogHandler(logger.Handler()))
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
