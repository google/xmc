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

	"xmanager_cloud/logging/logging"
)

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
