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

// healthCacheTTL bounds how long a deployed-release verdict is reused before ComponentHealth re-queries Helm.
const healthCacheTTL = time.Minute

// Repair backoff rate-limits re-upgrades of a release that keeps drifting (e.g. another actor keeps deleting one of
// its objects): each repair writes a Helm revision, so repairs back off exponentially from repairBackoffBase up to
// repairBackoffMax, with up to repairBackoffJitter extra randomized delay. This is separate from, and deliberately
// longer than, transient-error retry backoff.
const (
	repairBackoffBase   = 30 * time.Second
	repairBackoffMax    = 10 * time.Minute
	repairBackoffJitter = 0.1
)

// repairAttempt records the repairs consumed for a component since its backoff was last reset.
type repairAttempt struct {
	count     int
	last      time.Time
	notBefore time.Time
}

// upgradeRequest encapsulates a component upgrade request for debounced asynchronous execution.
type upgradeRequest struct {
	ref       ComponentRef
	namespace string
	chartPath string
	values    map[string]any
	// seq identifies this enqueue in pendingUpgrades so only the latest request clears the in-flight marker.
	seq uint64
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

func isReleaseNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "release: not found") || strings.Contains(msg, "not found")
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

// setUpgradeErr records (or clears) the last upgrade/uninstall error for ref. Every call marks a release
// operation boundary, so it also invalidates the cached health verdict.
func (m *K8sHelmManager) setUpgradeErr(ref ComponentRef, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.healthVerifiedAt, ref.String())
	if m.upgradeErrs == nil {
		m.upgradeErrs = make(map[string]error)
	}
	if err != nil {
		m.upgradeErrs[ref.String()] = err
		return
	}
	delete(m.upgradeErrs, ref.String())
}

// markUpgradePending records a new in-flight async upgrade for ref and returns its sequence number.
func (m *K8sHelmManager) markUpgradePending(ref ComponentRef) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pendingUpgrades == nil {
		m.pendingUpgrades = make(map[string]uint64)
	}
	m.upgradeSeq++
	m.pendingUpgrades[ref.String()] = m.upgradeSeq
	delete(m.healthVerifiedAt, ref.String())
	return m.upgradeSeq
}

// clearUpgradePending clears the in-flight marker for ref only if seq is still the latest enqueued upgrade.
func (m *K8sHelmManager) clearUpgradePending(ref ComponentRef, seq uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pendingUpgrades[ref.String()] == seq {
		delete(m.pendingUpgrades, ref.String())
	}
}

func (m *K8sHelmManager) isUpgradePending(ref ComponentRef) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, pending := m.pendingUpgrades[ref.String()]
	return pending
}

// GetUpgradeError returns the last error encountered during an upgrade for the given component, if any.
func (m *K8sHelmManager) GetUpgradeError(ref ComponentRef) error {
	if m == nil {
		return nil
	}
	targetNamespace, err := m.validateAndResolve(ref)
	if err != nil {
		return err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.upgradeErrs[ComponentRef{Namespace: targetNamespace, Name: ref.Name}.String()]
}

func (m *K8sHelmManager) acquireUpgradeLock(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	if m.upgradeSem == nil {
		m.upgradeSem = make(chan struct{}, 1)
	}
	sem := m.upgradeSem
	m.mu.Unlock()

	select {
	case sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *K8sHelmManager) releaseUpgradeLock() {
	if m == nil {
		return
	}
	m.mu.RLock()
	sem := m.upgradeSem
	m.mu.RUnlock()
	if sem != nil {
		select {
		case <-sem:
		default:
		}
	}
}

func (m *K8sHelmManager) triggerUpgrade(ctx context.Context, ref ComponentRef, values map[string]any) error {
	if m == nil {
		return nil
	}
	if ref.Namespace == "" {
		err := errors.New("component namespace must not be empty")
		m.setUpgradeErr(ref, err)
		return err
	}
	releaseName := ComponentReleaseName(ref.Name)
	if releaseName == "" {
		err := errors.New("helm release name must not be empty; cannot actuate release upgrade")
		m.setUpgradeErr(ref, err)
		return err
	}
	if m.runner == nil {
		err := errors.New("helm action runner is uninitialized; cannot actuate release upgrade")
		m.setUpgradeErr(ref, err)
		return err
	}
	targetNamespace := ref.Namespace
	resolvedChartPath := m.resolveChartPathForComponent(ref)

	m.stopMu.RLock()
	if m.stopped {
		m.stopMu.RUnlock()
		return errors.New("helm manager is stopped")
	}
	m.producerWg.Add(1)
	m.stopMu.RUnlock()
	defer m.producerWg.Done()

	if m.syncExecution {
		return m.executeSyncUpgrade(ctx, ref, targetNamespace, resolvedChartPath, values)
	}

	if err := ctx.Err(); err != nil {
		m.setUpgradeErr(ref, err)
		return err
	}

	seq := m.markUpgradePending(ref)
	select {
	case <-m.stopCh:
		m.clearUpgradePending(ref, seq)
		return errors.New("helm manager is stopped")
	case m.upgradeChan <- upgradeRequest{ref: ref, namespace: targetNamespace, chartPath: resolvedChartPath, values: values, seq: seq}:
		return nil
	case <-ctx.Done():
		m.clearUpgradePending(ref, seq)
		m.setUpgradeErr(ref, ctx.Err())
		return ctx.Err()
	}
}

func (m *K8sHelmManager) executeSyncUpgrade(ctx context.Context, ref ComponentRef, targetNamespace, chartPath string, values map[string]any) error {
	m.setUpgradeErr(ref, nil)
	releaseName := ComponentReleaseName(ref.Name)

	if err := m.acquireUpgradeLock(ctx); err != nil {
		m.setUpgradeErr(ref, err)
		return err
	}
	defer m.releaseUpgradeLock()

	if err := ctx.Err(); err != nil {
		m.setUpgradeErr(ref, err)
		return err
	}
	chartObj, loadErr := m.loadComponentChart(chartPath)
	if loadErr != nil {
		m.setUpgradeErr(ref, loadErr)
		return loadErr
	}
	_, err := m.runner.Upgrade(ctx, releaseName, targetNamespace, chartObj, values)
	m.setUpgradeErr(ref, err)
	return err
}

func (m *K8sHelmManager) uninstallRelease(ctx context.Context, releaseName, targetNamespace string, resolved ComponentRef) error {
	if m.runner == nil {
		logging.Warn(ctx, "Skipping Helm release uninstall because HelmActionRunner is uninitialized", "release", releaseName, "namespace", targetNamespace, "component", resolved.Name)
		return nil
	}
	if err := m.acquireUpgradeLock(ctx); err != nil {
		m.setUpgradeErr(resolved, err)
		return err
	}
	defer m.releaseUpgradeLock()

	_, err := m.runner.Uninstall(ctx, releaseName, targetNamespace)
	if err != nil && !isReleaseNotFound(err) {
		m.setUpgradeErr(resolved, err)
		return fmt.Errorf("failed to uninstall Helm release %q: %w", releaseName, err)
	}
	m.setUpgradeErr(resolved, nil)
	return nil
}

func (m *K8sHelmManager) syncUpgradeWorker() {
	defer m.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			logging.Error(context.Background(), "Recovered from panic in Helm upgrade worker", "panic", r)
		}
	}()
	for {
		select {
		case <-m.stopCh:
			m.drainRemainingUpgrades()
			return
		case req, ok := <-m.upgradeChan:
			if !ok {
				return
			}
			batch := []upgradeRequest{req}
			timer := time.NewTimer(m.debounceDuration)
		debounceLoop:
			for {
				select {
				case <-m.stopCh:
					timer.Stop()
					break debounceLoop
				case nextReq, ok := <-m.upgradeChan:
					if !ok {
						timer.Stop()
						break debounceLoop
					}
					batch = append(batch, nextReq)
				case <-timer.C:
					break debounceLoop
				}
			}
			timer.Stop()
			m.processUpgradeBatch(batch)
		}
	}
}

func (m *K8sHelmManager) drainRemainingUpgrades() {
	var drainBatch []upgradeRequest
drainLoop:
	for {
		select {
		case req, ok := <-m.upgradeChan:
			if !ok {
				break drainLoop
			}
			drainBatch = append(drainBatch, req)
		default:
			break drainLoop
		}
	}
	if len(drainBatch) > 0 {
		m.processUpgradeBatch(drainBatch)
	}
}

func (m *K8sHelmManager) processUpgradeBatch(batch []upgradeRequest) {
	if len(batch) == 0 || m.runner == nil {
		return
	}
	// Coalesce requests by component key, retaining the latest requested values.
	latestByComponent := make(map[string]upgradeRequest)
	var orderedKeys []string
	for _, req := range batch {
		key := componentKey(req.namespace, req.ref.Name)
		if _, exists := latestByComponent[key]; !exists {
			orderedKeys = append(orderedKeys, key)
		}
		latestByComponent[key] = req
	}

	for _, key := range orderedKeys {
		m.executeComponentUpgrade(latestByComponent[key])
	}
}

func (m *K8sHelmManager) executeComponentUpgrade(req upgradeRequest) {
	// Deferred so the marker is cleared only after setUpgradeErr records the outcome (and even on panic).
	defer m.clearUpgradePending(req.ref, req.seq)
	parentCtx := context.Background()
	if m != nil && m.ctx != nil {
		parentCtx = m.ctx
	}
	execCtx, cancel := context.WithTimeout(parentCtx, 5*time.Minute)
	defer cancel()

	releaseName := ComponentReleaseName(req.ref.Name)
	upgradeErr := func() error {
		m.upgradeMu.Lock()
		defer m.upgradeMu.Unlock()
		chartObj, loadErr := m.loadComponentChart(req.chartPath)
		if loadErr != nil {
			return loadErr
		}
		_, err := m.runner.Upgrade(execCtx, releaseName, req.namespace, chartObj, req.values)
		return err
	}()

	m.setUpgradeErr(req.ref, upgradeErr)
	if upgradeErr != nil {
		logging.Error(execCtx, "Asynchronous Helm upgrade failed", "release", releaseName, "name", req.ref.Name, "namespace", req.namespace, "error", upgradeErr)
		return
	}
	logging.Info(execCtx, "Asynchronous Helm upgrade succeeded", "release", releaseName, "name", req.ref.Name, "namespace", req.namespace)
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

// NeedsRepair returns true if the component is active, was actuated at the given generation, and its release has
// since been found missing, failed, or drifted. A pending repair for an older generation does not count: a new spec
// goes through the regular (dependency-gated) upgrade path instead.
func (m *K8sHelmManager) NeedsRepair(ref ComponentRef, generation int64) bool {
	if m == nil || ref.Name == "" || ref.Namespace == "" {
		return false
	}
	key := componentKey(ref.Namespace, ref.Name)
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.repairNeeded[key] && m.activeState[key] && m.actuatedGenerations[key] == generation
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

// ComponentHealth reports whether the component's last Helm upgrade succeeded, no upgrade is in flight, and its
// release is deployed. Deployed verdicts are cached for healthCacheTTL to avoid querying Helm on every poll.
func (m *K8sHelmManager) ComponentHealth(ctx context.Context, ref ComponentRef) (bool, string, string) {
	if m == nil {
		return false, ReasonHelmRunnerUninitialized, "Helm manager is uninitialized; cannot verify release status"
	}
	if _, err := m.validateAndResolve(ref); err != nil {
		return false, ReasonInvalidComponentRef, err.Error()
	}
	if err := m.GetUpgradeError(ref); err != nil {
		return false, ReasonHelmUpgradeFailed, fmt.Sprintf("Helm upgrade failed: %v", err)
	}
	if m.isUpgradePending(ref) {
		return false, ReasonHelmUpgradePending, "Helm upgrade is queued or in progress"
	}
	if m.runner == nil {
		return false, ReasonHelmRunnerUninitialized, "Helm action runner is uninitialized; cannot verify release status"
	}
	return m.releaseHealth(ctx, ref)
}

// releaseHealth returns the cached deployed verdict if still fresh, otherwise queries Helm, verifies that every object
// in the deployed release still exists, and caches a healthy result. A missing, terminally failed, or drifted release
// marks the component for repair so the next reconcile pass re-runs its Helm upgrade at the current generation.
func (m *K8sHelmManager) releaseHealth(ctx context.Context, ref ComponentRef) (bool, string, string) {
	releaseName := ComponentReleaseName(ref.Name)
	m.mu.RLock()
	verifiedAt, cached := m.healthVerifiedAt[ref.String()]
	m.mu.RUnlock()
	if cached && time.Since(verifiedAt) < healthCacheTTL {
		return true, ReasonComponentHealthy, fmt.Sprintf("Helm release %q deployed", releaseName)
	}
	rel, err := m.runner.Status(ctx, releaseName, ref.Namespace)
	healthy, reason, msg := evaluateReleaseHealth(releaseName, rel, err)
	if healthy {
		healthy, reason, msg = m.releaseResourceHealth(ctx, releaseName, rel)
	}
	m.recordReleaseHealth(ctx, ref, healthy, releaseNeedsRepair(reason, releaseStatus(rel)), reason)
	return healthy, reason, msg
}

// releaseResourceHealth reports drift when objects rendered by a deployed release no longer exist in the cluster.
// Lookup failures are transient: the deployed verdict is kept so an API blip never triggers a re-upgrade.
func (m *K8sHelmManager) releaseResourceHealth(ctx context.Context, releaseName string, rel *release.Release) (bool, string, string) {
	deployedMsg := fmt.Sprintf("Helm release %q deployed", releaseName)
	missing, err := m.runner.MissingResources(ctx, rel)
	if err != nil {
		logging.Warn(ctx, "Failed to verify Helm release resources; trusting deployed status", "release", releaseName, "err", err)
		return true, ReasonComponentHealthy, deployedMsg
	}
	if len(missing) == 0 {
		return true, ReasonComponentHealthy, deployedMsg
	}
	return false, ReasonHelmReleaseDrifted, fmt.Sprintf("Helm release %q is missing %d resource(s) (%s); the bridge re-applies the release, rate-limited by repair backoff", releaseName, len(missing), strings.Join(missing, ", "))
}

// recordReleaseHealth caches a healthy verdict, or marks the component for repair when the release needs re-applying
// and its repair backoff has elapsed.
func (m *K8sHelmManager) recordReleaseHealth(ctx context.Context, ref ComponentRef, healthy, repair bool, reason string) {
	key := componentKey(ref.Namespace, ref.Name)
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	if healthy {
		if m.healthVerifiedAt == nil {
			m.healthVerifiedAt = make(map[string]time.Time)
		}
		m.healthVerifiedAt[ref.String()] = now
		m.resetRepairBackoffIfStableLocked(key, now)
		return
	}
	if !repair {
		return
	}
	if attempt, ok := m.repairBackoff[key]; ok && now.Before(attempt.notBefore) {
		logging.Info(ctx, "Component Helm release needs repair; deferring re-upgrade until repair backoff elapses", "name", ref.Name, "namespace", ref.Namespace, "reason", reason, "attempts", attempt.count, "notBefore", attempt.notBefore)
		return
	}
	if m.repairNeeded == nil {
		m.repairNeeded = make(map[string]bool)
	}
	m.repairNeeded[key] = true
	logging.Warn(ctx, "Component Helm release needs repair; scheduling re-upgrade at current generation", "name", ref.Name, "namespace", ref.Namespace, "reason", reason)
}

// resetRepairBackoffIfStableLocked drops the repair backoff once the release has stayed healthy for repairBackoffMax
// after the last repair. Resetting on any healthy verdict would defeat the backoff: a repaired release is briefly
// healthy before an actor that keeps deleting its objects strikes again. Callers must hold m.mu.
func (m *K8sHelmManager) resetRepairBackoffIfStableLocked(key string, now time.Time) {
	attempt, ok := m.repairBackoff[key]
	if ok && now.Sub(attempt.last) >= repairBackoffMax {
		delete(m.repairBackoff, key)
	}
}

// recordRepairAttempt consumes the component's repair marker and arms its backoff for the next repair.
func (m *K8sHelmManager) recordRepairAttempt(ctx context.Context, ref ComponentRef) {
	key := componentKey(ref.Namespace, ref.Name)
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.repairNeeded, key)
	if m.repairBackoff == nil {
		m.repairBackoff = make(map[string]repairAttempt)
	}
	attempt := m.repairBackoff[key]
	attempt.count++
	attempt.last = now
	attempt.notBefore = now.Add(m.repairDelay(attempt.count))
	m.repairBackoff[key] = attempt
	logging.Info(ctx, "Repairing component Helm release", "name", ref.Name, "namespace", ref.Namespace, "attempt", attempt.count, "nextRepairNotBefore", attempt.notBefore)
}

// repairDelay returns the jittered backoff that must elapse after the attempt-th repair before the next one:
// repairBackoffBase doubled per attempt, capped at repairBackoffMax, plus up to repairBackoffJitter of extra delay.
func (m *K8sHelmManager) repairDelay(attempt int) time.Duration {
	delay := repairBackoffMax
	if shift := attempt - 1; shift < 16 && repairBackoffBase<<shift < repairBackoffMax {
		delay = repairBackoffBase << shift
	}
	jitter := rand.Float64
	if m.jitter != nil {
		jitter = m.jitter
	}
	return delay + time.Duration(float64(delay)*repairBackoffJitter*jitter())
}

// clearRepairLocked drops the component's repair marker and backoff. Callers must hold m.mu.
func (m *K8sHelmManager) clearRepairLocked(key string) {
	delete(m.repairNeeded, key)
	delete(m.repairBackoff, key)
}

// releaseNeedsRepair reports whether a health verdict means the release must be re-applied: it is missing, its
// objects drifted, or its latest revision is terminally failed or uninstalled. Every other verdict, including a
// release that is uninstalling, never triggers repair so the bridge cannot race another actor's Helm operation.
func releaseNeedsRepair(reason string, status release.Status) bool {
	switch reason {
	case ReasonHelmReleaseNotFound, ReasonHelmReleaseDrifted:
		return true
	case ReasonHelmReleaseNotReady:
		return status == release.StatusFailed || status == release.StatusUninstalled
	}
	return false
}

// evaluateReleaseHealth maps a Helm status query result to a health verdict, condition reason, and message.
func evaluateReleaseHealth(releaseName string, rel *release.Release, statusErr error) (bool, string, string) {
	if errors.Is(statusErr, driver.ErrReleaseNotFound) {
		return false, ReasonHelmReleaseNotFound, fmt.Sprintf("Helm release %q not found", releaseName)
	}
	if statusErr != nil {
		return false, ReasonHelmStatusUnavailable, fmt.Sprintf("failed to query Helm release %q status: %v", releaseName, statusErr)
	}
	status := releaseStatus(rel)
	if status.IsPending() {
		return false, ReasonHelmReleaseStuck, fmt.Sprintf("Helm release %q is stuck in %q with no upgrade in flight; recover with `helm rollback` (or `helm uninstall` for pending-install)", releaseName, status)
	}
	if status != release.StatusDeployed {
		return false, ReasonHelmReleaseNotReady, fmt.Sprintf("Helm release %q status is %q", releaseName, status)
	}
	return true, ReasonComponentHealthy, fmt.Sprintf("Helm release %q deployed", releaseName)
}

func releaseStatus(rel *release.Release) release.Status {
	if rel == nil || rel.Info == nil {
		return release.StatusUnknown
	}
	return rel.Info.Status
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
		if !strings.HasSuffix(k, ".json") {
			continue
		}
		compName := strings.TrimSuffix(k, ".json")
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
