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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
)

// ComponentValuesConfigMapName is the ConfigMap storing active 1P component Helm values overrides.
const ComponentValuesConfigMapName = "xmc-component-values"

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
