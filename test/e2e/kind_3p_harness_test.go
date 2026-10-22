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

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/bridge"
	"xmanager_cloud/pkg/components/bridge/healthprobe"
	"xmanager_cloud/pkg/components/bridge/statusdetector"
	"xmanager_cloud/pkg/components/cms"
	storememory "xmanager_cloud/pkg/components/store/memory"
)

const harnessBridgeSA = "xmc-bridge-sa@xmc-test.iam.gserviceaccount.com"

type staticHarnessAuthenticator struct {
	email string
}

func (s staticHarnessAuthenticator) AuthenticateUserFromContext(context.Context) (string, string, error) {
	if s.email == "" {
		return "", "", status.Error(codes.Unauthenticated, "missing bearer token")
	}
	return s.email, "uid-1", nil
}

func newHarnessCMSAuthorizer(callerEmail string) *cms.Authorizer {
	return cms.NewAuthorizer(cms.AuthorizerConfig{
		Authenticator:             staticHarnessAuthenticator{email: callerEmail},
		AuthorizedServiceAccounts: []string{harnessBridgeSA},
	})
}

var (
	crdGVR                                = schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
	componentGVR                          = schema.GroupVersionResource{Group: "xmc.dev", Version: "v1alpha1", Resource: "components"}
	_            healthprobe.HealthProber = (*kindNodePortProber)(nil)
)

// kindBridgeProbeInterval is the re-probe interval of the per-namespace bridge controller. It is
// short because 3P readiness follows external CR and workload status, which emit no Component
// event.
const kindBridgeProbeInterval = time.Second

// componentTransitionTimeout bounds each Component status transition awaited by waitForComponent. It
// exceeds the bridge's 30s DeactivationBlocked recheck interval, which gates how soon a blocked
// deactivation is retried.
const componentTransitionTimeout = 90 * time.Second

type kindClusterHarness struct {
	clusterName, kubeconfig string
	restCfg                 *rest.Config
	clientset               kubernetes.Interface
	dynClient               dynamic.Interface
	restMapper              *restmapper.DeferredDiscoveryRESTMapper
	cmsServer               *cms.Server
	reconciler              *bridge.Reconciler
}

func setupKindClusterHarness(t *testing.T, requiredImages ...string) *kindClusterHarness {
	t.Helper()
	ctrl.SetLogger(testr.New(t))
	clusterName, kubeconfig := acquireKindCluster(t)
	for _, img := range requiredImages {
		preloadImageIntoKind(t, clusterName, img)
	}
	harness := buildClusterClients(t, clusterName, kubeconfig)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	installComponentCRD(ctx, t, harness)
	return harness
}

func acquireKindCluster(t *testing.T) (string, string) {
	t.Helper()
	kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")
	if existing := strings.TrimSpace(os.Getenv("KIND_CLUSTER_NAME")); existing != "" {
		runCommand(t, "kind", "export", "kubeconfig", "--name", existing, "--kubeconfig", kubeconfig)
		return existing, kubeconfig
	}
	clusterName := fmt.Sprintf("xmc-3p-e2e-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		delCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		_ = exec.CommandContext(delCtx, resolveBinaryPath("kind"), "delete", "cluster", "--name", clusterName, "--kubeconfig", kubeconfig).Run()
	})
	runCommand(t, "kind", "create", "cluster", "--name", clusterName, "--kubeconfig", kubeconfig, "--wait", "90s")
	return clusterName, kubeconfig
}

func preloadImageIntoKind(t *testing.T, clusterName, image string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, resolveBinaryPath("docker"), "image", "inspect", image).Run(); err != nil {
		runCommand(t, "docker", "pull", image)
	}
	runCommand(t, "kind", "load", "docker-image", image, "--name", clusterName)
}

func runCommand(t *testing.T, name string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	bin := resolveBinaryPath(name)
	out, err := exec.CommandContext(ctx, bin, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("command %s %v failed: %v\nOutput:\n%s", bin, args, err, string(out))
	}
	return string(out)
}

// renderHelmTemplate runs `helm template args...` within ctx, bounded to 2 minutes, and returns
// its stdout only, because helm logs warnings such as symlinked chart files in Bazel runfiles to
// stderr.
func renderHelmTemplate(ctx context.Context, t *testing.T, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, resolveBinaryPath("helm"), append([]string{"template"}, args...)...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("helm template %v failed: %v\nStderr:\n%s", args, err, stderr.String())
	}
	return string(out)
}

func resolveBinaryPath(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	candidates := []string{"/usr/local/bin/" + name}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates, filepath.Join(home, ".local", "bin", name))
	}
	if wd, err := os.Getwd(); err == nil {
		if idx := strings.Index(wd, "/.cache/bazel/"); idx > 0 {
			candidates = append(candidates, filepath.Join(wd[:idx], ".local", "bin", name))
		}
	}
	for _, p := range candidates {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	return name
}

func buildClusterClients(t *testing.T, clusterName, kubeconfig string) *kindClusterHarness {
	t.Helper()
	restCfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		t.Fatalf("failed to build rest config: %v", err)
	}
	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		t.Fatalf("failed to create kubernetes clientset: %v", err)
	}
	dynClient, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		t.Fatalf("failed to create dynamic client: %v", err)
	}
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(clientset.Discovery()))
	cmsServer := cms.NewServer(storememory.NewMemoryStore(), cms.WithAuthorizer(newHarnessCMSAuthorizer(harnessBridgeSA)))
	prober := &kindNodePortProber{clientset: clientset, delegate: healthprobe.NewNetworkHealthProber(healthprobe.WithHealthProbeTimeout(5 * time.Second))}
	reconciler, err := bridge.NewReconciler(bridge.NewCMSClientAdapter(cmsServer), healthprobe.NewBridgeProber(statusdetector.NewK8sStatusDetector(dynClient, mapper), prober, 5*time.Second), nil)
	if err != nil {
		t.Fatalf("failed to create bridge reconciler: %v", err)
	}
	return &kindClusterHarness{clusterName: clusterName, kubeconfig: kubeconfig, restCfg: restCfg, clientset: clientset, dynClient: dynClient, restMapper: mapper, cmsServer: cmsServer, reconciler: reconciler}
}

func installComponentCRD(ctx context.Context, t *testing.T, h *kindClusterHarness) {
	t.Helper()
	chartRoot := filepath.Dir(filepath.Dir(filepath.Dir(locateHelmComponentCRDPath(t))))
	renderedYAML := renderHelmTemplate(ctx, t, "xmc-e2e", chartRoot, "--set", "componentsDeployment.enabled=true", "--show-only", "templates/components/crd.yaml")
	objs, err := decodeYAMLDocuments(renderedYAML)
	if err != nil || len(objs) != 1 {
		t.Fatalf("failed to decode CRD YAML into exactly one object (got %d): %v", len(objs), err)
	}
	applyUnstructuredObject(ctx, t, h, objs[0])
	waitForCRDEstablished(ctx, t, h, "components.xmc.dev")
}

// decodeYAMLDocuments decodes every non-empty document of a multi-document YAML stream, such as
// `helm template` output.
func decodeYAMLDocuments(rendered string) ([]map[string]any, error) {
	decoder := k8syaml.NewYAMLOrJSONDecoder(strings.NewReader(rendered), 4096)
	var objs []map[string]any
	for {
		var obj map[string]any
		err := decoder.Decode(&obj)
		if errors.Is(err, io.EOF) {
			return objs, nil
		}
		if err != nil {
			return nil, fmt.Errorf("failed to decode YAML document: %w", err)
		}
		if len(obj) > 0 {
			objs = append(objs, obj)
		}
	}
}

func installThirdPartyCRD(ctx context.Context, t *testing.T, h *kindClusterHarness, group, version, kind, plural string) {
	t.Helper()
	crdName := fmt.Sprintf("%s.%s", plural, group)
	schemaObj := map[string]any{"openAPIV3Schema": map[string]any{"type": "object", "x-kubernetes-preserve-unknown-fields": true}}
	applyUnstructuredObject(ctx, t, h, map[string]any{
		"apiVersion": "apiextensions.k8s.io/v1", "kind": "CustomResourceDefinition", "metadata": map[string]any{"name": crdName},
		"spec": map[string]any{
			"group": group, "scope": "Namespaced",
			"names":    map[string]any{"kind": kind, "listKind": kind + "List", "plural": plural, "singular": strings.ToLower(kind)},
			"versions": []any{map[string]any{"name": version, "served": true, "storage": true, "schema": schemaObj}},
		},
	})
	waitForCRDEstablished(ctx, t, h, crdName)
}

func applyThirdPartyStatusCR(ctx context.Context, t *testing.T, h *kindClusterHarness, apiVersion, kind, ns, name, endpoint, secretName string) {
	t.Helper()
	applyUnstructuredObject(ctx, t, h, map[string]any{
		"apiVersion": apiVersion, "kind": kind, "metadata": map[string]any{"name": name, "namespace": ns},
		"status": map[string]any{"endpoint": endpoint, "connectionSecret": secretName, "conditions": []any{map[string]any{"type": "Ready", "status": "True"}}},
	})
}

func buildStandardWrapperRef(group, version, kind, name string) map[string]any {
	return map[string]any{
		"group": group, "version": version, "kind": kind, "name": name,
		"readyConditionPath": "$.status.conditions[?(@.type=='Ready')].status", "endpointJsonPath": "$.status.endpoint", "connectionSecretJsonPath": "$.status.connectionSecret",
	}
}

func applyThirdPartyComponentCR(ctx context.Context, t *testing.T, h *kindClusterHarness, ns, name, compClass, version string, wrapperRef, params map[string]any) {
	t.Helper()
	compRes := h.dynClient.Resource(componentGVR).Namespace(ns)
	if existing, err := compRes.Get(ctx, name, metav1.GetOptions{}); err == nil {
		existing.SetFinalizers(nil)
		_, _ = compRes.Update(ctx, existing, metav1.UpdateOptions{})
		_ = compRes.Delete(ctx, name, metav1.DeleteOptions{})
		if !waitUntil(ctx, 15*time.Second, 100*time.Millisecond, func() bool {
			_, getErr := compRes.Get(ctx, name, metav1.GetOptions{})
			return apierrors.IsNotFound(getErr)
		}) {
			t.Fatalf("timed out waiting for previous Component %s/%s deletion", ns, name)
		}
	}
	spec := map[string]any{"componentType": "COMPONENT_TYPE_CUSTOM_3P_WRAPPER", "componentClass": compClass, "deletionProtection": false, "enabled": true, "version": version}
	if len(wrapperRef) > 0 {
		spec["wrapperRef"] = wrapperRef
	}
	if len(params) > 0 {
		spec["parameters"] = params
	}
	applyUnstructuredObject(ctx, t, h, map[string]any{
		"apiVersion": "xmc.dev/v1alpha1", "kind": "Component", "metadata": map[string]any{"name": name, "namespace": ns}, "spec": spec,
	})
}

func assertComponentEnvVars(t *testing.T, envVars map[string]string, wantEndpoint, wantHost, wantPort, wantSecret string) {
	t.Helper()
	if envVars["ENDPOINT"] != wantEndpoint || envVars["HOST"] != wantHost || envVars["PORT"] != wantPort || envVars["SECRET_NAME"] != wantSecret {
		t.Errorf("unexpected access envVars: got %v, want ENDPOINT=%q HOST=%q PORT=%q SECRET_NAME=%q", envVars, wantEndpoint, wantHost, wantPort, wantSecret)
	}
}

func locateHelmComponentCRDPath(t *testing.T) string {
	t.Helper()
	return locateRepoPath(t, "deploy/helm/templates/components/crd.yaml")
}

// locateRepoPath returns the absolute path of the repository-relative path relPath, resolved from
// Bazel runfiles when present and otherwise from the working directory or its ancestors.
func locateRepoPath(t *testing.T, relPath string) string {
	t.Helper()
	candidates := []string{".", "../..", "../../.."}
	if srcDir, ws := os.Getenv("TEST_SRCDIR"), os.Getenv("TEST_WORKSPACE"); srcDir != "" && ws != "" {
		candidates = append([]string{filepath.Join(srcDir, ws)}, candidates...)
	}
	for _, root := range candidates {
		p := filepath.Join(root, filepath.FromSlash(relPath))
		if _, err := os.Stat(p); err != nil {
			continue
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			t.Fatalf("failed to resolve absolute path of %s: %v", p, err)
		}
		return abs
	}
	t.Fatalf("could not locate %s in %v", relPath, candidates)
	return ""
}

func applyUnstructuredObject(ctx context.Context, t *testing.T, h *kindClusterHarness, rawObj map[string]any, subresources ...string) {
	t.Helper()
	u := &unstructured.Unstructured{Object: rawObj}
	jsonBytes, err := json.Marshal(rawObj)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}
	gvr := resolveObjectGVR(u.GroupVersionKind())
	var res dynamic.ResourceInterface = h.dynClient.Resource(gvr)
	if ns := u.GetNamespace(); ns != "" {
		res = h.dynClient.Resource(gvr).Namespace(ns)
	}
	force := true
	opts := metav1.PatchOptions{FieldManager: "e2e-harness", Force: &force}
	if _, err := res.Patch(ctx, u.GetName(), types.ApplyPatchType, jsonBytes, opts, subresources...); err != nil {
		t.Fatalf("failed to apply %s %s/%s: %v", u.GetKind(), u.GetNamespace(), u.GetName(), err)
	}
}

func resolveObjectGVR(gvk schema.GroupVersionKind) schema.GroupVersionResource {
	switch gvk.Kind {
	case "CustomResourceDefinition":
		return crdGVR
	case "Component":
		return componentGVR
	case "ClickHouseInstallation":
		return schema.GroupVersionResource{Group: "clickhouse.altinity.com", Version: "v1", Resource: "clickhouseinstallations"}
	case "Cluster":
		return schema.GroupVersionResource{Group: "cluster.redpanda.com", Version: "v1alpha2", Resource: "clusters"}
	default:
		return gvk.GroupVersion().WithResource(strings.ToLower(gvk.Kind) + "s")
	}
}

func waitUntil(ctx context.Context, timeout, interval time.Duration, cond func() bool) bool {
	return wait.PollUntilContextTimeout(ctx, interval, timeout, true, func(context.Context) (bool, error) {
		return cond(), nil
	}) == nil
}

func waitForCRDEstablished(ctx context.Context, t *testing.T, h *kindClusterHarness, crdName string) {
	t.Helper()
	ok := waitUntil(ctx, 30*time.Second, 250*time.Millisecond, func() bool {
		u, err := h.dynClient.Resource(crdGVR).Get(ctx, crdName, metav1.GetOptions{})
		return err == nil && hasConditionTrue(u, "Established")
	})
	if !ok {
		t.Fatalf("timed out waiting for CRD %q to become Established", crdName)
	}
	h.restMapper.Reset()
}

func hasConditionTrue(u *unstructured.Unstructured, condType string) bool {
	conds, found, err := unstructured.NestedSlice(u.Object, "status", "conditions")
	if err != nil || !found {
		return false
	}
	for _, raw := range conds {
		if m, ok := raw.(map[string]any); ok && m["type"] == condType && m["status"] == "True" {
			return true
		}
	}
	return false
}

func isComponentReady(u *unstructured.Unstructured) bool {
	obsGen, _, _ := unstructured.NestedInt64(u.Object, "status", "observedGeneration")
	state, _, _ := unstructured.NestedString(u.Object, "status", "state")
	return obsGen == u.GetGeneration() && state == "COMPONENT_STATE_ENABLED" && hasConditionTrue(u, "Ready")
}

// waitForComponent waits until cond holds for the named Component, as persisted by the namespace
// bridge, described by desc in failures. cond receives nil once the Component no longer exists.
func waitForComponent(ctx context.Context, t *testing.T, h *kindClusterHarness, ns, name, desc string, cond func(*unstructured.Unstructured) bool) *unstructured.Unstructured {
	t.Helper()
	var last *unstructured.Unstructured
	var lastErr error
	ok := waitUntil(ctx, componentTransitionTimeout, 500*time.Millisecond, func() bool {
		u, err := h.dynClient.Resource(componentGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
		switch {
		case apierrors.IsNotFound(err):
			last = nil
		case err != nil:
			lastErr = err
			return false
		default:
			last = u
		}
		return cond(last)
	})
	if !ok {
		t.Fatalf("timed out waiting for Component %s/%s to be %s (last err: %v); last object: %v", ns, name, desc, lastErr, last)
	}
	return last
}

func isObservedComponent(u *unstructured.Unstructured) bool {
	if u == nil {
		return false
	}
	obsGen, _, _ := unstructured.NestedInt64(u.Object, "status", "observedGeneration")
	return obsGen == u.GetGeneration()
}

func isExistingComponentReady(u *unstructured.Unstructured) bool {
	return u != nil && isComponentReady(u)
}

// conditionField returns field of the status condition condType, or "" if u is nil or the condition
// is absent.
func conditionField(u *unstructured.Unstructured, condType, field string) string {
	if u == nil {
		return ""
	}
	conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	for _, raw := range conds {
		if m, ok := raw.(map[string]any); ok && m["type"] == condType {
			v, _ := m[field].(string)
			return v
		}
	}
	return ""
}

// assertCondition verifies that the status condition condType of u has wantStatus and wantReason.
func assertCondition(t *testing.T, u *unstructured.Unstructured, condType, wantStatus, wantReason string) {
	t.Helper()
	gotStatus, gotReason := conditionField(u, condType, "status"), conditionField(u, condType, "reason")
	if gotStatus != wantStatus || gotReason != wantReason {
		t.Errorf("%s %s condition = %s/%s, want %s/%s", u.GetName(), condType, gotStatus, gotReason, wantStatus, wantReason)
	}
}

// assertCMSComponent verifies that the CMS record of the Component u is in wantState at the CR
// generation. Repeated bridge passes must not bump the CMS generation past the CR generation.
func assertCMSComponent(ctx context.Context, t *testing.T, h *kindClusterHarness, u *unstructured.Unstructured, wantState componentspb.ComponentState) {
	t.Helper()
	rec, err := h.cmsServer.Store().GetComponent(ctx, u.GetName())
	if err != nil {
		t.Fatalf("CMS GetComponent(%s) failed: %v", u.GetName(), err)
	}
	if rec.State != wantState || rec.Generation != u.GetGeneration() {
		t.Errorf("%s CMS state = %v, generation = %d; want %v, CR generation %d", u.GetName(), rec.State, rec.Generation, wantState, u.GetGeneration())
	}
}

// createTestNamespace creates a namespace named after prefix and runs a bridge controller over it
// until the test ends.
func createTestNamespace(ctx context.Context, t *testing.T, h *kindClusterHarness, prefix string) string {
	t.Helper()
	nsName := fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	if _, err := h.clientset.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: nsName}}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("failed to create namespace %q: %v", nsName, err)
	}
	t.Cleanup(func() {
		delCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		releaseComponentFinalizers(delCtx, t, h, nsName)
		_ = h.clientset.CoreV1().Namespaces().Delete(delCtx, nsName, metav1.DeleteOptions{})
	})
	// Registered after the namespace cleanup, so the bridge stops before it runs.
	startNamespaceBridge(t, h, nsName)
	return nsName
}

// startNamespaceBridge runs a bridge ComponentController over namespace in its own manager, as the
// deployed xmc-bridge does for its Release namespace, until the test ends. Each manager is the
// namespace's only writer, so it runs without leader election.
func startNamespaceBridge(t *testing.T, h *kindClusterHarness, namespace string) {
	t.Helper()
	opts := bridge.ComponentManagerOptions(bridge.ComponentManagerConfig{Namespace: namespace})
	opts.Controller.SkipNameValidation = ptr.To(true) // Every namespace gets an "xmc-bridge-component" controller.
	mgr, err := manager.New(h.restCfg, opts)
	if err != nil {
		t.Fatalf("failed to create bridge manager for %s: %v", namespace, err)
	}
	c := bridge.NewComponentController(mgr.GetClient(), h.reconciler, bridge.ComponentControllerOptions{ProbeInterval: kindBridgeProbeInterval, ReconcileTimeout: 30 * time.Second})
	if err := c.SetupWithManager(mgr); err != nil {
		t.Fatalf("failed to set up bridge controller for %s: %v", namespace, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- mgr.Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("bridge manager for %s stopped with error: %v", namespace, err)
		}
	})
}

// releaseComponentFinalizers removes the bridge cleanup finalizer from every Component in
// namespace. The bridge stops before this cleanup, so finalized Components would otherwise keep the
// namespace in Terminating on a reused KinD cluster. Failures are logged, not fatal, because this
// runs during cleanup.
func releaseComponentFinalizers(ctx context.Context, t *testing.T, h *kindClusterHarness, namespace string) {
	t.Helper()
	c, err := client.New(h.restCfg, client.Options{})
	if err != nil {
		t.Logf("cleanup: failed to create client for %s: %v", namespace, err)
		return
	}
	list, err := h.dynClient.Resource(componentGVR).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Logf("cleanup: failed to list Components in %s: %v", namespace, err)
		return
	}
	for i := range list.Items {
		if err := bridge.ReleaseCleanupFinalizer(ctx, c, &list.Items[i]); err != nil {
			t.Logf("cleanup: failed to release finalizer of Component %s/%s: %v", namespace, list.Items[i].GetName(), err)
		}
	}
}

type serviceInstanceSpec struct {
	Namespace, AppName, SecretName string
	SecretData                     map[string]string
	Ports                          map[string]int32
	Container                      corev1.Container
}

func deploySingleContainerService(ctx context.Context, t *testing.T, h *kindClusterHarness, spec serviceInstanceSpec) {
	t.Helper()
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: spec.SecretName, Namespace: spec.Namespace}, StringData: spec.SecretData}
	if _, err := h.clientset.CoreV1().Secrets(spec.Namespace).Create(ctx, secret, metav1.CreateOptions{}); err != nil {
		t.Fatalf("failed to create secret %s/%s: %v", spec.Namespace, spec.SecretName, err)
	}
	createNodePortService(ctx, t, h, spec.Namespace, spec.AppName, spec.Ports)
	podName := spec.AppName + "-0"
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: spec.Namespace, Labels: map[string]string{"app": spec.AppName}},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{spec.Container}},
	}
	if _, err := h.clientset.CoreV1().Pods(spec.Namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("failed to create pod %s/%s: %v", spec.Namespace, podName, err)
	}
	waitForPodReady(ctx, t, h, spec.Namespace, podName)
}

func createNodePortService(ctx context.Context, t *testing.T, h *kindClusterHarness, ns, name string, ports map[string]int32) {
	t.Helper()
	svcPorts := make([]corev1.ServicePort, 0, len(ports))
	for _, portName := range slices.Sorted(maps.Keys(ports)) {
		p := ports[portName]
		svcPorts = append(svcPorts, corev1.ServicePort{Name: portName, Port: p, TargetPort: intstr.FromInt(int(p))})
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeNodePort, Selector: map[string]string{"app": name}, Ports: svcPorts},
	}
	if _, err := h.clientset.CoreV1().Services(ns).Create(ctx, svc, metav1.CreateOptions{}); err != nil {
		t.Fatalf("failed to create service %s/%s: %v", ns, name, err)
	}
}

// waitForComponentReady waits for the namespace bridge to report the named Component Ready and
// returns its access envVars.
func waitForComponentReady(ctx context.Context, t *testing.T, h *kindClusterHarness, namespace, name string) map[string]string {
	t.Helper()
	var envVars map[string]string
	var last *unstructured.Unstructured
	var lastErr error
	ok := waitUntil(ctx, 60*time.Second, 500*time.Millisecond, func() bool {
		last, lastErr = h.dynClient.Resource(componentGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		if lastErr != nil || !isComponentReady(last) {
			return false
		}
		envVars, _, _ = unstructured.NestedStringMap(last.Object, "status", "access", "envVars")
		return true
	})
	if !ok {
		t.Fatalf("timed out waiting for Component %s/%s Ready (last err: %v); last object: %v", namespace, name, lastErr, last)
	}
	return envVars
}

func waitForPodReady(ctx context.Context, t *testing.T, h *kindClusterHarness, namespace, podName string) {
	t.Helper()
	ok := waitUntil(ctx, 90*time.Second, time.Second, func() bool {
		pod, err := h.clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
		if err != nil {
			return false
		}
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				return true
			}
		}
		return false
	})
	if !ok {
		t.Fatalf("timed out waiting for Pod %s/%s Ready; logs:\n%s", namespace, podName, fetchPodLogs(h, namespace, podName))
	}
}

type workloadPodSpec struct {
	Namespace, Name, Image string
	Command                []string
	AccessEnvVars          map[string]string
	SecretEnvBinds         map[string]corev1.SecretKeySelector
}

func runMockMLWorkloadPod(ctx context.Context, t *testing.T, h *kindClusterHarness, spec workloadPodSpec) string {
	t.Helper()
	startWorkloadPod(ctx, t, h, spec)
	return waitForPodSucceededLogs(ctx, t, h, spec.Namespace, spec.Name)
}

// startWorkloadPod creates the never-restarting workload Pod described by spec and returns without
// waiting for it.
func startWorkloadPod(ctx context.Context, t *testing.T, h *kindClusterHarness, spec workloadPodSpec) {
	t.Helper()
	envList := make([]corev1.EnvVar, 0, len(spec.AccessEnvVars)+len(spec.SecretEnvBinds))
	for _, k := range slices.Sorted(maps.Keys(spec.AccessEnvVars)) {
		envList = append(envList, corev1.EnvVar{Name: k, Value: spec.AccessEnvVars[k]})
	}
	for _, envName := range slices.Sorted(maps.Keys(spec.SecretEnvBinds)) {
		selCopy := spec.SecretEnvBinds[envName]
		envList = append(envList, corev1.EnvVar{Name: envName, ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &selCopy}})
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: spec.Name, Namespace: spec.Namespace},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers:    []corev1.Container{{Name: "workload", Image: spec.Image, ImagePullPolicy: corev1.PullIfNotPresent, Command: spec.Command, Env: envList}},
		},
	}
	if _, err := h.clientset.CoreV1().Pods(spec.Namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("failed to create workload pod %s/%s: %v", spec.Namespace, spec.Name, err)
	}
}

func waitForPodSucceededLogs(ctx context.Context, t *testing.T, h *kindClusterHarness, namespace, podName string) string {
	t.Helper()
	var failed bool
	ok := waitUntil(ctx, 60*time.Second, 500*time.Millisecond, func() bool {
		pod, err := h.clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
		if err != nil {
			return false
		}
		if pod.Status.Phase == corev1.PodFailed {
			failed = true
			return true
		}
		return pod.Status.Phase == corev1.PodSucceeded
	})
	logs := fetchPodLogs(h, namespace, podName)
	if !ok || failed {
		t.Fatalf("mock ML workload pod %s/%s did not succeed (failed=%v); logs:\n%s", namespace, podName, failed, logs)
	}
	return logs
}

func fetchPodLogs(h *kindClusterHarness, namespace, podName string) string {
	logCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := h.clientset.CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{}).Stream(logCtx)
	if err != nil {
		return fmt.Sprintf("<unavailable: %v>", err)
	}
	defer stream.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, io.LimitReader(stream, 1024*1024))
	return buf.String()
}

type kindNodePortProber struct {
	clientset kubernetes.Interface
	delegate  healthprobe.HealthProber
}

func (p *kindNodePortProber) Probe(ctx context.Context, target string, timeout time.Duration) (*healthprobe.HealthProbeResult, error) {
	trimmed := strings.TrimSpace(target)
	if lower := strings.ToLower(trimmed); strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return p.ProbeHTTP(ctx, trimmed, timeout)
	}
	return p.ProbeTCP(ctx, trimmed, timeout)
}

func (p *kindNodePortProber) ProbeHTTP(ctx context.Context, endpoint string, timeout time.Duration) (*healthprobe.HealthProbeResult, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Host == "" {
		return p.delegate.ProbeHTTP(ctx, endpoint, timeout)
	}
	rewrittenHost, err := p.translateHostPort(ctx, parsed.Host)
	if err != nil {
		return nil, err
	}
	parsed.Host = rewrittenHost
	return p.delegate.ProbeHTTP(ctx, parsed.String(), timeout)
}

func (p *kindNodePortProber) ProbeTCP(ctx context.Context, endpoint string, timeout time.Duration) (*healthprobe.HealthProbeResult, error) {
	rewritten, err := p.translateHostPort(ctx, strings.TrimSpace(endpoint))
	if err != nil {
		return nil, err
	}
	return p.delegate.ProbeTCP(ctx, rewritten, timeout)
}

func (p *kindNodePortProber) translateHostPort(ctx context.Context, hostPort string) (string, error) {
	host, portStr, err := net.SplitHostPort(hostPort)
	if err != nil || !strings.HasSuffix(host, ".svc.cluster.local") {
		return hostPort, nil
	}
	parts := strings.Split(strings.TrimSuffix(host, ".svc.cluster.local"), ".")
	if len(parts) < 2 {
		return hostPort, nil
	}
	portNum, err := strconv.Atoi(portStr)
	if err != nil {
		return "", fmt.Errorf("invalid port %q: %w", portStr, err)
	}
	nodeIP, err := p.lookupNodeInternalIP(ctx)
	if err != nil {
		return "", err
	}
	nodePort, err := p.lookupServiceNodePort(ctx, parts[len(parts)-1], parts[len(parts)-2], int32(portNum))
	if err != nil {
		return "", err
	}
	return net.JoinHostPort(nodeIP, strconv.Itoa(int(nodePort))), nil
}

func (p *kindNodePortProber) lookupNodeInternalIP(ctx context.Context) (string, error) {
	nodes, err := p.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("failed listing nodes: %w", err)
	}
	for _, n := range nodes.Items {
		for _, addr := range n.Status.Addresses {
			if addr.Type == corev1.NodeInternalIP && addr.Address != "" {
				return addr.Address, nil
			}
		}
	}
	return "", fmt.Errorf("no node InternalIP found in cluster")
}

func (p *kindNodePortProber) lookupServiceNodePort(ctx context.Context, namespace, svcName string, port int32) (int32, error) {
	svc, err := p.clientset.CoreV1().Services(namespace).Get(ctx, svcName, metav1.GetOptions{})
	if err != nil {
		return 0, fmt.Errorf("failed getting service %s/%s: %w", namespace, svcName, err)
	}
	for _, sp := range svc.Spec.Ports {
		if sp.Port == port && sp.NodePort > 0 {
			return sp.NodePort, nil
		}
	}
	return 0, fmt.Errorf("service %s/%s has no NodePort mapped for port %d", namespace, svcName, port)
}

func TestKind3PHarnessUnit(t *testing.T) {
	if got := resolveObjectGVR(schema.GroupVersionKind{Group: "xmc.dev", Version: "v1alpha1", Kind: "Component"}); got.Resource != "components" {
		t.Errorf("resolveObjectGVR Component = %q, want components", got.Resource)
	}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "kind-control-plane"}, Status: corev1.NodeStatus{Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "172.18.0.2"}}}}
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "clickhouse", Namespace: "xmc-test"}, Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeNodePort, Ports: []corev1.ServicePort{{Name: "http", Port: 8123, NodePort: 31123}}}}
	prober := &kindNodePortProber{clientset: fake.NewSimpleClientset(node, svc)}
	if got, err := prober.translateHostPort(context.Background(), "clickhouse.xmc-test.svc.cluster.local:8123"); err != nil || got != "172.18.0.2:31123" {
		t.Errorf("translateHostPort = (%q, %v), want (\"172.18.0.2:31123\", nil)", got, err)
	}
	comp := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"generation": int64(2)},
		"status":   map[string]any{"observedGeneration": int64(2), "state": "COMPONENT_STATE_ENABLED", "conditions": []any{map[string]any{"type": "Ready", "status": "True"}}},
	}}
	if !isComponentReady(comp) || locateHelmComponentCRDPath(t) == "" {
		t.Error("expected isComponentReady and locateHelmComponentCRDPath to succeed")
	}
	verifyHarnessCMSAuth(t)
}

func verifyHarnessCMSAuth(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	req := &componentspb.SetComponentIntentRequest{
		Name: "components/clickhouse",
		Spec: &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER, Version: "v1.0.0"},
	}
	unauthSrv := cms.NewServer(storememory.NewMemoryStore(), cms.WithAuthorizer(newHarnessCMSAuthorizer("")))
	if _, err := unauthSrv.SetComponentIntent(ctx, req); status.Code(err) != codes.Unauthenticated {
		t.Errorf("unauthenticated SetComponentIntent code = %v, want Unauthenticated", status.Code(err))
	}
	viewerSrv := cms.NewServer(storememory.NewMemoryStore(), cms.WithAuthorizer(newHarnessCMSAuthorizer("viewer@xmc.local")))
	if _, err := viewerSrv.SetComponentIntent(ctx, req); status.Code(err) != codes.PermissionDenied {
		t.Errorf("viewer SetComponentIntent code = %v, want PermissionDenied", status.Code(err))
	}
	if _, err := viewerSrv.GetPlatformHealth(ctx, &componentspb.GetPlatformHealthRequest{}); err != nil {
		t.Errorf("viewer GetPlatformHealth unexpected error = %v", err)
	}
	bridgeSrv := cms.NewServer(storememory.NewMemoryStore(), cms.WithAuthorizer(newHarnessCMSAuthorizer(harnessBridgeSA)))
	if _, err := bridgeSrv.SetComponentIntent(ctx, req); err != nil {
		t.Errorf("bridge SA SetComponentIntent unexpected error = %v", err)
	}
}
