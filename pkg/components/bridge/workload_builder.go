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
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
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
		if t, ok := opts.Parameters["imageTag"]; ok && t != "" {
			return t
		}
		if t, ok := opts.Parameters["global.imageTag"]; ok && t != "" {
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
	if opts.DrainTimeoutSeconds <= 0 {
		return defaultDrainSeconds
	}
	if opts.DrainTimeoutSeconds < minDrainOverrideSec {
		return int64(minDrainOverrideSec)
	}
	return int64(opts.DrainTimeoutSeconds)
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
