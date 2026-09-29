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

// Package crreader provides Kubernetes Component Custom Resource retrieval
// and unstructured field extraction for XMC control and data plane services.
package crreader

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	constants "xmanager_cloud/kubernetes/common/constants"
)

// ComponentGVR returns the GroupVersionResource for XMC Component custom resources.
func ComponentGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{
		Group:    constants.ComponentCRDGroup,
		Version:  constants.ComponentCRDVersion,
		Resource: constants.ComponentCRDResource,
	}
}

// ResolveNamespace returns the trimmed namespace, or the default components namespace ("xmc") when empty.
func ResolveNamespace(ns string) string {
	if trimmed := strings.TrimSpace(ns); trimmed != "" {
		return trimmed
	}
	return constants.DefaultComponentsNamespace
}

// Getter retrieves a Kubernetes Component Custom Resource by namespace and name.
type Getter interface {
	GetComponent(ctx context.Context, namespace, name string) (*unstructured.Unstructured, error)
}

// SecretReader retrieves a single string value from a Kubernetes Secret by namespace, name, and key.
type SecretReader interface {
	GetSecretValue(ctx context.Context, namespace, name, key string) (string, error)
}

type k8sGetter struct {
	client dynamic.Interface
}

var _ Getter = (*k8sGetter)(nil)

// NewGetterFromDynamic constructs a Getter backed by the provided Kubernetes dynamic client.
func NewGetterFromDynamic(client dynamic.Interface) Getter {
	return &k8sGetter{client: client}
}

// NewK8sGetter constructs a Getter backed by the in-cluster or default kubeconfig.
func NewK8sGetter() (Getter, error) {
	restCfg, err := loadRestConfig()
	if err != nil {
		return nil, err
	}
	dynClient, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic kubernetes client: %w", err)
	}
	return NewGetterFromDynamic(dynClient), nil
}

type k8sSecretReader struct{ client kubernetes.Interface }

var _ SecretReader = (*k8sSecretReader)(nil)

// NewSecretReaderFromClientset constructs a SecretReader backed by the provided Kubernetes clientset.
func NewSecretReaderFromClientset(client kubernetes.Interface) SecretReader {
	return &k8sSecretReader{client: client}
}

// NewK8sSecretReader constructs a SecretReader backed by the in-cluster or default kubeconfig.
func NewK8sSecretReader() (SecretReader, error) {
	restCfg, err := loadRestConfig()
	if err != nil {
		return nil, err
	}
	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}
	return NewSecretReaderFromClientset(clientset), nil
}

func loadRestConfig() (*rest.Config, error) {
	restCfg, err := rest.InClusterConfig()
	if err == nil {
		return restCfg, nil
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	restCfg, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to build kubernetes client config: %w", err)
	}
	return restCfg, nil
}

// GetComponent fetches an unstructured Component resource from the Kubernetes API server.
func (g *k8sGetter) GetComponent(ctx context.Context, namespace, name string) (*unstructured.Unstructured, error) {
	if g == nil || g.client == nil {
		return nil, fmt.Errorf("dynamic kubernetes client is not initialized")
	}
	return g.client.Resource(ComponentGVR()).Namespace(ResolveNamespace(namespace)).Get(ctx, name, metav1.GetOptions{})
}

// GetSecretValue fetches a Kubernetes Secret and extracts key, stripping only trailing line terminators.
func (s *k8sSecretReader) GetSecretValue(ctx context.Context, namespace, name, key string) (string, error) {
	if s == nil || s.client == nil {
		return "", fmt.Errorf("kubernetes clientset is not initialized")
	}
	ns := ResolveNamespace(namespace)
	secret, err := s.client.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get secret %s/%s: %w", ns, name, err)
	}
	if raw, ok := secret.Data[key]; ok {
		return strings.TrimRight(string(raw), "\r\n"), nil
	}
	if val, ok := secret.StringData[key]; ok {
		return strings.TrimRight(val, "\r\n"), nil
	}
	return "", fmt.Errorf("secret %s/%s is missing key %q", ns, name, key)
}
