// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package envtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bazelbuild/rules_go/go/runfiles"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	envtestpkg "sigs.k8s.io/controller-runtime/pkg/envtest"
	jobsetv1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
)

// resolveBinaryAssetsDir finds the directory containing kube-apiserver and etcd.
func resolveBinaryAssetsDir() (string, error) {
	if r, err := runfiles.New(); err == nil {
		if path, err := r.Rlocation("envtest_binaries/kube-apiserver"); err == nil && path != "" {
			if _, err := os.Stat(path); err == nil {
				return filepath.Dir(path), nil
			}
		}
	}

	if envPath := os.Getenv("KUBEBUILDER_ASSETS"); envPath != "" {
		if _, err := os.Stat(filepath.Join(envPath, "kube-apiserver")); err == nil {
			return envPath, nil
		}
	}

	return "", fmt.Errorf("failed to locate envtest binaries (kube-apiserver, etcd)")
}

// NewScheme constructs a Scheme with standard, JobSet, and Kueue types registered.
func NewScheme() (*runtime.Scheme, error) {
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		return nil, fmt.Errorf("failed to add client-go scheme: %w", err)
	}
	if err := apiextensionsv1.AddToScheme(s); err != nil {
		return nil, fmt.Errorf("failed to add apiextensions scheme: %w", err)
	}
	if err := jobsetv1alpha2.AddToScheme(s); err != nil {
		return nil, fmt.Errorf("failed to add jobset scheme: %w", err)
	}
	if err := kueuev1beta1.AddToScheme(s); err != nil {
		return nil, fmt.Errorf("failed to add kueue scheme: %w", err)
	}
	return s, nil
}

// TestEnv encapsulates an active envtest test environment.
type TestEnv struct {
	Environment   *envtestpkg.Environment
	Config        *rest.Config
	Client        ctrlclient.Client
	DynamicClient dynamic.Interface
	Clientset     kubernetes.Interface
	Scheme        *runtime.Scheme
}

// Stop terminates the test environment.
func (e *TestEnv) Stop() error {
	if e == nil || e.Environment == nil {
		return nil
	}
	return e.Environment.Stop()
}

// CreateNamespace creates a unique namespace and returns its name.
func (e *TestEnv) CreateNamespace(ctx context.Context, t *testing.T, prefix string) string {
	t.Helper()

	randBytes := make([]byte, 4)
	if _, err := rand.Read(randBytes); err != nil {
		t.Fatalf("failed to generate random suffix: %v", err)
	}
	nsName := fmt.Sprintf("%s-%s", prefix, hex.EncodeToString(randBytes))

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: nsName,
		},
	}
	if err := e.Client.Create(ctx, ns); err != nil {
		t.Fatalf("failed to create namespace %q: %v", nsName, err)
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = e.Client.Delete(cleanupCtx, ns)
	})

	return nsName
}

// Option configures environment startup.
type Option func(*envConfig)

type envConfig struct {
	crds   []*apiextensionsv1.CustomResourceDefinition
	scheme *runtime.Scheme
}

// WithCRDs specifies explicit CRDs to install instead of loading all by default.
func WithCRDs(crds ...*apiextensionsv1.CustomResourceDefinition) Option {
	return func(cfg *envConfig) {
		cfg.crds = crds
	}
}

// WithScheme overrides the default runtime scheme.
func WithScheme(scheme *runtime.Scheme) Option {
	return func(cfg *envConfig) {
		cfg.scheme = scheme
	}
}

// StartEnvironment starts the kube-apiserver and etcd control plane with registered CRDs.
func StartEnvironment(ctx context.Context, opts ...Option) (*TestEnv, error) {
	cfg := &envConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	if cfg.scheme == nil {
		s, err := NewScheme()
		if err != nil {
			return nil, err
		}
		cfg.scheme = s
	}

	if len(cfg.crds) == 0 {
		crds, err := LoadAllCRDs()
		if err != nil {
			return nil, fmt.Errorf("failed to load CRDs for envtest: %w", err)
		}
		cfg.crds = crds
	}

	binDir, err := resolveBinaryAssetsDir()
	if err != nil {
		return nil, err
	}

	env := &envtestpkg.Environment{
		BinaryAssetsDirectory: binDir,
		Scheme:                cfg.scheme,
		CRDs:                  cfg.crds,
		ErrorIfCRDPathMissing: false,
	}

	restConfig, err := env.Start()
	if err != nil {
		return nil, fmt.Errorf("failed to start envtest environment: %w", err)
	}

	ctrlCl, err := ctrlclient.New(restConfig, ctrlclient.Options{Scheme: cfg.scheme})
	if err != nil {
		_ = env.Stop()
		return nil, fmt.Errorf("failed to create controller-runtime client: %w", err)
	}

	dynCl, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		_ = env.Stop()
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		_ = env.Stop()
		return nil, fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}

	return &TestEnv{
		Environment:   env,
		Config:        restConfig,
		Client:        ctrlCl,
		DynamicClient: dynCl,
		Clientset:     clientset,
		Scheme:        cfg.scheme,
	}, nil
}

// SetupTestEnv creates and starts an envtest environment for a testing.T, registering automatic cleanup.
func SetupTestEnv(t *testing.T, opts ...Option) *TestEnv {
	t.Helper()

	ctx := context.Background()
	testEnv, err := StartEnvironment(ctx, opts...)
	if err != nil {
		t.Fatalf("failed to setup envtest test environment: %v", err)
	}

	t.Cleanup(func() {
		if err := testEnv.Stop(); err != nil {
			t.Logf("warning: failed to stop envtest environment: %v", err)
		}
	})

	return testEnv
}
