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

// Package bridge implements the GitOps Bridge Controller reconciliation loop,
// probers, and workload managers for the XMC Components System.
package bridge

import (
	"context"
	"fmt"
	"log"
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

// WorkloadOptions carries optional parameters and annotations for child workloads.
type WorkloadOptions struct {
	OwnerUID            types.UID
	ImageTag            string
	Parameters          map[string]string
	DrainTimeoutSeconds int32
}

// WorkloadManager defines the abstraction for provisioning and destroying 1P native workloads.
type WorkloadManager interface {
	Ensure1PWorkload(ctx context.Context, namespace string, componentName string, spec *componentspb.ComponentSpec) error
	Delete1PWorkload(ctx context.Context, namespace string, componentName string) error
}

// K8sWorkloadManager manages 1P child Deployment and Service resources via Kubernetes API.
type K8sWorkloadManager struct {
	clientset kubernetes.Interface
}

// NewK8sWorkloadManager creates a new K8sWorkloadManager instance.
func NewK8sWorkloadManager(clientset kubernetes.Interface) *K8sWorkloadManager {
	return &K8sWorkloadManager{
		clientset: clientset,
	}
}

// Ensure1PWorkload reconciles the child Deployment and Service for a 1P native component.
func (m *K8sWorkloadManager) Ensure1PWorkload(ctx context.Context, namespace string, componentName string, spec *componentspb.ComponentSpec) error {
	if m.clientset == nil {
		return fmt.Errorf("kubernetes clientset is uninitialized")
	}
	if componentName == "" {
		return fmt.Errorf("componentName must not be empty")
	}
	if spec == nil {
		return fmt.Errorf("spec must not be nil")
	}

	desiredDeployment, desiredService := buildDesiredWorkloads(namespace, componentName, spec)

	if err := m.reconcileDeployment(ctx, namespace, componentName, desiredDeployment); err != nil {
		return fmt.Errorf("failed to reconcile deployment for %s: %w", componentName, err)
	}

	if err := m.reconcileService(ctx, namespace, componentName, desiredService); err != nil {
		return fmt.Errorf("failed to reconcile service for %s: %w", componentName, err)
	}

	log.Printf("Successfully ensured 1P workload for %q in namespace %q", componentName, namespace)
	return nil
}

// Delete1PWorkload tears down the child Deployment and Service for a 1P native component.
func (m *K8sWorkloadManager) Delete1PWorkload(ctx context.Context, namespace string, componentName string) error {
	if m.clientset == nil {
		return fmt.Errorf("kubernetes clientset is uninitialized")
	}
	if componentName == "" {
		return fmt.Errorf("componentName must not be empty")
	}

	propagation := metav1.DeletePropagationBackground
	delOpts := metav1.DeleteOptions{PropagationPolicy: &propagation}

	if err := m.clientset.AppsV1().Deployments(namespace).Delete(ctx, componentName, delOpts); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete deployment for %s: %w", componentName, err)
	}

	if err := m.clientset.CoreV1().Services(namespace).Delete(ctx, componentName, delOpts); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete service for %s: %w", componentName, err)
	}

	log.Printf("Successfully deleted 1P workload for %q in namespace %q", componentName, namespace)
	return nil
}

func (m *K8sWorkloadManager) reconcileDeployment(ctx context.Context, namespace, name string, desired *appsv1.Deployment) error {
	existing, err := m.clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = m.clientset.AppsV1().Deployments(namespace).Create(ctx, desired, metav1.CreateOptions{})
		if err != nil && !apierrors.IsAlreadyExists(err) {
			return err
		}
		return nil
	}
	if err != nil {
		return err
	}

	desired.ResourceVersion = existing.ResourceVersion
	_, err = m.clientset.AppsV1().Deployments(namespace).Update(ctx, desired, metav1.UpdateOptions{})
	return err
}

func (m *K8sWorkloadManager) reconcileService(ctx context.Context, namespace, name string, desired *corev1.Service) error {
	existing, err := m.clientset.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = m.clientset.CoreV1().Services(namespace).Create(ctx, desired, metav1.CreateOptions{})
		if err != nil && !apierrors.IsAlreadyExists(err) {
			return err
		}
		return nil
	}
	if err != nil {
		return err
	}

	desired.ResourceVersion = existing.ResourceVersion
	desired.Spec.ClusterIP = existing.Spec.ClusterIP
	_, err = m.clientset.CoreV1().Services(namespace).Update(ctx, desired, metav1.UpdateOptions{})
	return err
}

func buildDesiredWorkloads(namespace, name string, spec *componentspb.ComponentSpec) (*appsv1.Deployment, *corev1.Service) {
	labels := map[string]string{
		"app":                          name,
		"app.kubernetes.io/name":       name,
		"app.kubernetes.io/managed-by": "xmc-bridge",
		"app.kubernetes.io/part-of":    "xmanager-cloud",
	}

	port := int32(8080)
	image := fmt.Sprintf("us-docker.pkg.dev/xmc-test-project-3/xmc/%s:%s", name, spec.GetVersion())
	if spec.GetVersion() == "" {
		image = fmt.Sprintf("us-docker.pkg.dev/xmc-test-project-3/xmc/%s:latest", name)
	}

	replicas := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            name,
							Image:           image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Ports:           []corev1.ContainerPort{{Name: "http", ContainerPort: port}},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{Path: "/readyz", Port: intstr.FromInt(int(port))},
								},
								InitialDelaySeconds: 3,
								PeriodSeconds:       5,
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{Path: "/healthz", Port: intstr.FromInt(int(port))},
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

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: map[string]string{"app": name},
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

	return dep, svc
}

// FakeWorkloadManager provides an in-memory WorkloadManager implementation for tests.
type FakeWorkloadManager struct {
	mu              sync.Mutex
	ActiveWorkloads map[string]bool
	EnsureCalls     int
	DeleteCalls     int
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
func (f *FakeWorkloadManager) Ensure1PWorkload(ctx context.Context, namespace string, componentName string, spec *componentspb.ComponentSpec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.EnsureCalls++
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
	f.DeleteCalls++
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

// SetEnsureErr sets an error to be returned by Ensure1PWorkload.
func (f *FakeWorkloadManager) SetEnsureErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.EnsureErr = err
}

// SetDeleteErr sets an error to be returned by Delete1PWorkload.
func (f *FakeWorkloadManager) SetDeleteErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.DeleteErr = err
}

// Reset clears recorded state and call counters.
func (f *FakeWorkloadManager) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ActiveWorkloads = make(map[string]bool)
	f.EnsureCalls = 0
	f.DeleteCalls = 0
	f.EnsureErr = nil
	f.DeleteErr = nil
}
