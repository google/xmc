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

// Package componentenv boots a real Kubernetes API server via controller-runtime envtest with the
// production Component CRD and deletion-protection ValidatingAdmissionPolicy installed.
//
// The manifests are read from deploy/helm/files/components, which is the same source of truth the
// Helm chart renders, so the admission behavior under test is the behavior that ships.
//
// A control plane takes seconds to boot, so callers are expected to create one Harness per test
// binary from TestMain and to isolate individual tests with NewNamespace.
package componentenv

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"xmanager_cloud/test/integration/envtestassets"
)

// ComponentGVR is the GroupVersionResource of the Component custom resource.
var ComponentGVR = schema.GroupVersionResource{Group: "xmc.dev", Version: "v1alpha1", Resource: "components"}

// manifestDir is the repo-relative directory holding the plain CRD and policy manifests.
const manifestDir = "deploy/helm/files/components"

// vapManifestFile is the policy manifest inside manifestDir.
const vapManifestFile = "deletion-vap.yaml"

// policySubjectUser is an ordinary cluster-admin user that is deliberately NOT in system:masters,
// because the deletion-protection policy exempts system:masters as an operator breakglass.
const policySubjectUser = "xmc-policy-subject"

// policyActivationTimeout bounds the wait for the API server to load a newly created
// ValidatingAdmissionPolicy into its admission chain. The API server discovers policies through an
// informer, so a policy is not enforced the instant its object is created.
const policyActivationTimeout = 30 * time.Second

// policyActivationPollInterval is the delay between deletion-protection activation probes.
const policyActivationPollInterval = 50 * time.Millisecond

// Harness owns a running envtest control plane preloaded with the Component CRD and policy.
type Harness struct {
	env          *envtest.Environment
	adminCfg     *rest.Config
	subjectCfg   *rest.Config
	adminClients kubernetes.Interface
}

// Clients holds the Component clients scoped to one test namespace.
type Clients struct {
	// Namespace is the namespace all clients are scoped to.
	Namespace string
	// Admin acts as a system:masters user. The deletion-protection policy deliberately exempts
	// system:masters, so use this for setup and teardown, not for admission assertions.
	Admin dynamic.ResourceInterface
	// PolicySubject acts as a cluster-admin user outside system:masters, and is therefore
	// subject to the deletion-protection policy.
	PolicySubject dynamic.ResourceInterface
}

// New boots an API server, installs the Component CRD and the deletion-protection policy, and
// provisions the policy-subject user. Callers must call Stop.
func New(ctx context.Context) (*Harness, error) {
	assetsDir, err := envtestassets.Dir()
	if err != nil {
		return nil, fmt.Errorf("locating envtest control-plane binaries: %w", err)
	}
	root, err := repoRoot()
	if err != nil {
		return nil, fmt.Errorf("locating repository root: %w", err)
	}

	env := &envtest.Environment{
		BinaryAssetsDirectory: assetsDir,
		CRDDirectoryPaths:     []string{filepath.Join(root, manifestDir)},
		ErrorIfCRDPathMissing: true,
	}
	adminCfg, err := env.Start()
	if err != nil {
		return nil, fmt.Errorf("starting envtest control plane: %w", err)
	}

	harness, err := configure(ctx, env, adminCfg, filepath.Join(root, manifestDir, vapManifestFile))
	if err != nil {
		return nil, errors.Join(err, env.Stop())
	}
	return harness, nil
}

// configure installs the policy and provisions the policy-subject user on a started control plane.
func configure(ctx context.Context, env *envtest.Environment, adminCfg *rest.Config, vapPath string) (*Harness, error) {
	adminClients, err := kubernetes.NewForConfig(adminCfg)
	if err != nil {
		return nil, fmt.Errorf("building admin clientset: %w", err)
	}
	if err := applyPolicy(ctx, adminClients, vapPath); err != nil {
		return nil, fmt.Errorf("installing deletion-protection policy: %w", err)
	}
	subjectCfg, err := addPolicySubjectUser(ctx, env, adminClients)
	if err != nil {
		return nil, fmt.Errorf("provisioning policy-subject user: %w", err)
	}
	return &Harness{env: env, adminCfg: adminCfg, subjectCfg: subjectCfg, adminClients: adminClients}, nil
}

// Stop shuts the control plane down.
func (h *Harness) Stop() error {
	return h.env.Stop()
}

// NewNamespace creates a uniquely named namespace and returns Component clients scoped to it.
func (h *Harness) NewNamespace(ctx context.Context) (*Clients, error) {
	ns, err := h.adminClients.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "xmc-components-"},
	}, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("creating test namespace: %w", err)
	}
	admin, err := componentClient(h.adminCfg, ns.GetName())
	if err != nil {
		return nil, err
	}
	subject, err := componentClient(h.subjectCfg, ns.GetName())
	if err != nil {
		return nil, err
	}
	return &Clients{Namespace: ns.GetName(), Admin: admin, PolicySubject: subject}, nil
}

// WaitForDeletionPolicy blocks until the deletion-protection policy is actually enforced, which it
// detects by creating a protected probe Component and retrying its deletion until it is rejected.
//
// Without this the first admission assertion in a suite races the API server's policy informer.
func (h *Harness) WaitForDeletionPolicy(ctx context.Context) error {
	clients, err := h.NewNamespace(ctx)
	if err != nil {
		return fmt.Errorf("creating activation probe namespace: %w", err)
	}
	probe, err := clients.Admin.Create(ctx, NewComponent("policy-activation-probe", true), metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("creating activation probe: %w", err)
	}

	deadline := time.Now().Add(policyActivationTimeout)
	for {
		err := clients.PolicySubject.Delete(ctx, probe.GetName(), metav1.DeleteOptions{})
		if IsDeniedByPolicy(err) {
			return nil
		}

		if err == nil {
			return fmt.Errorf("activation probe was deleted, so the deletion-protection policy is not enforced")
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("deletion-protection policy was not enforced within %v, last error: %w", policyActivationTimeout, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(policyActivationPollInterval):
		}
	}
}

// NewComponent builds a minimal valid Component manifest with the given deletion protection value.
func NewComponent(name string, deletionProtection bool) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": ComponentGVR.Group + "/" + ComponentGVR.Version,
		"kind":       "Component",
		"metadata":   map[string]any{"name": name},
		"spec": map[string]any{
			"enabled":            true,
			"deletionProtection": deletionProtection,
		},
	}}
}

// componentClient returns a namespaced Component client for the supplied credentials.
func componentClient(cfg *rest.Config, namespace string) (dynamic.ResourceInterface, error) {
	client, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("building dynamic client for namespace %q: %w", namespace, err)
	}
	return client.Resource(ComponentGVR).Namespace(namespace), nil
}

// addPolicySubjectUser provisions a cluster-admin user outside system:masters, so that the
// deletion-protection policy's breakglass match conditions do not exempt it.
func addPolicySubjectUser(ctx context.Context, env *envtest.Environment, clients kubernetes.Interface) (*rest.Config, error) {
	user, err := env.AddUser(envtest.User{Name: policySubjectUser, Groups: []string{"xmc-integration-tests"}}, nil)
	if err != nil {
		return nil, fmt.Errorf("adding user %q: %w", policySubjectUser, err)
	}
	binding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: policySubjectUser + "-cluster-admin"},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "cluster-admin"},
		Subjects:   []rbacv1.Subject{{Kind: rbacv1.UserKind, APIGroup: rbacv1.GroupName, Name: policySubjectUser}},
	}
	if _, err := clients.RbacV1().ClusterRoleBindings().Create(ctx, binding, metav1.CreateOptions{}); err != nil {
		return nil, fmt.Errorf("granting cluster-admin to %q: %w", policySubjectUser, err)
	}
	return user.Config(), nil
}
