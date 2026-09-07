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
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"

	"helm.sh/helm/v3/pkg/cli"

	"xmanager_cloud/logging/logging"
)

const (
	// DefaultNamespace is the standard Kubernetes namespace for XMC components.
	DefaultNamespace = "xmc"
	// DefaultHelmReleaseName is the standard Helm release name for XMC.
	DefaultHelmReleaseName = "xmanager-cloud"
	// ComponentValuesConfigMapName is the ConfigMap storing active 1P component Helm values overrides.
	ComponentValuesConfigMapName = "xmc-component-values"
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
	// RecordActuation records that the component was successfully actuated at the specified generation.
	RecordActuation(ref ComponentRef, generation int64)
}

// upgradeRequest encapsulates a component upgrade request for debounced asynchronous execution.
type upgradeRequest struct {
	ref       ComponentRef
	namespace string
	values    map[string]any
}

// K8sHelmManager implements HelmManager by synchronizing component values in Kubernetes ConfigMaps
// and executing Helm release upgrades via the Helm Go SDK.
type K8sHelmManager struct {
	client           kubernetes.Interface
	namespace        string
	releaseName      string
	chartPath        string
	kubeconfig       string
	hasKubeconfig    bool
	restGetter       genericclioptions.RESTClientGetter
	hasRESTGetter    bool
	runner           HelmActionRunner
	syncExecution    bool
	debounceDuration time.Duration

	mu                  sync.RWMutex
	activeState         map[string]bool
	actuatedGenerations map[string]int64
	upgradeErrs         map[string]error
	upgradeSem          chan struct{}

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

// HelmManagerOption configures a K8sHelmManager.
type HelmManagerOption func(*K8sHelmManager)

// WithActionRunner sets the HelmActionRunner for release upgrades.
func WithActionRunner(runner HelmActionRunner) HelmManagerOption {
	return func(m *K8sHelmManager) { m.runner = runner }
}

// WithChartPath sets the filesystem chart path for release upgrades.
// TODO: Support dynamic per-component chart path resolution rather than a single static path.
func WithChartPath(chartPath string) HelmManagerOption {
	return func(m *K8sHelmManager) { m.chartPath = chartPath }
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
		upgradeErrs:         make(map[string]error),
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
		mgr.runner = NewSDKHelmActionRunner(getter, WithRunnerChartPath(mgr.chartPath))
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

func (m *K8sHelmManager) setUpgradeErr(ref ComponentRef, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.upgradeErrs == nil {
		m.upgradeErrs = make(map[string]error)
	}
	if err != nil {
		m.upgradeErrs[ref.String()] = err
		return
	}
	delete(m.upgradeErrs, ref.String())
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

	m.stopMu.RLock()
	if m.stopped {
		m.stopMu.RUnlock()
		return errors.New("helm manager is stopped")
	}
	m.producerWg.Add(1)
	m.stopMu.RUnlock()
	defer m.producerWg.Done()

	if m.syncExecution {
		return m.executeSyncUpgrade(ctx, ref, targetNamespace, values)
	}

	select {
	case <-m.stopCh:
		return errors.New("helm manager is stopped")
	case m.upgradeChan <- upgradeRequest{ref: ref, namespace: targetNamespace, values: values}:
		return nil
	case <-ctx.Done():
		m.setUpgradeErr(ref, ctx.Err())
		return ctx.Err()
	}
}

func (m *K8sHelmManager) executeSyncUpgrade(ctx context.Context, ref ComponentRef, targetNamespace string, values map[string]any) error {
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
	_, err := m.runner.Upgrade(ctx, releaseName, targetNamespace, nil, values)
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
		_, err := m.runner.Upgrade(execCtx, releaseName, req.namespace, nil, req.values)
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
	if !modified && m.IsComponentActive(resolved) && m.GetUpgradeError(resolved) == nil {
		return nil
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

	logging.Info(ctx, "Ensured 1P component Helm values and triggered upgrade", "name", ref.Name, "namespace", targetNamespace, "release", ComponentReleaseName(ref.Name), "enabled", opts.Enabled)
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
	m.mu.Unlock()
	logging.Info(ctx, "Orphaned 1P component Helm values", "name", ref.Name, "namespace", targetNamespace)
	return nil
}

// IsComponentActuated returns true if the component is active and was actuated at the given generation without errors.
// TODO: Hydrate actuatedGenerations from existing CR status and cluster release storage on controller startup
// to eliminate redundant cold-start release metadata increments.
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

// RecordActuation records the generation at which the component was successfully actuated.
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

// persistComponentValues serializes the synthesized component Helm values map into JSON and writes it
// to the release ConfigMap (<namespace>/xmc-component-values) under the key "<component-name>.json".
//
// In XMC's components architecture, 1P components (such as ingestion-gateway,
// rod-data-provider, health-service) are dynamically configured via this
// ConfigMap and actuated by Helm. Uses RetryOnConflict to handle concurrent updates safely.
func (m *K8sHelmManager) persistComponentValues(ctx context.Context, namespace, componentName string, values map[string]any) (bool, error) {
	if m == nil {
		return false, nil
	}
	if m.client == nil {
		return false, errors.New("kubernetes client is not initialized")
	}

	rawJSON, err := json.Marshal(values)
	if err != nil {
		return false, fmt.Errorf("failed to marshal Helm values: %w", err)
	}

	var modified bool
	cmClient := m.client.CoreV1().ConfigMaps(namespace)
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cm, err := cmClient.Get(ctx, ComponentValuesConfigMapName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			newCM := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ComponentValuesConfigMapName,
					Namespace: namespace,
					Labels: map[string]string{
						"app.kubernetes.io/managed-by": "xmc-bridge",
						"app.kubernetes.io/part-of":    m.releaseName,
					},
				},
				Data: map[string]string{
					componentName + ".json": string(rawJSON),
				},
			}
			_, createErr := cmClient.Create(ctx, newCM, metav1.CreateOptions{})
			if apierrors.IsAlreadyExists(createErr) {
				return apierrors.NewConflict(corev1.Resource("configmaps"), ComponentValuesConfigMapName, createErr)
			}
			if createErr == nil {
				modified = true
			}
			return createErr
		}
		if err != nil {
			return err
		}

		if cm.Data == nil {
			cm.Data = make(map[string]string)
		}
		key := componentName + ".json"
		if cm.Data[key] == string(rawJSON) {
			modified = false
			return nil
		}
		cm.Data[key] = string(rawJSON)
		_, updateErr := cmClient.Update(ctx, cm, metav1.UpdateOptions{})
		if updateErr == nil {
			modified = true
		}
		return updateErr
	})
	if err != nil {
		return false, err
	}
	return modified, nil
}

func setDisabledValues(existingJSON, deploymentKey string) (string, error) {
	var values map[string]any
	if existingJSON != "" {
		if err := json.Unmarshal([]byte(existingJSON), &values); err != nil {
			return "", fmt.Errorf("failed to unmarshal existing Helm values: %w", err)
		}
	}
	if values == nil {
		values = make(map[string]any)
	}

	depValues, ok := values[deploymentKey].(map[string]any)
	if !ok || depValues == nil {
		depValues = make(map[string]any)
		values[deploymentKey] = depValues
	}
	depValues["enabled"] = false

	rawJSON, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("failed to marshal Helm values: %w", err)
	}
	return string(rawJSON), nil
}

// disableComponentValues sets enabled: false in the component's Helm values within the release ConfigMap
// (<namespace>/xmc-component-values), preserving any existing parameters while deactivating the component.
func (m *K8sHelmManager) disableComponentValues(ctx context.Context, namespace, componentName string) (bool, error) {
	if m == nil {
		return false, nil
	}
	if m.client == nil {
		return false, errors.New("kubernetes client is not initialized")
	}

	var modified bool
	deploymentKey := ComponentNameToHelmKey(componentName)
	cmClient := m.client.CoreV1().ConfigMaps(namespace)
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cm, err := cmClient.Get(ctx, ComponentValuesConfigMapName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			rawJSON, err := setDisabledValues("", deploymentKey)
			if err != nil {
				return err
			}
			newCM := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ComponentValuesConfigMapName,
					Namespace: namespace,
					Labels: map[string]string{
						"app.kubernetes.io/managed-by": "xmc-bridge",
						"app.kubernetes.io/part-of":    m.releaseName,
					},
				},
				Data: map[string]string{
					componentName + ".json": rawJSON,
				},
			}
			_, createErr := cmClient.Create(ctx, newCM, metav1.CreateOptions{})
			if apierrors.IsAlreadyExists(createErr) {
				return apierrors.NewConflict(corev1.Resource("configmaps"), ComponentValuesConfigMapName, createErr)
			}
			if createErr == nil {
				modified = true
			}
			return createErr
		}
		if err != nil {
			return err
		}

		if cm.Data == nil {
			cm.Data = make(map[string]string)
		}

		key := componentName + ".json"
		rawJSON, err := setDisabledValues(cm.Data[key], deploymentKey)
		if err != nil {
			return err
		}

		if cm.Data[key] == rawJSON {
			modified = false
			return nil
		}
		cm.Data[key] = rawJSON
		_, updateErr := cmClient.Update(ctx, cm, metav1.UpdateOptions{})
		if updateErr == nil {
			modified = true
		}
		return updateErr
	})
	if err != nil {
		return false, err
	}
	return modified, nil
}

// removeComponentValues permanently deletes "<component-name>.json" from the release ConfigMap
// (<namespace>/xmc-component-values) on component deletion/purging.
func (m *K8sHelmManager) removeComponentValues(ctx context.Context, namespace, componentName string) (bool, error) {
	if m == nil {
		return false, nil
	}
	if m.client == nil {
		return false, errors.New("kubernetes client is not initialized")
	}

	var modified bool
	cmClient := m.client.CoreV1().ConfigMaps(namespace)
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cm, err := cmClient.Get(ctx, ComponentValuesConfigMapName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			modified = false
			return nil
		}
		if err != nil {
			return err
		}

		if cm.Data == nil {
			modified = false
			return nil
		}
		key := componentName + ".json"
		if _, exists := cm.Data[key]; !exists {
			modified = false
			return nil
		}
		delete(cm.Data, key)
		_, updateErr := cmClient.Update(ctx, cm, metav1.UpdateOptions{})
		if updateErr == nil {
			modified = true
		}
		return updateErr
	})
	if err != nil {
		return false, err
	}
	return modified, nil
}
