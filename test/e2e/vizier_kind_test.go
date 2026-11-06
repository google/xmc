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
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Dev/test Vizier fixture (test/e2e/testdata/vizier), registered as the stable-2026a `vizier`
// component.
const (
	// vizierImageEnv overrides the fixture image, which must already be in the local Docker daemon
	// because no public image exists.
	vizierImageEnv              = "XMC_VIZIER_IMAGE"
	defaultVizierImage          = "xmc-vizier:0.1.24"
	vizierGRPCPort              = "7077"
	vizierManifestRelPath       = "test/e2e/testdata/vizier/vizier.yaml"
	vizierComponentChartRelPath = "deploy/profiles/stable-2026a/components/vizier"
)

func TestVizierFixtureKind(t *testing.T) {
	image := requireLocalVizierImage(t)
	harness := setupKindClusterHarness(t, image)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	ns := createTestNamespace(ctx, t, harness, "xmc-vizier-e2e")
	applyVizierFixture(ctx, t, harness, ns, image)
	waitForDeploymentAvailable(ctx, t, harness, ns, "vizier")

	host := fmt.Sprintf("vizier-service.%s.svc.cluster.local", ns)
	endpoint := net.JoinHostPort(host, vizierGRPCPort)
	applyVizierComponent(ctx, t, harness, ns, endpoint)
	envVars := waitForComponentReady(ctx, t, harness, ns, "vizier")
	assertComponentEnvVars(t, envVars, endpoint, host, vizierGRPCPort, "")
}

// requireLocalVizierImage returns the fixture image to test. A missing default image skips the
// test; a missing image named by vizierImageEnv fails it, so a mistyped override cannot pass.
func requireLocalVizierImage(t *testing.T) string {
	t.Helper()
	image := strings.TrimSpace(os.Getenv(vizierImageEnv))
	explicit := image != ""
	if !explicit {
		image = defaultVizierImage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := inspectLocalDockerImage(ctx, image)
	if err == nil {
		return image
	}
	hint := fmt.Sprintf("build it with `docker build -t %s test/e2e/testdata/vizier`", image)
	if explicit {
		t.Fatalf("%s=%q is not in the local Docker daemon (%v); %s", vizierImageEnv, image, err, hint)
	}
	t.Skipf("Vizier image %q is not in the local Docker daemon (%v); %s or set %s", image, err, hint, vizierImageEnv)
	return ""
}

// applyVizierFixture applies vizier.yaml into ns, running image and exposing vizier-service on a
// NodePort so that kindNodePortProber can reach it from the host. The manifest itself stays
// ClusterIP.
func applyVizierFixture(ctx context.Context, t *testing.T, h *kindClusterHarness, ns, image string) {
	t.Helper()
	raw, err := os.ReadFile(locateRepoPath(t, vizierManifestRelPath))
	if err != nil {
		t.Fatalf("failed to read %s: %v", vizierManifestRelPath, err)
	}
	objs, err := decodeYAMLDocuments(string(raw))
	if err != nil || len(objs) == 0 {
		t.Fatalf("failed to decode %s (got %d objects): %v", vizierManifestRelPath, len(objs), err)
	}
	for _, obj := range objs {
		adaptVizierFixtureObject(t, obj, ns, image)
		applyUnstructuredObject(ctx, t, h, obj)
	}
}

// adaptVizierFixtureObject places a fixture object in ns, sets the Deployment image and turns the
// Service into a NodePort.
func adaptVizierFixtureObject(t *testing.T, obj map[string]any, ns, image string) {
	t.Helper()
	u := &unstructured.Unstructured{Object: obj}
	u.SetNamespace(ns)
	var err error
	switch u.GetKind() {
	case "Deployment":
		err = setFirstContainerImage(obj, image)
	case "Service":
		err = unstructured.SetNestedField(obj, string(corev1.ServiceTypeNodePort), "spec", "type")
	}
	if err != nil {
		t.Fatalf("failed to adapt %s %s: %v", u.GetKind(), u.GetName(), err)
	}
}

// setFirstContainerImage sets the image of the first container of a Deployment object.
func setFirstContainerImage(obj map[string]any, image string) error {
	containers, found, err := unstructured.NestedSlice(obj, "spec", "template", "spec", "containers")
	if err != nil || !found || len(containers) == 0 {
		return fmt.Errorf("no containers (found=%v): %v", found, err)
	}
	container, ok := containers[0].(map[string]any)
	if !ok {
		return fmt.Errorf("container is %T, want a map", containers[0])
	}
	container["image"] = image
	return unstructured.SetNestedSlice(obj, containers, "spec", "template", "spec", "containers")
}

// applyVizierComponent renders the stable-2026a `vizier` Component blueprint into ns, enabled
// with its experimental stability acknowledged, and applies it.
func applyVizierComponent(ctx context.Context, t *testing.T, h *kindClusterHarness, ns, endpoint string) {
	t.Helper()
	rendered := renderHelmTemplate(ctx, t, "xmc-profile", locateRepoPath(t, vizierComponentChartRelPath),
		"--namespace", ns,
		"--set", "enabled=true",
		"--set", "acknowledgeExperimental=true",
		"--set", "deletionProtection=false",
		"--set", "parameters.endpoint="+endpoint)
	objs, err := decodeYAMLDocuments(rendered)
	if err != nil || len(objs) != 1 {
		t.Fatalf("failed to decode the vizier Component into one object (got %d): %v", len(objs), err)
	}
	applyUnstructuredObject(ctx, t, h, objs[0])
}

// waitForDeploymentAvailable waits until the named Deployment reports its Available condition.
func waitForDeploymentAvailable(ctx context.Context, t *testing.T, h *kindClusterHarness, ns, name string) {
	t.Helper()
	var last *appsv1.Deployment
	var lastErr error
	ok := waitUntil(ctx, 3*time.Minute, time.Second, func(pollCtx context.Context) bool {
		last, lastErr = h.clientset.AppsV1().Deployments(ns).Get(pollCtx, name, metav1.GetOptions{})
		if lastErr != nil {
			return false
		}
		for _, cond := range last.Status.Conditions {
			if cond.Type == appsv1.DeploymentAvailable && cond.Status == corev1.ConditionTrue {
				return true
			}
		}
		return false
	})
	if !ok {
		t.Fatalf("timed out waiting for Deployment %s/%s to become Available (last err: %v); last object: %+v", ns, name, lastErr, last)
	}
}
