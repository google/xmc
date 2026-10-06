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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage/driver"

	"xmanager_cloud/logging/logging"
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
	upgradeMu           sync.Mutex
	upgradeSem          chan struct{}
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
		getter := mgr.restGetter
		if getter == nil {
			settings := cli.New()
			settings.SetNamespace(mgr.namespace)
			if mgr.kubeconfig != "" {
				settings.KubeConfig = mgr.kubeconfig
			}
			getter = settings.RESTClientGetter()
		}
		mgr.runner = NewSDKHelmActionRunner(getter, WithRunnerChartPath(mgr.chartPath), WithRunnerChartPathResolver(func(releaseName string) string {
			return mgr.resolveChartPathForComponent(ComponentRef{Namespace: mgr.namespace, Name: strings.TrimPrefix(releaseName, "xmc-cmp-")})
		}))
	}
	if !mgr.syncExecution {
		mgr.wg.Add(1)
		go mgr.syncUpgradeWorker()
	}
	return mgr
}

// Close stops the background upgrade worker and waits for in-flight upgrades to complete cleanly.
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
		m.drainRemainingUpgrades()
		if m.cancel != nil {
			m.cancel()
		}
	})
	return nil
}

// ComponentReleaseName returns the Helm release name for an individual 1P component.
func ComponentReleaseName(componentName string) string {
	if componentName == "" {
		return ""
	}
	return fmt.Sprintf("xmc-cmp-%s", componentName)
}

func componentKey(namespace, componentName string) string {
	return namespace + "/" + componentName
}

func (m *K8sHelmManager) validateAndResolve(ref ComponentRef) (string, error) {
	if ref.Name == "" {
		return "", errors.New("component name must not be empty")
	}
	if ref.Namespace == "" {
		return "", errors.New("component namespace must not be empty")
	}
	return ref.Namespace, nil
}

// Ensure1PComponent sets component values in the release ConfigMap and executes an independent Helm upgrade.
func (m *K8sHelmManager) Ensure1PComponent(ctx context.Context, ref ComponentRef, opts HelmComponentOptions) error {
	if m == nil {
		return nil
	}
	targetNamespace, err := m.validateAndResolve(ref)
	if err != nil {
		return err
	}
	resolved := ComponentRef{Namespace: targetNamespace, Name: ref.Name}
	values := BuildHelmValues(opts)

	modified, err := m.persistComponentValues(ctx, targetNamespace, ref.Name, values)
	if err != nil {
		return fmt.Errorf("failed to persist Helm values for %q: %w", ref.Name, err)
	}

	key := componentKey(targetNamespace, ref.Name)
	m.mu.Lock()
	if m.componentChartPaths == nil {
		m.componentChartPaths = make(map[string]string)
	}
	prevChartPath := m.componentChartPaths[key]
	chartPathChanged := prevChartPath != opts.ChartPath
	if opts.ChartPath != "" {
		m.componentChartPaths[key] = opts.ChartPath
	} else {
		delete(m.componentChartPaths, key)
	}
	repair := m.repairNeeded[key]
	m.mu.Unlock()

	if !modified && !chartPathChanged && !repair && m.IsComponentActive(resolved) && m.GetUpgradeError(resolved) == nil {
		return nil
	}

	if repair {
		m.recordRepairAttempt(ctx, resolved)
	}
	if err := m.triggerUpgrade(ctx, resolved, values); err != nil {
		return err
	}

	m.mu.Lock()
	if m.activeState == nil {
		m.activeState = make(map[string]bool)
	}
	m.activeState[key] = opts.Enabled
	m.mu.Unlock()

	logging.Info(ctx, "Ensured 1P component Helm values and triggered upgrade", "name", ref.Name, "namespace", targetNamespace, "release", ComponentReleaseName(ref.Name), "enabled", opts.Enabled, "repair", repair)
	return nil
}

// Delete1PComponent uninstalls the 1P component Helm release and records it as disabled in the ConfigMap.
func (m *K8sHelmManager) Delete1PComponent(ctx context.Context, ref ComponentRef) error {
	if m == nil {
		return nil
	}
	targetNamespace, err := m.validateAndResolve(ref)
	if err != nil {
		return err
	}
	resolved := ComponentRef{Namespace: targetNamespace, Name: ref.Name}

	modified, err := m.disableComponentValues(ctx, targetNamespace, ref.Name)
	if err != nil {
		return fmt.Errorf("failed to disable Helm values for %q: %w", ref.Name, err)
	}

	key := componentKey(targetNamespace, ref.Name)
	if !modified && !m.IsComponentActive(resolved) && m.GetUpgradeError(resolved) == nil {
		return nil
	}

	releaseName := ComponentReleaseName(ref.Name)
	if err := m.uninstallRelease(ctx, releaseName, targetNamespace, resolved); err != nil {
		return err
	}

	m.mu.Lock()
	if m.activeState != nil {
		m.activeState[key] = false
	}
	if m.actuatedGenerations != nil {
		delete(m.actuatedGenerations, key)
	}
	m.clearRepairLocked(key)
	m.mu.Unlock()

	logging.Info(ctx, "Deleted 1P component Helm release", "name", ref.Name, "namespace", targetNamespace, "release", releaseName)
	return nil
}

// Purge1PComponent permanently uninstalls the Helm release and removes component values from the ConfigMap.
func (m *K8sHelmManager) Purge1PComponent(ctx context.Context, ref ComponentRef) error {
	if m == nil {
		return nil
	}
	targetNamespace, err := m.validateAndResolve(ref)
	if err != nil {
		return err
	}
	resolved := ComponentRef{Namespace: targetNamespace, Name: ref.Name}

	modified, err := m.removeComponentValues(ctx, targetNamespace, ref.Name)
	if err != nil {
		return fmt.Errorf("failed to purge Helm values for %q: %w", ref.Name, err)
	}

	key := componentKey(targetNamespace, ref.Name)
	if !modified && !m.IsComponentActive(resolved) && m.GetUpgradeError(resolved) == nil {
		return nil
	}

	releaseName := ComponentReleaseName(ref.Name)
	if err := m.uninstallRelease(ctx, releaseName, targetNamespace, resolved); err != nil {
		return err
	}

	m.mu.Lock()
	if m.activeState != nil {
		delete(m.activeState, key)
	}
	if m.actuatedGenerations != nil {
		delete(m.actuatedGenerations, key)
	}
	delete(m.componentChartPaths, key)
	m.clearRepairLocked(key)
	m.mu.Unlock()

	logging.Info(ctx, "Purged 1P component Helm release", "name", ref.Name, "namespace", targetNamespace, "release", releaseName)
	return nil
}

// Orphan1PComponent detaches component management without modifying release values or uninstalling Helm.
func (m *K8sHelmManager) Orphan1PComponent(ctx context.Context, ref ComponentRef) error {
	if m == nil {
		return nil
	}
	targetNamespace, err := m.validateAndResolve(ref)
	if err != nil {
		return err
	}
	resolved := ComponentRef{Namespace: targetNamespace, Name: ref.Name}
	m.setUpgradeErr(resolved, nil)
	key := componentKey(targetNamespace, ref.Name)
	m.mu.Lock()
	if m.activeState != nil {
		delete(m.activeState, key)
	}
	if m.actuatedGenerations != nil {
		delete(m.actuatedGenerations, key)
	}
	delete(m.componentChartPaths, key)
	m.clearRepairLocked(key)
	m.mu.Unlock()
	logging.Info(ctx, "Orphaned 1P component Helm values", "name", ref.Name, "namespace", targetNamespace)
	return nil
}

// IsComponentActuated returns true if the component is active and was actuated at the given generation without errors.
func (m *K8sHelmManager) IsComponentActuated(ref ComponentRef, generation int64) bool {
	if m == nil || ref.Name == "" || ref.Namespace == "" {
		return false
	}
	key := componentKey(ref.Namespace, ref.Name)
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.activeState[key] {
		return false
	}
	if m.upgradeErrs != nil && m.upgradeErrs[key] != nil {
		return false
	}
	if m.actuatedGenerations == nil {
		return false
	}
	return m.actuatedGenerations[key] == generation
}

// RecordActuation records the generation at which the component was successfully actuated. A new generation resets
// the component's repair backoff.
func (m *K8sHelmManager) RecordActuation(ref ComponentRef, generation int64) {
	if m == nil || ref.Name == "" || ref.Namespace == "" {
		return
	}
	key := componentKey(ref.Namespace, ref.Name)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.actuatedGenerations == nil {
		m.actuatedGenerations = make(map[string]int64)
	}
	if m.actuatedGenerations[key] != generation {
		delete(m.repairBackoff, key)
	}
	m.actuatedGenerations[key] = generation
}

// IsComponentActive returns true if the component is recorded as enabled.
func (m *K8sHelmManager) IsComponentActive(ref ComponentRef) bool {
	if m == nil || ref.Name == "" || ref.Namespace == "" {
		return false
	}
	key := componentKey(ref.Namespace, ref.Name)
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.activeState[key]
}

// HydrateFromCluster populates in-memory active state from the release ConfigMap (<namespace>/xmc-component-values)
// on controller startup to avoid cold-start state drift.
func (m *K8sHelmManager) HydrateFromCluster(ctx context.Context) error {
	if m == nil || m.client == nil || m.namespace == "" {
		return nil
	}
	return m.hydrateNamespaceFromConfigMap(ctx, m.namespace)
}

// HydrateComponentState synchronizes in-memory activeState and actuatedGenerations for a component
// from cluster storage and existing CR status conditions on cold start.
// Before trusting a Ready=True CR as proof of actuation, it verifies that the component's Helm release is
// actually deployed; otherwise the component is treated as not actuated so the next Ensure re-installs it.
func (m *K8sHelmManager) HydrateComponentState(ctx context.Context, ref ComponentRef, observedGeneration int64, crReady bool) error {
	if m == nil {
		return nil
	}
	if _, err := m.validateAndResolve(ref); err != nil {
		return nil
	}
	key := componentKey(ref.Namespace, ref.Name)
	m.mu.RLock()
	_, known := m.activeState[key]
	m.mu.RUnlock()
	var hydrateErr error
	if !known {
		hydrateErr = m.hydrateNamespaceFromConfigMap(ctx, ref.Namespace)
	}
	if crReady && m.wouldRecordHydratedActuation(key, observedGeneration) && m.releaseContradictsReadyCR(ctx, ref) {
		logging.Warn(ctx, "Component CR reports Ready but its Helm release is missing or failed; forcing re-actuation", "name", ref.Name, "namespace", ref.Namespace, "generation", observedGeneration)
		crReady = false
	}
	m.applyHydratedState(key, observedGeneration, crReady)
	return hydrateErr
}

// wouldRecordHydratedActuation reports whether hydrating a Ready CR at observedGeneration would record a new actuation.
func (m *K8sHelmManager) wouldRecordHydratedActuation(key string, observedGeneration int64) bool {
	if observedGeneration <= 0 {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.actuatedGenerations[key] == 0
}

// releaseContradictsReadyCR queries Helm (outside m.mu) and reports whether the release definitively contradicts a
// Ready CR: it is missing or in a terminal non-deployed state (failed, superseded, uninstalled, ...). Transient query
// errors and pending-* states keep trusting the CR: re-upgrading would only bump a revision or fail with "another
// operation is in progress"; ComponentHealth surfaces pending-* releases as HelmReleaseStuck instead.
func (m *K8sHelmManager) releaseContradictsReadyCR(ctx context.Context, ref ComponentRef) bool {
	if m.runner == nil {
		return true
	}
	rel, err := m.runner.Status(ctx, ComponentReleaseName(ref.Name), ref.Namespace)
	if errors.Is(err, driver.ErrReleaseNotFound) {
		return true
	}
	if err != nil {
		logging.Warn(ctx, "Failed to verify Helm release during hydration; trusting CR status", "name", ref.Name, "namespace", ref.Namespace, "err", err)
		return false
	}
	status := releaseStatus(rel)
	// TODO(components): Treat "uninstalling" (and other transient statuses) consistently with releaseNeedsRepair so
	// cold-start hydration doesn't force a re-actuation on bridge restart.
	return status != release.StatusDeployed && !status.IsPending()
}

func (m *K8sHelmManager) applyHydratedState(key string, observedGeneration int64, crReady bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.activeState == nil {
		m.activeState = make(map[string]bool)
	}
	if m.actuatedGenerations == nil {
		m.actuatedGenerations = make(map[string]int64)
	}
	activeInCM, hasCM := m.activeState[key]
	if !crReady {
		if observedGeneration > 0 && m.actuatedGenerations[key] == 0 {
			m.activeState[key] = false
		}
		return
	}
	if observedGeneration == 0 || !hasCM || activeInCM {
		m.activeState[key] = true
		if observedGeneration > 0 && m.actuatedGenerations[key] == 0 {
			m.actuatedGenerations[key] = observedGeneration
		}
	}
}

func (m *K8sHelmManager) hydrateNamespaceFromConfigMap(ctx context.Context, namespace string) error {
	if m == nil || m.client == nil || namespace == "" {
		return nil
	}
	cm, err := m.client.CoreV1().ConfigMaps(namespace).Get(ctx, ComponentValuesConfigMapName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.activeState == nil {
		m.activeState = make(map[string]bool)
	}
	for k, rawJSON := range cm.Data {
		compName, ok := strings.CutSuffix(k, componentValuesKeySuffix)
		if !ok {
			continue
		}
		stateKey := componentKey(namespace, compName)
		if _, exists := m.activeState[stateKey]; exists {
			continue
		}
		if enabled, ok := extractComponentEnabledFromValuesJSON(compName, rawJSON); ok {
			m.activeState[stateKey] = enabled
		}
	}
	return nil
}
