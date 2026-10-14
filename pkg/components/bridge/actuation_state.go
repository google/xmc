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
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage/driver"

	"xmanager_cloud/internal/logging"
)

// IsComponentActuated returns true if the component is active and was actuated at the given generation without errors.
func (m *K8sHelmManager) IsComponentActuated(ref ComponentRef, generation int64) bool {
	if m == nil || ref.Name == "" || ref.Namespace == "" {
		return false
	}
	key := componentStateKey(ref)
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.activeState[key] && m.upgradeErrs[key] == nil && m.actuatedGenerations[key] == generation
}

// RecordActuation records the generation at which the component was successfully actuated. A new generation resets
// the component's repair backoff.
func (m *K8sHelmManager) RecordActuation(ref ComponentRef, generation int64) {
	if m == nil || ref.Name == "" || ref.Namespace == "" {
		return
	}
	key := componentStateKey(ref)
	m.mu.Lock()
	defer m.mu.Unlock()
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
	key := componentStateKey(ref)
	m.mu.RLock()
	defer m.mu.RUnlock()
	if active, ok := m.activeState[key]; ok {
		return active
	}
	return m.activeState[componentKey(ref.Namespace, ref.Name)]
}

// isKnownInactiveIncarnation reports whether ref's own CR incarnation is recorded in memory as inactive. Unlike
// IsComponentActive, it ignores the namespace-level entry hydrated from the values ConfigMap on cold start, which
// cannot prove that this incarnation's release was already uninstalled.
func (m *K8sHelmManager) isKnownInactiveIncarnation(ref ComponentRef) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	active, known := m.activeState[componentStateKey(ref)]
	return known && !active
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
	if err := validateComponentRef(ref); err != nil {
		return nil
	}
	key := componentStateKey(ref)
	baseKey := componentKey(ref.Namespace, ref.Name)
	m.mu.RLock()
	_, known := m.activeState[key]
	if !known {
		_, known = m.activeState[baseKey]
	}
	m.mu.RUnlock()
	var hydrateErr error
	if !known {
		hydrateErr = m.hydrateNamespaceFromConfigMap(ctx, ref.Namespace)
	}
	if crReady && m.wouldRecordHydratedActuation(key, observedGeneration) && m.releaseContradictsReadyCR(ctx, ref) {
		logging.Warn(ctx, "Component CR reports Ready but its Helm release is missing or failed; forcing re-actuation", "name", ref.Name, "namespace", ref.Namespace, "generation", observedGeneration)
		crReady = false
	}
	m.applyHydratedState(key, baseKey, observedGeneration, crReady)
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
	rel, err := m.runner.Status(ctx, ComponentReleaseNameForRef(ref), ref.Namespace)
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

// forgetIncarnationLocked drops all in-memory state of the given CR incarnation without touching the namespace-level
// component entry or chart path override. Callers must hold m.mu.
func (m *K8sHelmManager) forgetIncarnationLocked(ref ComponentRef) {
	m.dropIncarnationStateLocked(componentStateKey(ref))
}

// dropIncarnationStateLocked deletes the activation, actuation, upgrade, health and repair state stored under a
// UID-scoped incarnation key. Dropping the pending upgrade marks any queued upgrade for that incarnation as
// superseded. Callers must hold m.mu.
func (m *K8sHelmManager) dropIncarnationStateLocked(key string) {
	delete(m.activeState, key)
	delete(m.actuatedGenerations, key)
	delete(m.upgradeErrs, key)
	delete(m.pendingUpgrades, key)
	delete(m.healthVerifiedAt, key)
	m.clearRepairLocked(key)
}

// retireOtherIncarnationsLocked drops the state of every incarnation of ref's component other than ref itself, and
// the namespace-level activation entry hydrated from the values ConfigMap. Kubernetes keeps at most one live CR per
// namespace/name, so once ref is actuated any other incarnation is stale (e.g. its teardown never ran) and must
// neither block ref's teardown nor run a queued upgrade. Callers must hold m.mu.
func (m *K8sHelmManager) retireOtherIncarnationsLocked(ref ComponentRef) {
	if ref.UID == "" {
		return
	}
	target := componentStateKey(ref)
	prefix := componentKey(ref.Namespace, ref.Name) + "@"
	for _, key := range m.incarnationKeysLocked(prefix) {
		if key != target {
			m.dropIncarnationStateLocked(key)
		}
	}
	delete(m.activeState, componentKey(ref.Namespace, ref.Name))
}

// ForgetComponent evicts all in-memory state of every incarnation of the namespace/name component, including its
// namespace-level activation entry and chart path override. The bridge calls it once the Component CR is gone, so
// state of incarnations whose teardown never ran (ghost incarnations) does not leak. The Helm release and the values
// ConfigMap are left untouched.
func (m *K8sHelmManager) ForgetComponent(namespace, name string) {
	if m == nil || namespace == "" || name == "" {
		return
	}
	baseKey := componentKey(namespace, name)
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, key := range m.incarnationKeysLocked(baseKey + "@") {
		m.dropIncarnationStateLocked(key)
	}
	m.dropIncarnationStateLocked(baseKey)
	delete(m.componentChartPaths, baseKey)
}

// incarnationKeysLocked returns the distinct UID-scoped keys with the given namespace/name@ prefix that hold any
// per-incarnation state. Callers must hold m.mu.
func (m *K8sHelmManager) incarnationKeysLocked(prefix string) []string {
	seen := make(map[string]bool)
	var keys []string
	add := func(key string) {
		if !seen[key] && strings.HasPrefix(key, prefix) {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	for key := range m.activeState {
		add(key)
	}
	for key := range m.actuatedGenerations {
		add(key)
	}
	for key := range m.upgradeErrs {
		add(key)
	}
	for key := range m.pendingUpgrades {
		add(key)
	}
	for key := range m.healthVerifiedAt {
		add(key)
	}
	for key := range m.repairNeeded {
		add(key)
	}
	for key := range m.repairBackoff {
		add(key)
	}
	return keys
}

// forgetComponentLocked drops the component's actuation and repair state after a teardown and records both the CR
// incarnation and the namespace-level entry as inactive, so namespace hydration never re-activates it from the values
// ConfigMap (Orphan keeps enabled values there). Delete keeps the chart path override (keepInactive); Purge and Orphan
// drop it. Callers must hold m.mu.
func (m *K8sHelmManager) forgetComponentLocked(ref ComponentRef, keepInactive bool) {
	key := componentStateKey(ref)
	baseKey := componentKey(ref.Namespace, ref.Name)
	delete(m.actuatedGenerations, key)
	m.clearRepairLocked(key)
	m.retireOtherIncarnationsLocked(ref)
	m.activeState[key] = false
	m.activeState[baseKey] = false
	if !keepInactive {
		delete(m.componentChartPaths, baseKey)
	}
}

// applyHydratedState records the hydrated activation and actuated generation for a component, never overwriting an
// actuation already recorded in memory.
func (m *K8sHelmManager) applyHydratedState(key, baseKey string, observedGeneration int64, crReady bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	activeInCM, hasCM := m.activeState[key]
	if !hasCM {
		activeInCM, hasCM = m.activeState[baseKey]
	}
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

// hydrateNamespaceFromConfigMap records the enablement of every component in the namespace's values ConfigMap that
// has no in-memory state yet; torn-down components stay recorded as inactive. Callers must pass a non-empty namespace.
func (m *K8sHelmManager) hydrateNamespaceFromConfigMap(ctx context.Context, namespace string) error {
	if m.client == nil {
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
