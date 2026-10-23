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

package helm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
)

// ComponentValuesConfigMapName is the ConfigMap storing active 1P component Helm values overrides.
const ComponentValuesConfigMapName = "xmc-component-values"

// componentValuesKeySuffix terminates every component's data key in the release ConfigMap.
const componentValuesKeySuffix = ".json"

// componentValuesKey returns the release ConfigMap data key holding componentName's Helm values JSON.
func componentValuesKey(componentName string) string {
	return componentName + componentValuesKeySuffix
}

// valuesMutation computes a component's desired values JSON from its current JSON (empty when the key is absent).
// keep reports whether the key should exist afterwards; when false, next is ignored and the key is removed.
type valuesMutation func(current string, exists bool) (next string, keep bool, err error)

func extractComponentEnabledFromValuesJSON(componentName, rawJSON string) (bool, bool) {
	if rawJSON == "" {
		return false, false
	}
	var values map[string]any
	if err := json.Unmarshal([]byte(rawJSON), &values); err != nil {
		return false, false
	}
	deploymentKey := ComponentNameToHelmKey(componentName)
	depValues, ok := values[deploymentKey].(map[string]any)
	if !ok || depValues == nil {
		return false, false
	}
	enabled, ok := depValues["enabled"].(bool)
	return enabled, ok
}

// persistComponentValues serializes the synthesized component Helm values map into JSON and writes it
// to the release ConfigMap (<namespace>/xmc-component-values) under the key "<component-name>.json".
//
// In XMC's components architecture, 1P components (such as ingestion-gateway,
// rod-data-provider, health-service) are dynamically configured via this
// ConfigMap and actuated by Helm.
func (m *K8sHelmManager) persistComponentValues(ctx context.Context, namespace, componentName string, values map[string]any) (bool, error) {
	rawJSON, err := json.Marshal(values)
	if err != nil {
		return false, fmt.Errorf("failed to marshal Helm values: %w", err)
	}
	return m.mutateComponentValues(ctx, namespace, componentName, func(string, bool) (string, bool, error) {
		return string(rawJSON), true, nil
	})
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
	deploymentKey := ComponentNameToHelmKey(componentName)
	return m.mutateComponentValues(ctx, namespace, componentName, func(current string, _ bool) (string, bool, error) {
		next, err := setDisabledValues(current, deploymentKey)
		return next, true, err
	})
}

// removeComponentValues permanently deletes "<component-name>.json" from the release ConfigMap
// (<namespace>/xmc-component-values) on component deletion/purging.
func (m *K8sHelmManager) removeComponentValues(ctx context.Context, namespace, componentName string) (bool, error) {
	return m.mutateComponentValues(ctx, namespace, componentName, func(string, bool) (string, bool, error) {
		return "", false, nil
	})
}

// mutateComponentValues applies mutate to the component's values in the release ConfigMap, creating the ConfigMap
// when it is missing and mutate keeps the key. It uses RetryOnConflict to handle concurrent updates safely and
// reports whether the ConfigMap changed.
func (m *K8sHelmManager) mutateComponentValues(ctx context.Context, namespace, componentName string, mutate valuesMutation) (bool, error) {
	if m.client == nil {
		return false, errors.New("kubernetes client is not initialized")
	}
	var modified bool
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var err error
		modified, err = m.applyValuesMutation(ctx, namespace, componentValuesKey(componentName), mutate)
		return err
	})
	if err != nil {
		return false, err
	}
	return modified, nil
}

// applyValuesMutation performs one read-modify-write attempt of mutateComponentValues.
func (m *K8sHelmManager) applyValuesMutation(ctx context.Context, namespace, key string, mutate valuesMutation) (bool, error) {
	cmClient := m.client.CoreV1().ConfigMaps(namespace)
	cm, err := cmClient.Get(ctx, ComponentValuesConfigMapName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return m.createComponentValues(ctx, namespace, key, mutate)
	}
	if err != nil {
		return false, err
	}
	current, exists := cm.Data[key]
	next, keep, err := mutate(current, exists)
	if err != nil {
		return false, err
	}
	if keep == exists && (!keep || next == current) {
		return false, nil
	}
	if !keep {
		delete(cm.Data, key)
	} else {
		if cm.Data == nil {
			cm.Data = make(map[string]string)
		}
		cm.Data[key] = next
	}
	if _, err := cmClient.Update(ctx, cm, metav1.UpdateOptions{}); err != nil {
		return false, err
	}
	return true, nil
}

// createComponentValues creates the release ConfigMap holding only key, unless mutate drops the key. A concurrent
// creation is reported as a conflict so RetryOnConflict re-runs the mutation against the existing ConfigMap.
func (m *K8sHelmManager) createComponentValues(ctx context.Context, namespace, key string, mutate valuesMutation) (bool, error) {
	next, keep, err := mutate("", false)
	if err != nil || !keep {
		return false, err
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
		Data: map[string]string{key: next},
	}
	_, err = m.client.CoreV1().ConfigMaps(namespace).Create(ctx, newCM, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return false, apierrors.NewConflict(corev1.Resource("configmaps"), ComponentValuesConfigMapName, err)
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
