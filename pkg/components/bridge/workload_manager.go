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
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

const (
	defaultImageRepoPrefix = "us-docker.pkg.dev/xmc-test-project-3/xmc"
	defaultPort            = int32(8080)
	defaultReplicas        = int32(1)
	defaultDrainSeconds    = int64(300)
	minDrainOverrideSec    = int32(5)
)

var specialParameterKeys = map[string]struct{}{
	"ownerUID":        {},
	"image":           {},
	"imageRepository": {},
	"imageTag":        {},
	"global.imageTag": {},
	"replicas":        {},
	"port":            {},
}

// WorkloadOptions holds parameter configuration for 1P child workload provisioning.
type WorkloadOptions struct {
	OwnerUID            types.UID
	ImageTag            string
	Parameters          map[string]string
	DrainTimeoutSeconds int32
}

// WorkloadManager defines the contract for managing 1P native child workloads.
type WorkloadManager interface {
	Ensure1PWorkload(ctx context.Context, namespace string, componentName string, opts WorkloadOptions) error
	Delete1PWorkload(ctx context.Context, namespace string, componentName string) error
}

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

func buildDeployment(namespace, componentName string, opts WorkloadOptions) (*appsv1.Deployment, error) {
	port, err := resolvePort(opts.Parameters)
	if err != nil {
		return nil, err
	}
	replicas, err := resolveReplicas(opts.Parameters)
	if err != nil {
		return nil, err
	}

	gracePeriod := resolveTerminationGracePeriodSeconds(opts)
	labels := buildLabels(componentName)
	podLabels := buildLabels(componentName)

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:            componentName,
			Namespace:       namespace,
			Labels:          labels,
			OwnerReferences: buildOwnerReferences(componentName, opts.OwnerUID),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": componentName,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: podLabels,
				},
				Spec: corev1.PodSpec{
					TerminationGracePeriodSeconds: &gracePeriod,
					Containers: []corev1.Container{
						{
							Name:  componentName,
							Image: resolveImage(componentName, opts),
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: port,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							Args: buildArgs(opts.Parameters),
							Lifecycle: &corev1.Lifecycle{
								PreStop: &corev1.LifecycleHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"/bin/sleep", "5"},
									},
								},
							},
						},
					},
				},
			},
		},
	}, nil
}

func buildService(namespace, componentName string, opts WorkloadOptions) (*corev1.Service, error) {
	port, err := resolvePort(opts.Parameters)
	if err != nil {
		return nil, err
	}

	labels := buildLabels(componentName)
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            componentName,
			Namespace:       namespace,
			Labels:          labels,
			OwnerReferences: buildOwnerReferences(componentName, opts.OwnerUID),
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Selector: map[string]string{
				"app": componentName,
			},
			Ports: []corev1.ServicePort{
				{
					Port:       port,
					TargetPort: intstr.FromInt32(port),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}, nil
}

func resolveTag(opts WorkloadOptions) string {
	if opts.ImageTag != "" {
		return opts.ImageTag
	}
	if opts.Parameters != nil {
		if t, ok := opts.Parameters["global.imageTag"]; ok && t != "" {
			return t
		}
		if t, ok := opts.Parameters["imageTag"]; ok && t != "" {
			return t
		}
	}
	return "latest"
}

func resolveImage(componentName string, opts WorkloadOptions) string {
	if opts.Parameters != nil {
		if img, ok := opts.Parameters["image"]; ok && img != "" {
			return img
		}
	}
	tag := resolveTag(opts)
	if opts.Parameters != nil {
		if repo, ok := opts.Parameters["imageRepository"]; ok && repo != "" {
			return fmt.Sprintf("%s:%s", repo, tag)
		}
	}
	return fmt.Sprintf("%s/%s:%s", defaultImageRepoPrefix, componentName, tag)
}

func resolvePort(params map[string]string) (int32, error) {
	if params == nil {
		return defaultPort, nil
	}
	portStr, ok := params["port"]
	if !ok || portStr == "" {
		return defaultPort, nil
	}
	p, err := strconv.ParseInt(portStr, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid port %q: %w", portStr, err)
	}
	if p <= 0 || p > 65535 {
		return 0, fmt.Errorf("invalid port %d: must be between 1 and 65535", p)
	}
	return int32(p), nil
}

func resolveReplicas(params map[string]string) (int32, error) {
	if params == nil {
		return defaultReplicas, nil
	}
	repStr, ok := params["replicas"]
	if !ok || repStr == "" {
		return defaultReplicas, nil
	}
	r, err := strconv.ParseInt(repStr, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid replicas %q: %w", repStr, err)
	}
	if r < 0 {
		return 0, fmt.Errorf("invalid replicas %d: must be non-negative", r)
	}
	return int32(r), nil
}

func resolveTerminationGracePeriodSeconds(opts WorkloadOptions) int64 {
	if opts.DrainTimeoutSeconds > minDrainOverrideSec {
		return int64(opts.DrainTimeoutSeconds)
	}
	return defaultDrainSeconds
}

func toSnakeCase(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 4)
	var prev rune
	for _, r := range s {
		if r == '-' || r == '.' {
			b.WriteRune('_')
			prev = r
			continue
		}
		if unicode.IsUpper(r) {
			if prev != 0 && prev != '_' && prev != '-' && prev != '.' && !unicode.IsUpper(prev) {
				b.WriteRune('_')
			}
			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteRune(r)
		}
		prev = r
	}
	return b.String()
}

func buildArgs(params map[string]string) []string {
	if len(params) == 0 {
		return nil
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		if _, skip := specialParameterKeys[k]; skip {
			continue
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return nil
	}
	sort.Strings(keys)

	args := make([]string, 0, len(keys))
	for _, k := range keys {
		flagKey := toSnakeCase(k)
		args = append(args, fmt.Sprintf("-%s=%s", flagKey, params[k]))
	}
	return args
}

func buildLabels(componentName string) map[string]string {
	return map[string]string{
		"app":                          componentName,
		"app.kubernetes.io/name":       componentName,
		"app.kubernetes.io/managed-by": "xmc-bridge",
		"app.kubernetes.io/part-of":    "xmanager-cloud",
	}
}

func buildOwnerReferences(componentName string, ownerUID types.UID) []metav1.OwnerReference {
	if ownerUID == "" {
		return nil
	}
	isController := true
	blockOwnerDeletion := true
	return []metav1.OwnerReference{
		{
			APIVersion:         "xmc.dev/v1alpha1",
			Kind:               "Component",
			Name:               componentName,
			UID:                ownerUID,
			Controller:         &isController,
			BlockOwnerDeletion: &blockOwnerDeletion,
		},
	}
}

// FakeWorkloadManager is an in-memory test double implementing WorkloadManager.
type FakeWorkloadManager struct {
	mu              sync.Mutex
	ActiveWorkloads map[string]bool
	EnsureErr       error
	DeleteErr       error
}

var _ WorkloadManager = (*FakeWorkloadManager)(nil)

// NewFakeWorkloadManager constructs a new FakeWorkloadManager.
func NewFakeWorkloadManager() *FakeWorkloadManager {
	return &FakeWorkloadManager{
		ActiveWorkloads: make(map[string]bool),
	}
}

// Ensure1PWorkload records the active workload or returns EnsureErr if configured.
func (f *FakeWorkloadManager) Ensure1PWorkload(ctx context.Context, namespace, componentName string, opts WorkloadOptions) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.EnsureErr != nil {
		return f.EnsureErr
	}
	if f.ActiveWorkloads == nil {
		f.ActiveWorkloads = make(map[string]bool)
	}
	f.ActiveWorkloads[componentName] = true
	return nil
}

// Delete1PWorkload removes the active workload or returns DeleteErr if configured.
func (f *FakeWorkloadManager) Delete1PWorkload(ctx context.Context, namespace, componentName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.DeleteErr != nil {
		return f.DeleteErr
	}
	if f.ActiveWorkloads != nil {
		delete(f.ActiveWorkloads, componentName)
	}
	return nil
}

// IsWorkloadActive returns whether a given component workload is active.
func (f *FakeWorkloadManager) IsWorkloadActive(componentName string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ActiveWorkloads == nil {
		return false
	}
	return f.ActiveWorkloads[componentName]
}
