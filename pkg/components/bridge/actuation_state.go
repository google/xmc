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
	key := componentKey(ref.Namespace, ref.Name)
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
	key := componentKey(ref.Namespace, ref.Name)
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
	if err := validateComponentRef(ref); err != nil {
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

// forgetComponentLocked drops the component's actuation and repair state after a teardown and records it as inactive,
// so namespace hydration never re-activates it from the values ConfigMap (Orphan keeps enabled values there). Delete
// keeps the chart path override (keepInactive); Purge and Orphan drop it. Callers must hold m.mu.
func (m *K8sHelmManager) forgetComponentLocked(key string, keepInactive bool) {
	delete(m.actuatedGenerations, key)
	m.clearRepairLocked(key)
	m.activeState[key] = false
	if !keepInactive {
		delete(m.componentChartPaths, key)
	}
}

// applyHydratedState records the hydrated activation and actuated generation for a component, never overwriting an
// actuation already recorded in memory.
func (m *K8sHelmManager) applyHydratedState(key string, observedGeneration int64, crReady bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
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
