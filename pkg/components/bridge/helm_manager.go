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

// Ensure1PComponent sets the component values in the release ConfigMap and records component enablement.
func (m *K8sHelmManager) Ensure1PComponent(ctx context.Context, namespace string, componentName string, opts HelmComponentOptions) error {
	if m == nil {
		return nil
	}
	if componentName == "" {
		return errors.New("component name must not be empty")
	}
	targetNamespace := m.resolveNamespace(namespace)
	values := BuildHelmValues(opts)

	if err := m.persistComponentValues(ctx, targetNamespace, componentName, values); err != nil {
		return fmt.Errorf("failed to persist Helm values for %q: %w", componentName, err)
	}

	m.mu.Lock()
	m.activeState[componentName] = opts.Enabled
	m.activeState[componentKey(targetNamespace, componentName)] = opts.Enabled
	m.mu.Unlock()

	logging.Info(ctx, "Ensured 1P component Helm values", "name", componentName, "namespace", targetNamespace, "enabled", opts.Enabled)
	return nil
}

// Delete1PComponent removes the component values from the release ConfigMap and removes it from active state.
func (m *K8sHelmManager) Delete1PComponent(ctx context.Context, namespace string, componentName string) error {
	if m == nil {
		return nil
	}
	if componentName == "" {
		return errors.New("component name must not be empty")
	}
	targetNamespace := m.resolveNamespace(namespace)

	if err := m.removeComponentValues(ctx, targetNamespace, componentName); err != nil {
		return fmt.Errorf("failed to remove Helm values for %q: %w", componentName, err)
	}

	m.mu.Lock()
	delete(m.activeState, componentName)
	delete(m.activeState, componentKey(targetNamespace, componentName))
	m.mu.Unlock()

	logging.Info(ctx, "Disabled 1P component Helm values", "name", componentName, "namespace", targetNamespace)
	return nil
}

// Orphan1PComponent detaches component management without disabling values in Helm.
func (m *K8sHelmManager) Orphan1PComponent(ctx context.Context, namespace string, componentName string) error {
	if m == nil {
		return nil
	}
	if componentName == "" {
		return errors.New("component name must not be empty")
	}
	targetNamespace := m.resolveNamespace(namespace)
	m.mu.Lock()
	delete(m.activeState, componentName)
	delete(m.activeState, componentKey(targetNamespace, componentName))
	m.mu.Unlock()
	logging.Info(ctx, "Orphaned 1P component Helm values", "name", componentName, "namespace", targetNamespace)
	return nil
}

// IsComponentActive returns true if the component is recorded as enabled.
func (m *K8sHelmManager) IsComponentActive(componentName string) bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.activeState[componentName] {
		return true
	}
	targetNamespace := m.resolveNamespace("")
	return m.activeState[componentKey(targetNamespace, componentName)]
}

func (m *K8sHelmManager) resolveNamespace(ns string) string {
	if ns != "" {
		return ns
	}
	if m.namespace != "" {
		return m.namespace
	}
	return DefaultNamespace
}

func (m *K8sHelmManager) persistComponentValues(ctx context.Context, namespace, componentName string, values map[string]any) error {
	if m.client == nil {
		return nil
	}

	rawJSON, err := json.Marshal(values)
	if err != nil {
		return fmt.Errorf("failed to marshal Helm values: %w", err)
	}

	cmClient := m.client.CoreV1().ConfigMaps(namespace)
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
		return createErr
	}
	if err != nil {
		return err
	}

	if cm.Data == nil {
		cm.Data = make(map[string]string)
	}
	cm.Data[componentName+".json"] = string(rawJSON)
	_, updateErr := cmClient.Update(ctx, cm, metav1.UpdateOptions{})
	return updateErr
}

func (m *K8sHelmManager) removeComponentValues(ctx context.Context, namespace, componentName string) error {
	if m.client == nil {
		return nil
	}

	cmClient := m.client.CoreV1().ConfigMaps(namespace)
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
}
