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

// probeComponentName is the name of the throwaway Component used to detect policy activation.
const probeComponentName = "policy-activation-probe"

// Harness owns a running envtest control plane preloaded with the Component CRD and policy.
type Harness struct {
	env          *envtest.Environment
	adminClients kubernetes.Interface
	// adminDynamic and subjectDynamic are built once and re-scoped per namespace. Constructing a
	// dynamic client allocates a fresh HTTP transport and connection pool, so building one per
	// test namespace would be wasteful.
	adminDynamic   dynamic.Interface
	subjectDynamic dynamic.Interface
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
	// Dynamic is the unscoped admin dynamic client, for production types that construct their
	// own resource clients from a dynamic.Interface.
	Dynamic dynamic.Interface
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
	adminDynamic, err := dynamic.NewForConfig(adminCfg)
	if err != nil {
		return nil, fmt.Errorf("building admin dynamic client: %w", err)
	}
	subjectDynamic, err := dynamic.NewForConfig(subjectCfg)
	if err != nil {
		return nil, fmt.Errorf("building policy-subject dynamic client: %w", err)
	}
	return &Harness{
		env:            env,
		adminClients:   adminClients,
		adminDynamic:   adminDynamic,
		subjectDynamic: subjectDynamic,
	}, nil
}

// Stop shuts the control plane down.
func (h *Harness) Stop() error {
	return h.env.Stop()
}

// NewNamespace creates a uniquely named namespace and returns Component clients scoped to it.
//
// The namespace is intentionally not deleted afterwards. envtest runs only etcd and the API
// server, with no kube-controller-manager, so nothing finalizes a deleted namespace: it would
// simply sit in the Terminating phase for the rest of the run.
func (h *Harness) NewNamespace(ctx context.Context) (*Clients, error) {
	ns, err := h.adminClients.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "xmc-components-"},
	}, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("creating test namespace: %w", err)
	}
	name := ns.GetName()
	return &Clients{
		Namespace:     name,
		Admin:         h.adminDynamic.Resource(ComponentGVR).Namespace(name),
		PolicySubject: h.subjectDynamic.Resource(ComponentGVR).Namespace(name),
		Dynamic:       h.adminDynamic,
	}, nil
}

// probeOps are the probe operations the activation poll loop depends on. They are injected so the
// retry behavior can be unit tested without a control plane: against a real API server the policy
// is live on the first attempt, so the retry path would otherwise never be exercised.
type probeOps struct {
	// create provisions a deletion-protected probe Component as the admin user.
	create func(context.Context) error
	// deleteAsSubject attempts the deletion whose rejection proves the policy is enforced.
	deleteAsSubject func(context.Context) error
	// deleteAsAdmin removes the probe once the policy is confirmed, via the breakglass user.
	deleteAsAdmin func(context.Context) error
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
	ops := probeOps{
		create: func(ctx context.Context) error {
			_, err := clients.Admin.Create(ctx, NewComponent(probeComponentName, true), metav1.CreateOptions{})
			return err
		},
		deleteAsSubject: func(ctx context.Context) error {
			return clients.PolicySubject.Delete(ctx, probeComponentName, metav1.DeleteOptions{})
		},
		deleteAsAdmin: func(ctx context.Context) error {
			return clients.Admin.Delete(ctx, probeComponentName, metav1.DeleteOptions{})
		},
	}
	return pollUntilPolicyEnforced(ctx, ops, policyActivationTimeout)
}

// pollUntilPolicyEnforced retries the probe until its deletion is denied by the policy.
func pollUntilPolicyEnforced(ctx context.Context, ops probeOps, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		// The probe is recreated on every attempt: while the policy is not yet enforced the
		// deletion below actually succeeds and destroys it, so a probe created once outside the
		// loop would be usable exactly once.
		if err := ops.create(ctx); err != nil {
			return fmt.Errorf("creating activation probe: %w", err)
		}

		err := ops.deleteAsSubject(ctx)
		if IsDeniedByPolicy(err) {
			if err := ops.deleteAsAdmin(ctx); err != nil {
				return fmt.Errorf("deleting activation probe: %w", err)
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("deleting activation probe: %w", err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("deletion-protection policy was not enforced within %v", timeout)
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
