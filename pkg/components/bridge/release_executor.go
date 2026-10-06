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
	"strings"
	"time"

	"xmanager_cloud/internal/logging"
)

// asyncUpgradeTimeout bounds a single asynchronous Helm upgrade executed by the background worker.
const asyncUpgradeTimeout = 5 * time.Minute

// errManagerStopped is returned for upgrades requested after Close.
var errManagerStopped = errors.New("helm manager is stopped")

// upgradeRequest encapsulates a component upgrade request for debounced asynchronous execution.
type upgradeRequest struct {
	ref       ComponentRef
	namespace string
	chartPath string
	values    map[string]any
	// seq identifies this enqueue in pendingUpgrades so only the latest request clears the in-flight marker.
	seq uint64
}

func isReleaseNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "release: not found") || strings.Contains(msg, "not found")
}

// setUpgradeErr records (or clears) the last upgrade/uninstall error for ref. Every call marks a release
// operation boundary, so it also invalidates the cached health verdict.
func (m *K8sHelmManager) setUpgradeErr(ref ComponentRef, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.healthVerifiedAt, ref.String())
	if err != nil {
		m.upgradeErrs[ref.String()] = err
		return
	}
	delete(m.upgradeErrs, ref.String())
}

// failUpgrade records err as ref's last upgrade error and returns it.
func (m *K8sHelmManager) failUpgrade(ref ComponentRef, err error) error {
	m.setUpgradeErr(ref, err)
	return err
}

// markUpgradePending records a new in-flight async upgrade for ref and returns its sequence number.
func (m *K8sHelmManager) markUpgradePending(ref ComponentRef) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
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
	if err := validateComponentRef(ref); err != nil {
		return err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.upgradeErrs[ref.String()]
}

// acquireUpgradeLock takes the semaphore that serializes Helm release operations, giving up when ctx is done.
func (m *K8sHelmManager) acquireUpgradeLock(ctx context.Context) error {
	select {
	case m.upgradeSem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *K8sHelmManager) releaseUpgradeLock() {
	select {
	case <-m.upgradeSem:
	default:
	}
}

// registerProducer reports whether the manager still accepts upgrades and, if so, registers the caller with
// producerWg so Close waits for it. Callers that get true must call m.producerWg.Done when finished.
func (m *K8sHelmManager) registerProducer() bool {
	m.stopMu.RLock()
	defer m.stopMu.RUnlock()
	if m.stopped {
		return false
	}
	m.producerWg.Add(1)
	return true
}

func (m *K8sHelmManager) triggerUpgrade(ctx context.Context, ref ComponentRef, values map[string]any) error {
	if ref.Namespace == "" {
		return m.failUpgrade(ref, errors.New("component namespace must not be empty"))
	}
	if ComponentReleaseName(ref.Name) == "" {
		return m.failUpgrade(ref, errors.New("helm release name must not be empty; cannot actuate release upgrade"))
	}
	if m.runner == nil {
		return m.failUpgrade(ref, errors.New("helm action runner is uninitialized; cannot actuate release upgrade"))
	}
	resolvedChartPath := m.resolveChartPathForComponent(ref)
	if !m.registerProducer() {
		return errManagerStopped
	}
	defer m.producerWg.Done()

	if m.syncExecution {
		return m.executeSyncUpgrade(ctx, ref, ref.Namespace, resolvedChartPath, values)
	}
	if err := ctx.Err(); err != nil {
		return m.failUpgrade(ref, err)
	}
	return m.enqueueUpgrade(ctx, upgradeRequest{ref: ref, namespace: ref.Namespace, chartPath: resolvedChartPath, values: values})
}

// enqueueUpgrade hands req to the async worker and marks it pending until the worker records its outcome.
func (m *K8sHelmManager) enqueueUpgrade(ctx context.Context, req upgradeRequest) error {
	req.seq = m.markUpgradePending(req.ref)
	select {
	case <-m.stopCh:
		m.clearUpgradePending(req.ref, req.seq)
		return errManagerStopped
	case m.upgradeChan <- req:
		return nil
	case <-ctx.Done():
		m.clearUpgradePending(req.ref, req.seq)
		return m.failUpgrade(req.ref, ctx.Err())
	}
}

func (m *K8sHelmManager) executeSyncUpgrade(ctx context.Context, ref ComponentRef, targetNamespace, chartPath string, values map[string]any) error {
	m.setUpgradeErr(ref, nil)
	if err := m.acquireUpgradeLock(ctx); err != nil {
		return m.failUpgrade(ref, err)
	}
	defer m.releaseUpgradeLock()

	if err := ctx.Err(); err != nil {
		return m.failUpgrade(ref, err)
	}
	err := m.upgradeRelease(ctx, ComponentReleaseName(ref.Name), targetNamespace, chartPath, values)
	m.setUpgradeErr(ref, err)
	return err
}

// upgradeRelease loads the component chart at chartPath (nil selects the runner's default chart) and upgrades the
// release with values. Callers must serialize release operations.
func (m *K8sHelmManager) upgradeRelease(ctx context.Context, releaseName, namespace, chartPath string, values map[string]any) error {
	chartObj, err := m.loadComponentChart(chartPath)
	if err != nil {
		return err
	}
	_, err = m.runner.Upgrade(ctx, releaseName, namespace, chartObj, values)
	return err
}

// uninstallRelease uninstalls the component's Helm release under the upgrade lock and records the outcome as the
// component's upgrade error. A release that is already gone counts as uninstalled.
func (m *K8sHelmManager) uninstallRelease(ctx context.Context, ref ComponentRef) error {
	releaseName := ComponentReleaseName(ref.Name)
	if m.runner == nil {
		logging.Warn(ctx, "Skipping Helm release uninstall because HelmActionRunner is uninitialized", "release", releaseName, "namespace", ref.Namespace, "component", ref.Name)
		return nil
	}
	if err := m.acquireUpgradeLock(ctx); err != nil {
		return m.failUpgrade(ref, err)
	}
	defer m.releaseUpgradeLock()

	_, err := m.runner.Uninstall(ctx, releaseName, ref.Namespace)
	if err != nil && !isReleaseNotFound(err) {
		m.setUpgradeErr(ref, err)
		return fmt.Errorf("failed to uninstall Helm release %q: %w", releaseName, err)
	}
	m.setUpgradeErr(ref, nil)
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
			m.processUpgradeBatch(m.collectDebouncedBatch(req))
		}
	}
}

// collectDebouncedBatch gathers the requests that arrive within debounceDuration of first. It returns early when
// the manager stops or the upgrade channel closes.
func (m *K8sHelmManager) collectDebouncedBatch(first upgradeRequest) []upgradeRequest {
	batch := []upgradeRequest{first}
	timer := time.NewTimer(m.debounceDuration)
	defer timer.Stop()
	for {
		select {
		case <-m.stopCh:
			return batch
		case req, ok := <-m.upgradeChan:
			if !ok {
				return batch
			}
			batch = append(batch, req)
		case <-timer.C:
			return batch
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
	execCtx, cancel := context.WithTimeout(m.ctx, asyncUpgradeTimeout)
	defer cancel()

	releaseName := ComponentReleaseName(req.ref.Name)
	upgradeErr := func() error {
		m.upgradeMu.Lock()
		defer m.upgradeMu.Unlock()
		return m.upgradeRelease(execCtx, releaseName, req.namespace, req.chartPath, req.values)
	}()

	m.setUpgradeErr(req.ref, upgradeErr)
	if upgradeErr != nil {
		logging.Error(execCtx, "Asynchronous Helm upgrade failed", "release", releaseName, "name", req.ref.Name, "namespace", req.namespace, "error", upgradeErr)
		return
	}
	logging.Info(execCtx, "Asynchronous Helm upgrade succeeded", "release", releaseName, "name", req.ref.Name, "namespace", req.namespace)
}
