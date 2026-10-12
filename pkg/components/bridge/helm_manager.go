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
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/cli"

	"xmanager_cloud/internal/logging"
	"xmanager_cloud/pkg/components/naming"
)

const (
	// DefaultNamespace is the standard Kubernetes namespace for XMC components.
	DefaultNamespace = "xmc"
	// DefaultHelmReleaseName is the standard Helm release name for XMC.
	DefaultHelmReleaseName = "xmanager-cloud"
)

// HelmManager defines the contract for provisioning and managing 1P component workloads via Helm releases.
type HelmManager interface {
	// Ensure1PComponent sets the component's enablement flag and values in the Helm release and triggers an upgrade.
	Ensure1PComponent(ctx context.Context, ref ComponentRef, opts HelmComponentOptions) error
	// Delete1PComponent uninstalls the 1P component Helm release and records it as disabled in the ConfigMap.
	Delete1PComponent(ctx context.Context, ref ComponentRef) error
	// Purge1PComponent permanently uninstalls the Helm release and removes component values from the ConfigMap.
	Purge1PComponent(ctx context.Context, ref ComponentRef) error
	// Orphan1PComponent retains the component values while detaching bridge ownership.
	Orphan1PComponent(ctx context.Context, ref ComponentRef) error
	// IsComponentActive checks if the component is currently marked enabled in the Helm release.
	IsComponentActive(ref ComponentRef) bool
	// IsComponentActuated checks if the component is currently active and was actuated at the specified generation.
	IsComponentActuated(ref ComponentRef, generation int64) bool
	// NeedsRepair reports whether the component's release, already actuated at the specified generation, was found
	// missing, failed, or drifted and must be re-applied. A repair re-applies an approved spec, so it is not held for
	// unready dependencies.
	NeedsRepair(ref ComponentRef, generation int64) bool
	// RecordActuation records that the component was successfully actuated at the specified generation.
	RecordActuation(ref ComponentRef, generation int64)
	// HydrateComponentState synchronizes in-memory active and actuated generation state from existing CR status and cluster storage on cold start.
	HydrateComponentState(ctx context.Context, ref ComponentRef, observedGeneration int64, crReady bool) error
	// ComponentHealth reports whether the component's Helm release is deployed and its last upgrade succeeded,
	// returning a single-word condition reason and a human-readable message.
	ComponentHealth(ctx context.Context, ref ComponentRef) (healthy bool, reason, message string)
}

// K8sHelmManager implements HelmManager by synchronizing component values in Kubernetes ConfigMaps
// and executing Helm release upgrades via the Helm Go SDK.
type K8sHelmManager struct {
	client            kubernetes.Interface
	namespace         string
	releaseName       string
	chartPath         string
	chartPathResolver ChartPathResolver
	kubeconfig        string
	hasKubeconfig     bool
	restGetter        genericclioptions.RESTClientGetter
	hasRESTGetter     bool
	runner            HelmActionRunner
	syncExecution     bool
	debounceDuration  time.Duration

	mu                  sync.RWMutex
	activeState         map[string]bool
	actuatedGenerations map[string]int64
	componentChartPaths map[string]string
	chartCache          map[string]*chart.Chart
	upgradeErrs         map[string]error
	// upgradeSem serializes every Helm release operation: sync and async upgrades and uninstalls.
	upgradeSem chan struct{}
	// pendingUpgrades maps component keys to the sequence number of their latest enqueued async upgrade.
	pendingUpgrades map[string]uint64
	upgradeSeq      uint64
	// healthVerifiedAt records when a component's release was last observed deployed, to throttle Status calls.
	healthVerifiedAt map[string]time.Time
	// repairNeeded marks component keys whose actuated release was found missing, failed, or drifted from its manifest;
	// it forces the next Ensure1PComponent to re-run the Helm upgrade at the current generation.
	repairNeeded map[string]bool
	// repairBackoff rate-limits repairs per component key; see repairBackoffBase.
	repairBackoff map[string]repairAttempt
	// jitter returns a value in [0, 1) that randomizes repair backoff; tests may replace it for determinism.
	jitter func() float64

	stopMu      sync.RWMutex
	stopped     bool
	producerWg  sync.WaitGroup
	upgradeChan chan upgradeRequest
	stopCh      chan struct{}
	closeOnce   sync.Once
	wg          sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc
}

var _ HelmManager = (*K8sHelmManager)(nil)

// HelmManagerOption configures a K8sHelmManager.
type HelmManagerOption func(*K8sHelmManager)

// WithActionRunner sets the HelmActionRunner for release upgrades.
func WithActionRunner(runner HelmActionRunner) HelmManagerOption {
	return func(m *K8sHelmManager) { m.runner = runner }
}

// WithChartPath sets the base or single-chart filesystem path for release upgrades.
func WithChartPath(chartPath string) HelmManagerOption {
	return func(m *K8sHelmManager) { m.chartPath = chartPath }
}

// WithChartPathResolver sets a custom per-component Helm chart path resolver.
func WithChartPathResolver(resolver ChartPathResolver) HelmManagerOption {
	return func(m *K8sHelmManager) { m.chartPathResolver = resolver }
}

// WithSyncExecution configures whether Helm upgrades execute synchronously or via background workers.
func WithSyncExecution(sync bool) HelmManagerOption {
	return func(m *K8sHelmManager) { m.syncExecution = sync }
}

// WithDebounceDuration sets the debounce aggregation window for asynchronous upgrades.
func WithDebounceDuration(d time.Duration) HelmManagerOption {
	return func(m *K8sHelmManager) {
		if d > 0 {
			m.debounceDuration = d
		}
	}
}

// WithKubeconfig sets the kubeconfig path for release upgrades.
func WithKubeconfig(kubeconfig string) HelmManagerOption {
	return func(m *K8sHelmManager) {
		m.kubeconfig = kubeconfig
		m.hasKubeconfig = true
	}
}

// WithRESTClientGetter sets the RESTClientGetter for release upgrades.
func WithRESTClientGetter(getter genericclioptions.RESTClientGetter) HelmManagerOption {
	return func(m *K8sHelmManager) {
		m.restGetter = getter
		m.hasRESTGetter = true
	}
}

// NewK8sHelmManager constructs an initialized K8sHelmManager.
func NewK8sHelmManager(client kubernetes.Interface, namespace, releaseName string, opts ...HelmManagerOption) *K8sHelmManager {
	ctx, cancel := context.WithCancel(context.Background())
	mgr := &K8sHelmManager{
		client:              client,
		namespace:           namespace,
		releaseName:         releaseName,
		debounceDuration:    25 * time.Millisecond,
		activeState:         make(map[string]bool),
		actuatedGenerations: make(map[string]int64),
		componentChartPaths: make(map[string]string),
		chartCache:          make(map[string]*chart.Chart),
		upgradeErrs:         make(map[string]error),
		pendingUpgrades:     make(map[string]uint64),
		healthVerifiedAt:    make(map[string]time.Time),
		repairNeeded:        make(map[string]bool),
		repairBackoff:       make(map[string]repairAttempt),
		jitter:              rand.Float64,
		upgradeSem:          make(chan struct{}, 1),
		upgradeChan:         make(chan upgradeRequest, 100),
		stopCh:              make(chan struct{}),
		ctx:                 ctx,
		cancel:              cancel,
	}
	for _, opt := range opts {
		opt(mgr)
	}
	if mgr.runner == nil && (mgr.hasKubeconfig || mgr.hasRESTGetter) {
		mgr.runner = mgr.defaultActionRunner()
	}
	if !mgr.syncExecution {
		mgr.wg.Add(1)
		go mgr.syncUpgradeWorker()
	}
	return mgr
}

// defaultActionRunner builds an SDKHelmActionRunner from the configured REST client getter, or from Helm CLI settings
// for the manager's namespace and kubeconfig, that resolves each release's chart through the manager.
func (m *K8sHelmManager) defaultActionRunner() *SDKHelmActionRunner {
	getter := m.restGetter
	if getter == nil {
		settings := cli.New()
		settings.SetNamespace(m.namespace)
		if m.kubeconfig != "" {
			settings.KubeConfig = m.kubeconfig
		}
		getter = settings.RESTClientGetter()
	}
	return NewSDKHelmActionRunner(getter, WithRunnerChartPath(m.chartPath), WithRunnerChartPathResolver(func(releaseName string) string {
		return m.resolveChartPathForComponent(ComponentRef{Namespace: m.namespace, Name: componentNameFromRelease(releaseName)})
	}))
}

// Close stops the background upgrade worker, drops the queued upgrades that have not started, and waits for the
// in-flight upgrade to complete cleanly.
func (m *K8sHelmManager) Close() error {
	if m == nil {
		return nil
	}
	m.closeOnce.Do(func() {
		m.stopMu.Lock()
		m.stopped = true
		close(m.stopCh)
		m.stopMu.Unlock()

		m.producerWg.Wait()
		m.wg.Wait()
		m.dropQueuedUpgrades()
		if m.cancel != nil {
			m.cancel()
		}
	})
	return nil
}

// Shutdown stops accepting upgrades, drops the queued ones that have not started, and waits for the in-flight one to
// finish, like Close. If ctx ends first, Shutdown cancels the in-flight upgrade instead, waits for the worker to
// return, and returns ctx's error.
func (m *K8sHelmManager) Shutdown(ctx context.Context) error {
	if m == nil {
		return nil
	}
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		m.Close()
	}()
	select {
	case <-closed:
		return nil
	case <-ctx.Done():
		if m.cancel != nil {
			m.cancel()
		}
		<-closed
		return ctx.Err()
	}
}

// Abort stops accepting upgrades, cancels the queued and in-flight ones instead of waiting for them, and waits for
// the background worker to return. That wait lasts only as long as the cancelled Helm actions take to return. Abort
// returns at once if the manager was already closed.
func (m *K8sHelmManager) Abort() {
	if m == nil {
		return
	}
	if m.cancel != nil {
		m.cancel()
	}
	m.Close()
}

// componentReleasePrefix prefixes the component name in every per-component Helm release name.
const componentReleasePrefix = "xmc-cmp-"

// ComponentReleaseName returns the Helm release name for an individual 1P component.
func ComponentReleaseName(componentName string) string {
	canonical := naming.CanonicalComponentName(componentName)
	if canonical == "" {
		return ""
	}
	return componentReleasePrefix + canonical
}

// ComponentReleaseNameForRef returns the Helm release name ("xmc-cmp-<canonical>") for ref.
// Keeping the canonical release name across custom resource UIDs preserves Helm resource ownership
// metadata on live upgrades and allows a recreated CR to adopt an Orphan-retained release.
func ComponentReleaseNameForRef(ref ComponentRef) string {
	return ComponentReleaseName(ref.Name)
}

// componentNameFromRelease is the inverse of ComponentReleaseName.
func componentNameFromRelease(releaseName string) string {
	return strings.TrimPrefix(releaseName, componentReleasePrefix)
}

func componentKey(namespace, componentName string) string {
	return namespace + "/" + componentName
}

func componentStateKey(ref ComponentRef) string {
	return ref.String()
}

func (m *K8sHelmManager) hasOtherActiveIncarnation(ref ComponentRef) bool {
	if ref.UID == "" {
		return false
	}
	targetKey := componentStateKey(ref)
	prefix := componentKey(ref.Namespace, ref.Name) + "@"
	m.mu.RLock()
	defer m.mu.RUnlock()
	for k, active := range m.activeState {
		if active && k != targetKey && strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

// validateComponentRef reports an error if the component reference is missing a name or namespace.
func validateComponentRef(ref ComponentRef) error {
	if ref.Name == "" {
		return errors.New("component name must not be empty")
	}
	if ref.Namespace == "" {
		return errors.New("component namespace must not be empty")
	}
	return nil
}

// Ensure1PComponent sets component values in the release ConfigMap and executes an independent Helm upgrade.
func (m *K8sHelmManager) Ensure1PComponent(ctx context.Context, ref ComponentRef, opts HelmComponentOptions) error {
	if m == nil {
		return nil
	}
	if err := validateComponentRef(ref); err != nil {
		return err
	}
	values := BuildHelmValues(opts)
	modified, err := m.persistComponentValues(ctx, ref.Namespace, ref.Name, values)
	if err != nil {
		return fmt.Errorf("failed to persist Helm values for %q: %w", ref.Name, err)
	}
	// TODO(vluk): Read the chart override, repair mark, activity and upgrade error under one m.mu critical section so
	// a concurrent teardown cannot land between these checks and the upgrade (pre-existing check-then-act gap).
	chartPathChanged := m.recordChartPathOverride(ref, opts.ChartPath)
	repair := m.isRepairMarked(ref)
	if !modified && !chartPathChanged && !repair && m.IsComponentActive(ref) && m.GetUpgradeError(ref) == nil {
		return nil
	}
	if repair {
		m.recordRepairAttempt(ctx, ref)
	}
	if err := m.triggerUpgrade(ctx, ref, values); err != nil {
		return err
	}
	m.mu.Lock()
	m.activeState[componentStateKey(ref)] = opts.Enabled
	m.mu.Unlock()
	logging.Info(ctx, "Ensured 1P component Helm values and triggered upgrade", "name", ref.Name, "namespace", ref.Namespace, "release", ComponentReleaseNameForRef(ref), "enabled", opts.Enabled, "repair", repair)
	return nil
}

// recordChartPathOverride stores the component's chart path override, clearing it when chartPath is empty, and reports
// whether the override changed.
func (m *K8sHelmManager) recordChartPathOverride(ref ComponentRef, chartPath string) bool {
	key := componentKey(ref.Namespace, ref.Name)
	m.mu.Lock()
	defer m.mu.Unlock()
	changed := m.componentChartPaths[key] != chartPath
	if chartPath == "" {
		delete(m.componentChartPaths, key)
		return changed
	}
	m.componentChartPaths[key] = chartPath
	return changed
}

// Delete1PComponent uninstalls the 1P component Helm release and records it as disabled in the ConfigMap.
func (m *K8sHelmManager) Delete1PComponent(ctx context.Context, ref ComponentRef) error {
	return m.uninstallComponent(ctx, ref, componentTeardown{action: "disable", updateValues: m.disableComponentValues, keepInactive: true})
}

// Purge1PComponent permanently uninstalls the Helm release and removes component values from the ConfigMap.
func (m *K8sHelmManager) Purge1PComponent(ctx context.Context, ref ComponentRef) error {
	return m.uninstallComponent(ctx, ref, componentTeardown{action: "purge", updateValues: m.removeComponentValues, alwaysUninstall: true})
}

// componentTeardown describes how uninstallComponent records a teardown.
type componentTeardown struct {
	// action names the teardown in errors and logs.
	action string
	// updateValues records the teardown in the values ConfigMap and reports whether it changed.
	updateValues func(ctx context.Context, namespace, componentName string) (bool, error)
	// keepInactive selects the state kept after uninstall; see forgetComponentLocked.
	keepInactive bool
	// alwaysUninstall forces uninstallRelease even when the ConfigMap and in-memory active state were already empty
	// (e.g. purging a release on cold start or in a fenced cluster).
	alwaysUninstall bool
}

// uninstallComponent records the teardown in the values ConfigMap, uninstalls the component's release, and forgets its
// state. When another CR incarnation in the same namespace is already active, it forgets only ref's state without
// touching the shared ConfigMap entry or uninstalling the active incarnation's release.
func (m *K8sHelmManager) uninstallComponent(ctx context.Context, ref ComponentRef, td componentTeardown) error {
	if m == nil {
		return nil
	}
	if err := validateComponentRef(ref); err != nil {
		return err
	}
	if m.hasOtherActiveIncarnation(ref) {
		m.dropPendingUpgrade(ref)
		m.setUpgradeErr(ref, nil)
		m.mu.Lock()
		m.forgetIncarnationLocked(ref)
		m.mu.Unlock()
		return nil
	}
	modified, err := td.updateValues(ctx, ref.Namespace, ref.Name)
	if err != nil {
		return fmt.Errorf("failed to %s Helm values for %q: %w", td.action, ref.Name, err)
	}
	if !td.alwaysUninstall && !modified && !m.IsComponentActive(ref) && m.GetUpgradeError(ref) == nil {
		return nil
	}
	if err := m.uninstallRelease(ctx, ref); err != nil {
		return err
	}
	m.mu.Lock()
	m.forgetComponentLocked(ref, td.keepInactive)
	m.mu.Unlock()
	logging.Info(ctx, "Uninstalled 1P component Helm release", "action", td.action, "name", ref.Name, "namespace", ref.Namespace, "release", ComponentReleaseNameForRef(ref))
	return nil
}

// Orphan1PComponent detaches component management without modifying release values or uninstalling Helm.
func (m *K8sHelmManager) Orphan1PComponent(ctx context.Context, ref ComponentRef) error {
	if m == nil {
		return nil
	}
	if err := validateComponentRef(ref); err != nil {
		return err
	}
	m.dropPendingUpgrade(ref)
	m.setUpgradeErr(ref, nil)
	m.mu.Lock()
	m.forgetComponentLocked(ref, false)
	m.mu.Unlock()
	logging.Info(ctx, "Orphaned 1P component Helm values", "name", ref.Name, "namespace", ref.Namespace)
	return nil
}
