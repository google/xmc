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
	"xmanager_cloud/pkg/components/bridge/healthprobe"
	"xmanager_cloud/pkg/components/bridge/helm"
	"xmanager_cloud/pkg/components/bridge/statusdetector"

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
	// ClusterName is the human-readable cluster label declared to CMS.
	ClusterName string
	// OwnershipEpoch is the monotonic fencing epoch declared to CMS (>= 1).
	OwnershipEpoch int64
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

// finalizeConfig checks required Config fields, resolves cluster identity, and validates timings.
func finalizeConfig(cfg *Config) error {
	if cfg == nil {
		return errors.New("config cannot be nil")
	}
	if cfg.CMSAddress == "" {
		return errors.New("cms-address must be specified")
	}
	if cfg.Namespace == "" {
		return errors.New("namespace must be specified")
	}
	if cfg.ReleaseName == "" {
		return errors.New("release-name must be specified")
	}
	resolvedClusterName, err := resolveClusterIdentityFlags(cfg.ClusterName, cfg.ReleaseName, cfg.OwnershipEpoch)
	if err != nil {
		return err
	}
	cfg.ClusterName = resolvedClusterName
	if err := validateTimings(cfg.ProbeInterval, cfg.ProbeTimeout, cfg.ReconcileTimeout); err != nil {
		return err
	}
	if cfg.MaxConcurrentReconciles < 1 {
		return fmt.Errorf("max-concurrent-reconciles must be at least 1, got %d", cfg.MaxConcurrentReconciles)
	}
	if cfg.HealthProbeBindAddress == "" || cfg.HealthProbeBindAddress == "0" {
		return fmt.Errorf("health-probe-bind-address must be a listen address (\"\" and \"0\" disable the probes), got %q", cfg.HealthProbeBindAddress)
	}
	return nil
}

// ParseConfig parses command line flags and environment variables into a validated Config object.
// args typically receives os.Args[1:]. Default values fall back to corresponding environment
// variables (CMS_ADDRESS, NAMESPACE, PROBE_INTERVAL, PROBE_TIMEOUT, RECONCILE_TIMEOUT,
// MAX_CONCURRENT_RECONCILES, KUBECONFIG) if flags are omitted.
// Returns an error if flag parsing fails, if intervals/timeouts are non-positive, if
// max-concurrent-reconciles is below 1 or if health-probe-bind-address is empty or "0".
func ParseConfig(args []string) (*Config, error) {
	defaultOwnershipEpoch, err := ownershipEpochFromEnv()
	if err != nil {
		return nil, err
	}
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

	cfg := &Config{
		CMSAddress:              os.Getenv("CMS_ADDRESS"),
		Namespace:               os.Getenv("NAMESPACE"),
		ReleaseName:             os.Getenv("HELM_RELEASE_NAME"),
		ClusterName:             os.Getenv("CLUSTER_NAME"),
		OwnershipEpoch:          defaultOwnershipEpoch,
		ProbeInterval:           defaultProbeInterval,
		ProbeTimeout:            defaultProbeTimeout,
		ReconcileTimeout:        defaultReconcileTimeout,
		MaxConcurrentReconciles: defaultMaxConcurrentReconciles,
		MetricsBindAddress:      "127.0.0.1:8080",
		HealthProbeBindAddress:  ":8081",
		Kubeconfig:              os.Getenv("KUBECONFIG"),
		ChartPath:               os.Getenv("HELM_CHART_PATH"),
	}
	fs := flag.NewFlagSet("xmc_bridge", flag.ContinueOnError)
	fs.StringVar(&cfg.CMSAddress, "cms-address", cfg.CMSAddress, "CMS gRPC server address")
	fs.StringVar(&cfg.Namespace, "namespace", cfg.Namespace, "Kubernetes namespace to watch for Component resources")
	fs.StringVar(&cfg.ReleaseName, "release-name", cfg.ReleaseName, "Helm release name for 1P component actuation")
	fs.StringVar(&cfg.ClusterName, "cluster-name", cfg.ClusterName, "Human-readable cluster label declared to CMS (defaults to release-name)")
	fs.Int64Var(&cfg.OwnershipEpoch, "ownership-epoch", cfg.OwnershipEpoch, "Monotonic fencing epoch declared to CMS (>= 1)")
	fs.DurationVar(&cfg.ProbeInterval, "probe-interval", cfg.ProbeInterval, "Delay after a successful reconcile pass before a Component is re-probed")
	fs.DurationVar(&cfg.ProbeTimeout, "probe-timeout", cfg.ProbeTimeout, "Timeout duration for an individual component health probe")
	fs.DurationVar(&cfg.ReconcileTimeout, "reconcile-timeout", cfg.ReconcileTimeout, "Timeout duration for one Component reconcile pass")
	fs.IntVar(&cfg.MaxConcurrentReconciles, "max-concurrent-reconciles", cfg.MaxConcurrentReconciles, "Number of Components reconciled in parallel")
	fs.StringVar(&cfg.MetricsBindAddress, "metrics-bind-address", cfg.MetricsBindAddress, "Address of the unauthenticated metrics endpoint; 0 disables it")
	fs.StringVar(&cfg.HealthProbeBindAddress, "health-probe-bind-address", cfg.HealthProbeBindAddress, "Address of the /healthz and /readyz endpoints; must not be empty or 0")
	fs.StringVar(&cfg.Kubeconfig, "kubeconfig", cfg.Kubeconfig, "Path to kubeconfig file")
	fs.StringVar(&cfg.ChartPath, "chart-path", cfg.ChartPath, "Base path to per-component Helm charts (<chart-path>/components/<name>) or a fallback Helm chart directory/tarball")

	if err := fs.Parse(args); err != nil {
		return nil, fmt.Errorf("failed to parse command line flags: %w", err)
	}
	if err := finalizeConfig(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// validateTimings returns an error for the first non-positive probe or reconcile duration. It is
// separate from ParseConfig to keep that function within the cyclomatic complexity limit.
func validateTimings(probeInterval, probeTimeout, reconcileTimeout time.Duration) error {
	if probeInterval <= 0 {
		return fmt.Errorf("probe-interval must be a positive duration, got %v", probeInterval)
	}
	if probeTimeout <= 0 {
		return fmt.Errorf("probe-timeout must be a positive duration, got %v", probeTimeout)
	}
	if reconcileTimeout <= 0 {
		return fmt.Errorf("reconcile-timeout must be a positive duration, got %v", reconcileTimeout)
	}
	return nil
}

// durationFromEnv returns the duration parsed from environment variable name, or def when unset.
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

// ownershipEpochFromEnv returns the COMPONENT_OWNERSHIP_EPOCH environment value, or 1 when unset.
func ownershipEpochFromEnv() (int64, error) {
	val := os.Getenv("COMPONENT_OWNERSHIP_EPOCH")
	if val == "" {
		return 1, nil
	}
	epoch, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid COMPONENT_OWNERSHIP_EPOCH %q: %w", val, err)
	}
	return epoch, nil
}

// resolveClusterIdentityFlags defaults clusterName to releaseName, validates it as a DNS-1123
// label and checks that epoch is at least 1. It returns the resolved cluster name.
func resolveClusterIdentityFlags(clusterName, releaseName string, epoch int64) (string, error) {
	if clusterName == "" {
		clusterName = releaseName
	}
	if err := bridge.ValidateClusterName(clusterName); err != nil {
		return "", err
	}
	if epoch < 1 {
		return "", fmt.Errorf("ownership-epoch must be at least 1, got %d", epoch)
	}
	return clusterName, nil
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

// dialCMS creates the CMS gRPC client authenticated with the bridge's CMS credentials.
func dialCMS(ctx context.Context, address string) (*grpc.ClientConn, error) {
	logging.Info(ctx, "Connecting to Component Manager Service (CMS)", "address", address)
	cmsCreds, err := bridge.NewCMSTokenCredentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve CMS credentials: %w", err)
	}
	conn, err := grpc.NewClient(
		address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithPerRPCCredentials(cmsCreds),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to CMS at %q: %w", address, err)
	}
	return conn, nil
}

type bridgeK8sClients struct {
	restCfg   *rest.Config
	k8sClient kubernetes.Interface
	prober    *healthprobe.BridgeProber
}

func buildBridgeK8sClients(kubeconfig string, probeTimeout time.Duration) (bridgeK8sClients, error) {
	k8sCfg, err := BuildKubeConfig(kubeconfig)
	if err != nil {
		return bridgeK8sClients{}, fmt.Errorf("failed to build Kubernetes client config: %w", err)
	}
	dynamicClient, err := dynamic.NewForConfig(k8sCfg)
	if err != nil {
		return bridgeK8sClients{}, fmt.Errorf("failed to create Kubernetes dynamic client: %w", err)
	}
	k8sClient, err := kubernetes.NewForConfig(k8sCfg)
	if err != nil {
		return bridgeK8sClients{}, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(k8sCfg)
	if err != nil {
		return bridgeK8sClients{}, fmt.Errorf("failed to create Kubernetes discovery client: %w", err)
	}
	cachedDiscovery := memory.NewMemCacheClient(discoveryClient)
	detector := statusdetector.NewK8sStatusDetector(dynamicClient, restmapper.NewDeferredDiscoveryRESTMapper(cachedDiscovery))
	netProber := healthprobe.NewNetworkHealthProber(healthprobe.WithHealthProbeTimeout(probeTimeout))
	return bridgeK8sClients{
		restCfg:   k8sCfg,
		k8sClient: k8sClient,
		prober:    healthprobe.NewBridgeProber(detector, netProber, probeTimeout),
	}, nil
}

// RunBridge initializes all dependencies (CMS gRPC client, Kubernetes clients, WorkloadManager and
// Reconciler) and runs the Component controller until ctx is cancelled.
func RunBridge(ctx context.Context, cfg *Config) error {
	conn, err := dialCMS(ctx, cfg.CMSAddress)
	if err != nil {
		return err
	}
	defer conn.Close()

	logging.Info(ctx, "Configuring Kubernetes clients", "kubeconfig", cfg.Kubeconfig)
	k8sClients, err := buildBridgeK8sClients(cfg.Kubeconfig, cfg.ProbeTimeout)
	if err != nil {
		return err
	}

	var helmOpts []helm.HelmManagerOption
	helmOpts = append(helmOpts, helm.WithKubeconfig(cfg.Kubeconfig))
	if cfg.ChartPath != "" {
		helmOpts = append(helmOpts, helm.WithChartPath(cfg.ChartPath))
	}
	helmMgr := helm.NewK8sHelmManager(k8sClients.k8sClient, cfg.Namespace, cfg.ReleaseName, helmOpts...)
	// The leader's runnable drains the Helm worker on a graceful shutdown. This stops the worker on every other
	// return path too: a follower that never led, an early error, or a lost Lease, where Helm work is cancelled
	// rather than awaited, so exit waits only for the cancelled Helm actions to return.
	defer helmMgr.Abort()

	clusterUID, err := bridge.ReadClusterUID(ctx, k8sClients.k8sClient)
	if err != nil {
		return fmt.Errorf("failed to read cluster identity: %w", err)
	}
	cmsClient := componentspb.NewComponentManagerServiceClient(conn)
	fence, err := bridge.NewClusterFence(cmsClient, &componentspb.ClusterIdentity{Uid: clusterUID, Name: cfg.ClusterName, Epoch: cfg.OwnershipEpoch}, bridge.ClusterFenceOptions{})
	if err != nil {
		return fmt.Errorf("failed to initialize cluster fence: %w", err)
	}
	mgr, err := ctrl.NewManager(k8sClients.restCfg, bridge.ComponentManagerOptions(bridge.ComponentManagerConfig{
		Namespace: cfg.Namespace, LeaderElection: true, MetricsBindAddress: cfg.MetricsBindAddress, HealthProbeBindAddress: cfg.HealthProbeBindAddress,
	}))
	if err != nil {
		return fmt.Errorf("failed to create controller manager: %w", err)
	}
	// The live UID reader bypasses the informer cache, so takeover and stale-incarnation checks see
	// the current Component rather than a possibly stale cached copy.
	reconciler, err := bridge.NewReconciler(cmsClient, k8sClients.prober, helmMgr, bridge.WithClusterFence(fence), bridge.WithLiveUIDReader(bridge.NewAPIReaderLiveUID(mgr.GetAPIReader())))
	if err != nil {
		return fmt.Errorf("failed to initialize Reconciler: %w", err)
	}
	if err := bridge.AddComponentHealthChecks(ctx, mgr); err != nil {
		return err
	}
	// Helm hydration and the Helm worker's shutdown run only while this process holds the Lease.
	// A lost Lease stops the manager without a grace period; RunBridge then cancels in-flight Helm work
	// and returns, and the process exits, so that no Helm write outlives the Lease.
	if err := mgr.Add(helm.NewHelmLeaderRunnable(helmMgr)); err != nil {
		return fmt.Errorf("failed to add Helm manager runnable: %w", err)
	}
	if err := mgr.Add(fence); err != nil {
		return fmt.Errorf("failed to add cluster fence runnable: %w", err)
	}
	controller := bridge.NewComponentController(mgr.GetClient(), reconciler, bridge.ComponentControllerOptions{
		Namespace: cfg.Namespace, ProbeInterval: cfg.ProbeInterval, ReconcileTimeout: cfg.ReconcileTimeout, MaxConcurrentReconciles: cfg.MaxConcurrentReconciles,
	})
	if err := controller.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("failed to set up Component controller: %w", err)
	}
	logging.Info(ctx, "Starting XMC Bridge Controller; reconciling once elected", "namespace", cfg.Namespace, "cluster", cfg.ClusterName, "epoch", cfg.OwnershipEpoch, "lease", bridge.ComponentLeaseName, "probeInterval", cfg.ProbeInterval, "probeTimeout", cfg.ProbeTimeout, "reconcileTimeout", cfg.ReconcileTimeout, "maxConcurrentReconciles", cfg.MaxConcurrentReconciles)
	return mgr.Start(ctx)
}

func configureLogger(ctx context.Context, logger *slog.Logger) context.Context {
	slog.SetDefault(logger)
	ctrl.SetLogger(logr.FromSlogHandler(logger.Handler()))
	return logging.WithLogger(ctx, logger)
}

func main() {
	cfg, err := ParseConfig(os.Args[1:])
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	ctx = configureLogger(ctx, logging.NewGCPLogger(slog.LevelInfo, ""))

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
