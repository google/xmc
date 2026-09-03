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
	"sync"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"

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
	// Delete1PComponent sets the component's enablement flag to false in the Helm release ConfigMap.
	Delete1PComponent(ctx context.Context, ref ComponentRef) error
	// Purge1PComponent permanently removes the component values from the release ConfigMap.
	Purge1PComponent(ctx context.Context, ref ComponentRef) error
	// Orphan1PComponent retains the component values while detaching bridge ownership.
	Orphan1PComponent(ctx context.Context, ref ComponentRef) error
	// IsComponentActive checks if the component is currently marked enabled in the Helm release.
	IsComponentActive(ref ComponentRef) bool
}

// K8sHelmManager implements HelmManager by synchronizing component values in Kubernetes ConfigMaps
// and tracking active 1P component states for Helm release actuation.
type K8sHelmManager struct {
	client      kubernetes.Interface
	namespace   string
	releaseName string
	mu          sync.RWMutex
	activeState map[string]bool
}

// NewK8sHelmManager constructs an initialized K8sHelmManager.
func NewK8sHelmManager(client kubernetes.Interface, namespace, releaseName string) *K8sHelmManager {
	if releaseName == "" {
		releaseName = DefaultHelmReleaseName
	}
	if namespace == "" {
		namespace = DefaultNamespace
	}
	return &K8sHelmManager{
		client:      client,
		namespace:   namespace,
		releaseName: releaseName,
		activeState: make(map[string]bool),
	}
}

func componentKey(namespace, componentName string) string {
	return namespace + "/" + componentName
}

func (m *K8sHelmManager) validateAndResolve(ref ComponentRef) (string, error) {
	if ref.Name == "" {
		return "", errors.New("component name must not be empty")
	}
	return m.resolveNamespace(ref.Namespace), nil
}

// Ensure1PComponent sets the component values in the release ConfigMap and records component enablement.
func (m *K8sHelmManager) Ensure1PComponent(ctx context.Context, ref ComponentRef, opts HelmComponentOptions) error {
	if m == nil {
		return nil
	}
	targetNamespace, err := m.validateAndResolve(ref)
	if err != nil {
		return err
	}
	values := BuildHelmValues(opts)

	if err := m.persistComponentValues(ctx, targetNamespace, ref.Name, values); err != nil {
		return fmt.Errorf("failed to persist Helm values for %q: %w", ref.Name, err)
	}

	key := componentKey(targetNamespace, ref.Name)
	m.mu.Lock()
	if m.activeState == nil {
		m.activeState = make(map[string]bool)
	}
	m.activeState[key] = opts.Enabled
	m.mu.Unlock()

	logging.Info(ctx, "Ensured 1P component Helm values", "name", ref.Name, "namespace", targetNamespace, "enabled", opts.Enabled)
	return nil
}

// Delete1PComponent disables the component values in the release ConfigMap (enabled: false) and marks it inactive.
func (m *K8sHelmManager) Delete1PComponent(ctx context.Context, ref ComponentRef) error {
	if m == nil {
		return nil
	}
	targetNamespace, err := m.validateAndResolve(ref)
	if err != nil {
		return err
	}

	if err := m.disableComponentValues(ctx, targetNamespace, ref.Name); err != nil {
		return fmt.Errorf("failed to disable Helm values for %q: %w", ref.Name, err)
	}

	key := componentKey(targetNamespace, ref.Name)
	m.mu.Lock()
	if m.activeState != nil {
		m.activeState[key] = false
	}
	m.mu.Unlock()

	logging.Info(ctx, "Disabled 1P component Helm values", "name", ref.Name, "namespace", targetNamespace)
	return nil
}

// Purge1PComponent permanently removes the component values from the release ConfigMap and removes it from active state.
func (m *K8sHelmManager) Purge1PComponent(ctx context.Context, ref ComponentRef) error {
	if m == nil {
		return nil
	}
	targetNamespace, err := m.validateAndResolve(ref)
	if err != nil {
		return err
	}

	if err := m.removeComponentValues(ctx, targetNamespace, ref.Name); err != nil {
		return fmt.Errorf("failed to purge Helm values for %q: %w", ref.Name, err)
	}

	key := componentKey(targetNamespace, ref.Name)
	m.mu.Lock()
	if m.activeState != nil {
		delete(m.activeState, key)
	}
	m.mu.Unlock()

	logging.Info(ctx, "Purged 1P component Helm values", "name", ref.Name, "namespace", targetNamespace)
	return nil
}

// Orphan1PComponent detaches component management without disabling values in Helm.
func (m *K8sHelmManager) Orphan1PComponent(ctx context.Context, ref ComponentRef) error {
	if m == nil {
		return nil
	}
	targetNamespace, err := m.validateAndResolve(ref)
	if err != nil {
		return err
	}
	key := componentKey(targetNamespace, ref.Name)
	m.mu.Lock()
	if m.activeState != nil {
		delete(m.activeState, key)
	}
	m.mu.Unlock()
	logging.Info(ctx, "Orphaned 1P component Helm values", "name", ref.Name, "namespace", targetNamespace)
	return nil
}

// IsComponentActive returns true if the component is recorded as enabled.
func (m *K8sHelmManager) IsComponentActive(ref ComponentRef) bool {
	if m == nil || ref.Name == "" {
		return false
	}
	targetNamespace := m.resolveNamespace(ref.Namespace)
	key := componentKey(targetNamespace, ref.Name)
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.activeState[key]
}

func (m *K8sHelmManager) resolveNamespace(ns string) string {
	if ns != "" {
		return ns
	}
	if m != nil && m.namespace != "" {
		return m.namespace
	}
	return DefaultNamespace
}

// persistComponentValues serializes the synthesized component Helm values map into JSON and writes it
// to the release ConfigMap (<namespace>/xmc-component-values) under the key "<component-name>.json".
//
// In XMC's components architecture, 1P components (such as ingestion-gateway,
// rod-data-provider, health-service) are dynamically configured via this
// ConfigMap and actuated by Helm. Uses RetryOnConflict to handle concurrent updates safely.
func (m *K8sHelmManager) persistComponentValues(ctx context.Context, namespace, componentName string, values map[string]any) error {
	if m == nil {
		return nil
	}
	if m.client == nil {
		return errors.New("kubernetes client is not initialized")
	}

	rawJSON, err := json.Marshal(values)
	if err != nil {
		return fmt.Errorf("failed to marshal Helm values: %w", err)
	}

	cmClient := m.client.CoreV1().ConfigMaps(namespace)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
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
			return createErr
		}
		if err != nil {
			return err
		}

		if cm.Data == nil {
			cm.Data = make(map[string]string)
		}
		key := componentName + ".json"
		if existing, ok := cm.Data[key]; ok && existing == string(rawJSON) {
			return nil
		}
		cm.Data[key] = string(rawJSON)
		_, updateErr := cmClient.Update(ctx, cm, metav1.UpdateOptions{})
		return updateErr
	})
}

func setDisabledValues(existingJSON, deploymentKey string) (string, error) {
	var values map[string]any
	if existingJSON != "" {
		_ = json.Unmarshal([]byte(existingJSON), &values)
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
func (m *K8sHelmManager) disableComponentValues(ctx context.Context, namespace, componentName string) error {
	if m == nil {
		return nil
	}
	if m.client == nil {
		return errors.New("kubernetes client is not initialized")
	}

	deploymentKey := ComponentNameToHelmKey(componentName)
	cmClient := m.client.CoreV1().ConfigMaps(namespace)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
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
			return nil
		}
		cm.Data[key] = rawJSON
		_, updateErr := cmClient.Update(ctx, cm, metav1.UpdateOptions{})
		return updateErr
	})
}

// removeComponentValues permanently deletes "<component-name>.json" from the release ConfigMap
// (<namespace>/xmc-component-values) on component deletion/purging.
func (m *K8sHelmManager) removeComponentValues(ctx context.Context, namespace, componentName string) error {
	if m == nil {
		return nil
	}
	if m.client == nil {
		return errors.New("kubernetes client is not initialized")
	}

	cmClient := m.client.CoreV1().ConfigMaps(namespace)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cm, err := cmClient.Get(ctx, ComponentValuesConfigMapName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}

		if cm.Data == nil {
			return nil
		}
		key := componentName + ".json"
		if _, exists := cm.Data[key]; !exists {
			return nil
		}
		delete(cm.Data, key)
		_, updateErr := cmClient.Update(ctx, cm, metav1.UpdateOptions{})
		return updateErr
	})
}
