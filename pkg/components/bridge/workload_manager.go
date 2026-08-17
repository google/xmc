// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package bridge

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

// WorkloadManager defines the contract for managing 1P child workloads.
type WorkloadManager interface {
	Ensure1PWorkload(ctx context.Context, namespace string, componentName string, spec *componentspb.ComponentSpecProto) error
	Delete1PWorkload(ctx context.Context, namespace string, componentName string) error
}

// K8sWorkloadManager creates and cleans up Kubernetes Deployments and Services for 1P components.
type K8sWorkloadManager struct {
	clientset kubernetes.Interface
}

// NewK8sWorkloadManager creates a new K8sWorkloadManager.
func NewK8sWorkloadManager(clientset kubernetes.Interface) *K8sWorkloadManager {
	return &K8sWorkloadManager{clientset: clientset}
}

// Ensure1PWorkload provisions or updates the child Deployment and Service for a 1P native component.
func (m *K8sWorkloadManager) Ensure1PWorkload(ctx context.Context, namespace string, componentName string, spec *componentspb.ComponentSpecProto) error {
	if m.clientset == nil {
		return nil
	}

	port := int32(8080)
	if pStr, ok := spec.GetParameters()["port"]; ok {
		if pVal, err := strconv.Atoi(pStr); err == nil && pVal > 0 {
			port = int32(pVal)
		}
	}

	image := fmt.Sprintf("us-docker.pkg.dev/xmc-test-project-3/xmc/%s:%s", componentName, spec.GetImageTag())
	if spec.GetImageTag() == "" {
		image = fmt.Sprintf("us-docker.pkg.dev/xmc-test-project-3/xmc/%s:latest", componentName)
	}
	if img, ok := spec.GetParameters()["image"]; ok && img != "" {
		image = img
	} else if repo, ok := spec.GetParameters()["imageRepository"]; ok && repo != "" {
		tag := spec.GetImageTag()
		if tag == "" {
			tag = "latest"
		}
		image = fmt.Sprintf("%s:%s", repo, tag)
	}

	var args []string
	for k, v := range spec.GetParameters() {
		if k == "ownerUID" || k == "image" || k == "imageRepository" || k == "replicas" {
			continue
		}
		snakeKey := toSnakeCase(k)
		args = append(args, fmt.Sprintf("-%s=%s", snakeKey, v))
	}

	replicas := int32(1)
	if rStr, ok := spec.GetParameters()["replicas"]; ok {
		if rVal, err := strconv.Atoi(rStr); err == nil && rVal > 0 {
			replicas = int32(rVal)
		}
	}

	labels := map[string]string{
		"app":                          componentName,
		"app.kubernetes.io/name":       componentName,
		"app.kubernetes.io/managed-by": "xmc-bridge",
		"app.kubernetes.io/part-of":    "xmanager-cloud",
	}

	var ownerRefs []metav1.OwnerReference
	if uidStr, ok := spec.GetParameters()["ownerUID"]; ok && uidStr != "" {
		isCtrl := true
		blockDel := true
		ownerRefs = append(ownerRefs, metav1.OwnerReference{
			APIVersion:         "xmc.dev/v1alpha1",
			Kind:               "Component",
			Name:               componentName,
			UID:                types.UID(uidStr),
			Controller:         &isCtrl,
			BlockOwnerDeletion: &blockDel,
		})
	}

	desiredDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:            componentName,
			Namespace:       namespace,
			Labels:          labels,
			OwnerReferences: ownerRefs,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": componentName},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            componentName,
							Image:           image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Args:            args,
							Ports: []corev1.ContainerPort{
								{
									Name:          "http",
									ContainerPort: port,
								},
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/readyz",
										Port: intstr.FromInt(int(port)),
									},
								},
								InitialDelaySeconds: 3,
								PeriodSeconds:       5,
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/healthz",
										Port: intstr.FromInt(int(port)),
									},
								},
								InitialDelaySeconds: 5,
								PeriodSeconds:       10,
							},
						},
					},
				},
			},
		},
	}

	existingDep, err := m.clientset.AppsV1().Deployments(namespace).Get(ctx, componentName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = m.clientset.AppsV1().Deployments(namespace).Create(ctx, desiredDeployment, metav1.CreateOptions{})
		if err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create deployment for %s: %w", componentName, err)
		}
	} else if err == nil {
		desiredDeployment.ResourceVersion = existingDep.ResourceVersion
		desiredDeployment.ObjectMeta.OwnerReferences = ownerRefs
		_, err = m.clientset.AppsV1().Deployments(namespace).Update(ctx, desiredDeployment, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update deployment for %s: %w", componentName, err)
		}
	} else {
		return fmt.Errorf("failed to get deployment for %s: %w", componentName, err)
	}

	// 2. Reconcile Service
	desiredService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            componentName,
			Namespace:       namespace,
			Labels:          labels,
			OwnerReferences: ownerRefs,
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: map[string]string{"app": componentName},
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Port:       port,
					TargetPort: intstr.FromInt(int(port)),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}

	svc, err := m.clientset.CoreV1().Services(namespace).Get(ctx, componentName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = m.clientset.CoreV1().Services(namespace).Create(ctx, desiredService, metav1.CreateOptions{})
		if err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create service for %s: %w", componentName, err)
		}
	} else if err == nil {
		desiredService.ResourceVersion = svc.ResourceVersion
		desiredService.Spec.ClusterIP = svc.Spec.ClusterIP
		_, err = m.clientset.CoreV1().Services(namespace).Update(ctx, desiredService, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update service for %s: %w", componentName, err)
		}
	} else {
		return fmt.Errorf("failed to get service for %s: %w", componentName, err)
	}

	log.Printf("Successfully ensured 1P workload (Deployment & Service) for %q in namespace %q", componentName, namespace)
	return nil
}

// Delete1PWorkload tears down the child Deployment and Service for a 1P native component.
func (m *K8sWorkloadManager) Delete1PWorkload(ctx context.Context, namespace string, componentName string) error {
	if m.clientset == nil {
		return nil
	}

	propagation := metav1.DeletePropagationBackground
	delOpts := metav1.DeleteOptions{
		PropagationPolicy: &propagation,
	}

	// Delete Deployment
	err := m.clientset.AppsV1().Deployments(namespace).Delete(ctx, componentName, delOpts)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete deployment for %s: %w", componentName, err)
	}

	// Delete Service
	err = m.clientset.CoreV1().Services(namespace).Delete(ctx, componentName, delOpts)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete service for %s: %w", componentName, err)
	}

	log.Printf("Successfully deleted 1P workload (Deployment & Service) for %q in namespace %q", componentName, namespace)
	return nil
}

// FakeWorkloadManager provides an in-memory WorkloadManager implementation for tests.
type FakeWorkloadManager struct {
	mu              sync.Mutex
	ActiveWorkloads map[string]bool
	EnsureErr       error
	DeleteErr       error
}

// NewFakeWorkloadManager creates an initialized FakeWorkloadManager.
func NewFakeWorkloadManager() *FakeWorkloadManager {
	return &FakeWorkloadManager{
		ActiveWorkloads: make(map[string]bool),
	}
}

// Ensure1PWorkload records workload provisioning in memory.
func (f *FakeWorkloadManager) Ensure1PWorkload(ctx context.Context, namespace string, componentName string, spec *componentspb.ComponentSpecProto) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.EnsureErr != nil {
		return f.EnsureErr
	}
	f.ActiveWorkloads[componentName] = true
	return nil
}

// Delete1PWorkload records workload deletion in memory.
func (f *FakeWorkloadManager) Delete1PWorkload(ctx context.Context, namespace string, componentName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.DeleteErr != nil {
		return f.DeleteErr
	}
	delete(f.ActiveWorkloads, componentName)
	return nil
}

// IsWorkloadActive checks if a component workload is marked active.
func (f *FakeWorkloadManager) IsWorkloadActive(componentName string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ActiveWorkloads[componentName]
}

func toSnakeCase(s string) string {
	var result strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			result.WriteRune('_')
		}
		result.WriteRune(r)
	}
	return strings.ToLower(result.String())
}
