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
	"reflect"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// K8sWorkloadManager implements WorkloadManager using Kubernetes client-go.
type K8sWorkloadManager struct {
	clientset kubernetes.Interface
}

var _ WorkloadManager = (*K8sWorkloadManager)(nil)

// NewK8sWorkloadManager constructs a new K8sWorkloadManager.
func NewK8sWorkloadManager(clientset kubernetes.Interface) (*K8sWorkloadManager, error) {
	if clientset == nil {
		return nil, errors.New("clientset cannot be nil")
	}
	return &K8sWorkloadManager{clientset: clientset}, nil
}

// Ensure1PWorkload reconciles the Deployment and Service for a 1P component workload.
func (m *K8sWorkloadManager) Ensure1PWorkload(ctx context.Context, namespace, componentName string, opts WorkloadOptions) error {
	if namespace == "" {
		return errors.New("namespace cannot be empty")
	}
	if componentName == "" {
		return errors.New("component name cannot be empty")
	}

	dep, err := buildDeployment(namespace, componentName, opts)
	if err != nil {
		return fmt.Errorf("failed to build deployment for %s/%s: %w", namespace, componentName, err)
	}

	svc, err := buildService(namespace, componentName, opts)
	if err != nil {
		return fmt.Errorf("failed to build service for %s/%s: %w", namespace, componentName, err)
	}

	if err := m.reconcileDeployment(ctx, namespace, componentName, dep); err != nil {
		return fmt.Errorf("failed to reconcile deployment for %s/%s: %w", namespace, componentName, err)
	}

	if err := m.reconcileService(ctx, namespace, componentName, svc); err != nil {
		return fmt.Errorf("failed to reconcile service for %s/%s: %w", namespace, componentName, err)
	}

	return nil
}

// Delete1PWorkload deletes the child Deployment and Service for the component.
func (m *K8sWorkloadManager) Delete1PWorkload(ctx context.Context, namespace, componentName string) error {
	if m.clientset == nil {
		return nil
	}

	prop := metav1.DeletePropagationBackground
	deleteOpts := metav1.DeleteOptions{
		PropagationPolicy: &prop,
	}

	if err := m.clientset.AppsV1().Deployments(namespace).Delete(ctx, componentName, deleteOpts); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete deployment for %s/%s: %w", namespace, componentName, err)
	}

	if err := m.clientset.CoreV1().Services(namespace).Delete(ctx, componentName, deleteOpts); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete service for %s/%s: %w", namespace, componentName, err)
	}

	return nil
}

func reconcileResource[T any](
	ctx context.Context,
	getFunc func() (T, error),
	createFunc func(T) (T, error),
	updateFunc func(T) (T, error),
	prepareUpdate func(T, T) bool,
	desired T,
) error {
	existing, err := getFunc()
	if apierrors.IsNotFound(err) {
		_, createErr := createFunc(desired)
		if createErr != nil && !apierrors.IsAlreadyExists(createErr) {
			return createErr
		}
		return nil
	}
	if err != nil {
		return err
	}

	if changed := prepareUpdate(existing, desired); changed {
		_, updateErr := updateFunc(existing)
		if updateErr != nil {
			return updateErr
		}
	}
	return nil
}

func (m *K8sWorkloadManager) reconcileDeployment(ctx context.Context, namespace, componentName string, desired *appsv1.Deployment) error {
	depClient := m.clientset.AppsV1().Deployments(namespace)
	return reconcileResource(
		ctx,
		func() (*appsv1.Deployment, error) {
			return depClient.Get(ctx, componentName, metav1.GetOptions{})
		},
		func(d *appsv1.Deployment) (*appsv1.Deployment, error) {
			return depClient.Create(ctx, d, metav1.CreateOptions{})
		},
		func(d *appsv1.Deployment) (*appsv1.Deployment, error) {
			return depClient.Update(ctx, d, metav1.UpdateOptions{})
		},
		func(existing, desired *appsv1.Deployment) bool {
			changed := false
			if !reflect.DeepEqual(existing.Labels, desired.Labels) {
				existing.Labels = desired.Labels
				changed = true
			}
			if !reflect.DeepEqual(existing.OwnerReferences, desired.OwnerReferences) {
				existing.OwnerReferences = desired.OwnerReferences
				changed = true
			}
			if !equalInt32Ptr(existing.Spec.Replicas, desired.Spec.Replicas) {
				existing.Spec.Replicas = desired.Spec.Replicas
				changed = true
			}
			if !reflect.DeepEqual(existing.Spec.Template.Labels, desired.Spec.Template.Labels) ||
				!equalInt64Ptr(existing.Spec.Template.Spec.TerminationGracePeriodSeconds, desired.Spec.Template.Spec.TerminationGracePeriodSeconds) ||
				len(existing.Spec.Template.Spec.Containers) != len(desired.Spec.Template.Spec.Containers) {
				existing.Spec.Template = desired.Spec.Template
				changed = true
			} else if len(existing.Spec.Template.Spec.Containers) > 0 {
				eConn := existing.Spec.Template.Spec.Containers[0]
				dConn := desired.Spec.Template.Spec.Containers[0]
				if eConn.Name != dConn.Name ||
					eConn.Image != dConn.Image ||
					!reflect.DeepEqual(eConn.Ports, dConn.Ports) ||
					!reflect.DeepEqual(eConn.Args, dConn.Args) ||
					!reflect.DeepEqual(eConn.Lifecycle, dConn.Lifecycle) {
					existing.Spec.Template = desired.Spec.Template
					changed = true
				}
			}
			return changed
		},
		desired,
	)
}

func equalInt32Ptr(a, b *int32) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func equalInt64Ptr(a, b *int64) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func (m *K8sWorkloadManager) reconcileService(ctx context.Context, namespace, componentName string, desired *corev1.Service) error {
	svcClient := m.clientset.CoreV1().Services(namespace)
	return reconcileResource(
		ctx,
		func() (*corev1.Service, error) {
			return svcClient.Get(ctx, componentName, metav1.GetOptions{})
		},
		func(s *corev1.Service) (*corev1.Service, error) {
			return svcClient.Create(ctx, s, metav1.CreateOptions{})
		},
		func(s *corev1.Service) (*corev1.Service, error) {
			return svcClient.Update(ctx, s, metav1.UpdateOptions{})
		},
		func(existing, desired *corev1.Service) bool {
			desired.Spec.ClusterIP = existing.Spec.ClusterIP
			desired.Spec.ClusterIPs = existing.Spec.ClusterIPs
			changed := false
			if !reflect.DeepEqual(existing.Labels, desired.Labels) {
				existing.Labels = desired.Labels
				changed = true
			}
			if !reflect.DeepEqual(existing.OwnerReferences, desired.OwnerReferences) {
				existing.OwnerReferences = desired.OwnerReferences
				changed = true
			}
			if !reflect.DeepEqual(existing.Spec.Ports, desired.Spec.Ports) {
				existing.Spec.Ports = desired.Spec.Ports
				changed = true
			}
			if !reflect.DeepEqual(existing.Spec.Selector, desired.Spec.Selector) {
				existing.Spec.Selector = desired.Spec.Selector
				changed = true
			}
			if existing.Spec.Type != desired.Spec.Type {
				existing.Spec.Type = desired.Spec.Type
				changed = true
			}
			return changed
		},
		desired,
	)
}
