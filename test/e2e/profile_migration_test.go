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
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/utils/ptr"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/bridge"
)

const (
	profileMigrationTestdata = "test/e2e/testdata/profile_migration"
	// gatewayImageRepository is the public image of the rolling-gateway stand-in chart.
	gatewayImageRepository = "registry.k8s.io/e2e-test-images/agnhost"
	// baselineGatewayImageTag is the stable-2026a global.imageTag; the overlay fixture bumps it.
	baselineGatewayImageTag = "2.52"
	gatewayComponent        = "ingestion-gateway"
	clickhouseComponent     = "clickhouse"
	gatewaySelector         = "app.kubernetes.io/name=" + gatewayComponent
	// gatewayReplicas reaches the rolling-gateway chart as the replicas Component parameter.
	gatewayReplicas = 2
	gatewayProbePod = "gateway-http-probe"
	// postRolloutProbeIterations is how many successful gateway probe requests must follow the exit of
	// the last old gateway Pod. At about 5 requests/s this spans more than the chart's 10s
	// --delay-shutdown plus margin.
	postRolloutProbeIterations = 75
	// migrationTimeout bounds the test below the Bazel "long" timeout so cleanup still runs.
	migrationTimeout = 10 * time.Minute
)

// profileCatalogComponents are the Component CRs the stable-2026a profile renders.
var profileCatalogComponents = []string{"redpanda", clickhouseComponent, gatewayComponent}

// enabledProfileComponents are the Component CRs the admin values fixture enables and the suite
// migrates.
var enabledProfileComponents = []string{gatewayComponent}

// TestProfileMigrationE2E upgrades the Component CRs of the real stable-2026a profile to a test-only
// stable-2026b overlay on KinD, with an in-process bridge whose real Helm manager actuates the 1P
// ingestion-gateway through a public-image stand-in chart (parameters.chartPath). During the
// migration a continuous HTTP probe (about 5 requests/s, a new connection per request) hits the
// gateway Service and must record zero failed requests. Afterwards the suite verifies the
// ReplicaSet rollover and the CR status and CMS sync-back at the new generation.
func TestProfileMigrationE2E(t *testing.T) {
	overlay := loadProfileOverlay(t)
	baselineImage, candidateImage := gatewayImage(baselineGatewayImageTag), gatewayImage(overlay.ImageTag)
	h := newKindClusterHarness(t, kindHarnessConfig{requiredImages: []string{baselineImage, candidateImage}, helmActuation: true})
	ctx, cancel := context.WithTimeout(context.Background(), migrationTimeout)
	defer cancel()

	ns := createTestNamespace(ctx, t, h, "xmc-profile-mig")
	baseline := deployBaselineProfile(ctx, t, h, ns, baselineImage)
	preUpgrade := startGatewayProbe(ctx, t, h, ns, baselineImage)

	applyComponents(ctx, t, h, renderCandidateProfile(ctx, t, ns, overlay))
	waitForGatewayRollout(ctx, t, h, ns, candidateImage)
	newPods := waitForOldGatewayPodsGone(ctx, t, h, ns, candidateImage)
	postRollout := tallyWorkloadLogs(fetchPodLogs(h, ns, gatewayProbePod))
	upgraded := waitForUpgradedComponents(ctx, t, h, baseline)

	t.Run("ZeroDowntimeRollover", func(t *testing.T) {
		assertReplicaSetRollover(ctx, t, h, ns, candidateImage)
		assertGatewayProbeUninterrupted(ctx, t, h, ns, preUpgrade, postRollout, newPods)
	})
	t.Run("StatusSyncBack", func(t *testing.T) {
		runPerComponent(t, upgraded, func(t *testing.T, name string, u *unstructured.Unstructured) {
			assertUpgradedStatus(ctx, t, h, baseline[name], u, overlay)
		})
	})
}

// runPerComponent runs fn as one subtest per Component of comps, in name order.
func runPerComponent(t *testing.T, comps map[string]*unstructured.Unstructured, fn func(*testing.T, string, *unstructured.Unstructured)) {
	t.Helper()
	for _, name := range slices.Sorted(maps.Keys(comps)) {
		t.Run(name, func(t *testing.T) { fn(t, name, comps[name]) })
	}
}

func gatewayImage(tag string) string {
	return gatewayImageRepository + ":" + tag
}

func loadProfileOverlay(t *testing.T) profileOverlay {
	t.Helper()
	raw, err := os.ReadFile(locateRepoPath(t, profileMigrationTestdata+"/stable-2026b-overlay.yaml"))
	if err != nil {
		t.Fatalf("failed to read profile overlay fixture: %v", err)
	}
	var overlay profileOverlay
	if err := k8syaml.Unmarshal(raw, &overlay); err != nil {
		t.Fatalf("failed to parse profile overlay fixture: %v", err)
	}
	if overlay.Profile == "" || overlay.ImageTag == "" || overlay.ImageTag == baselineGatewayImageTag || len(overlay.ChartVersions) == 0 {
		t.Fatalf("invalid profile overlay fixture %+v: need profile, chartVersions and an imageTag other than %q", overlay, baselineGatewayImageTag)
	}
	return overlay
}

// deployBaselineProfile applies every Component CR rendered from stable-2026a and waits until the
// enabled ones are Ready and the gateway Deployment runs baselineImage. It returns the enabled
// Components by name.
func deployBaselineProfile(ctx context.Context, t *testing.T, h *kindClusterHarness, ns, baselineImage string) map[string]*unstructured.Unstructured {
	t.Helper()
	applyComponents(ctx, t, h, renderProfileComponents(ctx, t, ns, baselineGatewayImageTag))
	ready := make(map[string]*unstructured.Unstructured, len(enabledProfileComponents))
	for _, name := range enabledProfileComponents {
		ready[name] = waitForComponent(ctx, t, h, ns, name, "Ready", isExistingComponentReady)
		assertCMSComponent(ctx, t, h, ready[name], componentspb.ComponentState_COMPONENT_STATE_ENABLED)
	}
	waitForGatewayRollout(ctx, t, h, ns, baselineImage)
	return ready
}

// renderProfileComponents renders the Component CRs of deploy/profiles/stable-2026a with helm
// template, layering the per-run values over the static admin values fixture.
func renderProfileComponents(ctx context.Context, t *testing.T, ns, imageTag string) []map[string]any {
	t.Helper()
	profileDir := locateRepoPath(t, "deploy/profiles/stable-2026a")
	adminValues := locateRepoPath(t, profileMigrationTestdata+"/stable-2026a-values.yaml")
	rendered := renderHelmTemplate(ctx, t, "xmc-profile", profileDir, "--namespace", ns, "-f", adminValues, "-f", writeRunValues(t, ns, imageTag))
	objs, err := decodeYAMLDocuments(rendered)
	if err != nil {
		t.Fatalf("failed to decode rendered profile: %v", err)
	}
	if len(objs) != len(profileCatalogComponents) {
		t.Fatalf("rendered profile has %d objects, want one Component CR per %v:\n%s", len(objs), profileCatalogComponents, rendered)
	}
	return objs
}

// renderCandidateProfile renders stable-2026a at the overlay image tag and applies the overlay.
func renderCandidateProfile(ctx context.Context, t *testing.T, ns string, overlay profileOverlay) []map[string]any {
	t.Helper()
	candidate, err := applyProfileOverlay(renderProfileComponents(ctx, t, ns, overlay.ImageTag), overlay)
	if err != nil {
		t.Fatalf("failed to apply profile overlay: %v", err)
	}
	return candidate
}

// writeRunValues writes the per-run profile values: every Component in ns, the gateway image tag, and
// the rolling-gateway stand-in chart, image repository and replica count.
func writeRunValues(t *testing.T, ns, imageTag string) string {
	t.Helper()
	gatewayParams := map[string]any{
		"chartPath":       locateRepoPath(t, profileMigrationTestdata+"/rolling-gateway"),
		"imageRepository": gatewayImageRepository,
		"replicas":        gatewayReplicas,
	}
	values := map[string]any{
		"global": map[string]any{"imageTag": imageTag},
		"components": map[string]any{
			"redpanda":          map[string]any{"namespace": ns},
			clickhouseComponent: map[string]any{"namespace": ns},
			gatewayComponent:    map[string]any{"namespace": ns, "parameters": gatewayParams},
		},
	}
	raw, err := json.Marshal(values)
	if err != nil {
		t.Fatalf("failed to marshal run values: %v", err)
	}
	path := filepath.Join(t.TempDir(), "run-values.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("failed to write run values: %v", err)
	}
	return path
}

func applyComponents(ctx context.Context, t *testing.T, h *kindClusterHarness, objs []map[string]any) {
	t.Helper()
	for _, obj := range objs {
		applyUnstructuredObject(ctx, t, h, obj)
	}
}

func componentGeneration(u *unstructured.Unstructured) int64 {
	return bridge.ParseComponentGenerationFromUnstructured(u.Object["status"])
}

// startGatewayProbe starts the continuous gateway HTTP probe and returns its tally once it has
// completed a few successful requests.
func startGatewayProbe(ctx context.Context, t *testing.T, h *kindClusterHarness, ns, probeImage string) logTally {
	t.Helper()
	startWorkloadPod(ctx, t, h, workloadPodSpec{
		Namespace: ns, Name: gatewayProbePod, Image: probeImage, Command: []string{"sh", "-c", gatewayProbeScript},
		AccessEnvVars:   map[string]string{"GATEWAY_URL": fmt.Sprintf("http://%s.%s.svc.cluster.local/hostname", gatewayComponent, ns)},
		SecurityContext: restrictedContainerSecurity(),
		Resources:       smallContainerResources(),
	})
	tally, _ := waitForWorkloadIterations(ctx, t, h, ns, gatewayProbePod, 3)
	return tally
}

// restrictedContainerSecurity runs a container as the nobody user on a read-only root filesystem
// without privilege escalation, capabilities or an unconfined seccomp profile.
func restrictedContainerSecurity() *corev1.SecurityContext {
	return &corev1.SecurityContext{
		RunAsNonRoot: ptr.To(true), RunAsUser: ptr.To[int64](65534), AllowPrivilegeEscalation: ptr.To(false),
		Capabilities:           &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		SeccompProfile:         &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		ReadOnlyRootFilesystem: ptr.To(true),
	}
}

func smallContainerResources() corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("10m"), corev1.ResourceMemory: resource.MustParse("16Mi")},
		Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("200m"), corev1.ResourceMemory: resource.MustParse("64Mi")},
	}
}

// gatewayProbeScript requests GATEWAY_URL (agnhost /hostname, which returns the serving Pod name)
// every 200ms with a 2s timeout, opening a new connection per request.
const gatewayProbeScript = `while true; do
  if body=$(curl -fsS -m 2 "$GATEWAY_URL" 2>&1); then echo "WORKLOAD_OK host=$body"; else echo "WORKLOAD_FAIL curl_exit=$? $body"; fi
  sleep 0.2
done`

// waitForWorkloadIterations waits until the running workload Pod has logged at least minOK successful
// iterations and returns its tally and logs.
func waitForWorkloadIterations(ctx context.Context, t *testing.T, h *kindClusterHarness, ns, pod string, minOK int) (logTally, string) {
	t.Helper()
	var tally logTally
	var logs string
	ok := waitUntil(ctx, 2*time.Minute, time.Second, func(context.Context) bool {
		logs = fetchPodLogs(h, ns, pod)
		tally = tallyWorkloadLogs(logs)
		return tally.ok >= minOK
	})
	if !ok {
		t.Fatalf("workload %s/%s logged %d successful iterations, want >= %d; %s", ns, pod, tally.ok, minOK, workloadFailureExcerpt(logs, 10))
	}
	return tally, logs
}

// assertGatewayProbeUninterrupted verifies that the gateway probe failed no request since the
// upgrade started, made postRolloutProbeIterations more successful requests after the last old Pod
// exited, and that one of those was served by a Pod of the new ReplicaSet.
func assertGatewayProbeUninterrupted(ctx context.Context, t *testing.T, h *kindClusterHarness, ns string, preUpgrade, postRollout logTally, newPods map[string]bool) {
	t.Helper()
	after, logs := waitForWorkloadIterations(ctx, t, h, ns, gatewayProbePod, postRollout.ok+postRolloutProbeIterations)
	assertNoFailuresSince(t, gatewayProbePod, preUpgrade, after, logs)
	hosts := workloadOKHosts(logs)
	if !slices.ContainsFunc(hosts[postRollout.ok:], func(host string) bool { return newPods[host] }) {
		t.Errorf("no gateway probe request after the rollout was served by a new Pod %v; %s", slices.Sorted(maps.Keys(newPods)), workloadFailureExcerpt(logs, 10))
	}
	assertWorkloadRunning(ctx, t, h, ns, gatewayProbePod)
}

func assertNoFailuresSince(t *testing.T, pod string, before, after logTally, logs string) {
	t.Helper()
	if failed := after.fail - before.fail; failed != 0 {
		t.Errorf("workload %s failed %d of %d iterations since the upgrade started; %s", pod, failed, after.ok+after.fail-before.ok-before.fail, workloadFailureExcerpt(logs, 10))
	}
}

func assertWorkloadRunning(ctx context.Context, t *testing.T, h *kindClusterHarness, ns, pod string) {
	t.Helper()
	p, err := h.clientset.CoreV1().Pods(ns).Get(ctx, pod, metav1.GetOptions{})
	if err != nil {
		t.Errorf("failed to get workload Pod %s/%s: %v", ns, pod, err)
		return
	}
	if p.Status.Phase != corev1.PodRunning {
		t.Errorf("workload %s/%s phase = %s after the migration, want Running", ns, pod, p.Status.Phase)
	}
}

// firstContainerImage returns the image of the first container of spec, or "" if it has none.
func firstContainerImage(spec corev1.PodSpec) string {
	if len(spec.Containers) == 0 {
		return ""
	}
	return spec.Containers[0].Image
}

// waitForGatewayRollout waits until the gateway Deployment runs wantImage on all gatewayReplicas
// replicas with no replicas of an older revision left.
func waitForGatewayRollout(ctx context.Context, t *testing.T, h *kindClusterHarness, ns, wantImage string) {
	t.Helper()
	var dep *appsv1.Deployment
	ok := waitUntil(ctx, 3*time.Minute, time.Second, func(pollCtx context.Context) bool {
		var err error
		dep, err = h.clientset.AppsV1().Deployments(ns).Get(pollCtx, gatewayComponent, metav1.GetOptions{})
		return err == nil && firstContainerImage(dep.Spec.Template.Spec) == wantImage && isDeploymentRolledOut(dep)
	})
	if !ok {
		t.Fatalf("timed out waiting for Deployment %s/%s to roll out %s on %d replicas; last: %+v", ns, gatewayComponent, wantImage, gatewayReplicas, dep)
	}
}

func isDeploymentRolledOut(dep *appsv1.Deployment) bool {
	s := dep.Status
	return ptr.Deref(dep.Spec.Replicas, 1) == gatewayReplicas && s.ObservedGeneration >= dep.Generation &&
		s.Replicas == gatewayReplicas && s.UpdatedReplicas == gatewayReplicas && s.AvailableReplicas == gatewayReplicas
}

// waitForOldGatewayPodsGone waits until no gateway Pod with another image than wantImage exists,
// terminating ones included, and returns the names of the gatewayReplicas Pods running wantImage.
func waitForOldGatewayPodsGone(ctx context.Context, t *testing.T, h *kindClusterHarness, ns, wantImage string) map[string]bool {
	t.Helper()
	var current map[string]bool
	var stale []string
	ok := waitUntil(ctx, 2*time.Minute, time.Second, func(pollCtx context.Context) bool {
		list, err := h.clientset.CoreV1().Pods(ns).List(pollCtx, metav1.ListOptions{LabelSelector: gatewaySelector})
		if err != nil {
			return false
		}
		current, stale = make(map[string]bool, gatewayReplicas), nil
		for _, p := range list.Items {
			if firstContainerImage(p.Spec) != wantImage {
				stale = append(stale, p.Name)
				continue
			}
			current[p.Name] = true
		}
		return len(stale) == 0 && len(current) == gatewayReplicas
	})
	if !ok {
		t.Fatalf("timed out waiting for old gateway Pods to exit: stale %v, current %v", stale, current)
	}
	return current
}

// assertReplicaSetRollover verifies that the upgrade rolled the gateway onto a new ReplicaSet running
// wantImage on all gatewayReplicas replicas, and scaled each older ReplicaSet to zero.
func assertReplicaSetRollover(ctx context.Context, t *testing.T, h *kindClusterHarness, ns, wantImage string) {
	t.Helper()
	list, err := h.clientset.AppsV1().ReplicaSets(ns).List(ctx, metav1.ListOptions{LabelSelector: gatewaySelector})
	if err != nil {
		t.Fatalf("failed to list gateway ReplicaSets: %v", err)
	}
	if len(list.Items) < 2 {
		t.Fatalf("gateway has %d ReplicaSet(s), want a new one plus the scaled-down baseline", len(list.Items))
	}
	for _, rs := range list.Items {
		replicas, image := ptr.Deref(rs.Spec.Replicas, 0), firstContainerImage(rs.Spec.Template.Spec)
		if image == wantImage && (replicas != gatewayReplicas || rs.Status.AvailableReplicas != gatewayReplicas) {
			t.Errorf("new ReplicaSet %s: %d/%d replicas available, want %d/%d", rs.Name, rs.Status.AvailableReplicas, replicas, gatewayReplicas, gatewayReplicas)
		}
		if image != wantImage && (replicas != 0 || rs.Status.Replicas != 0) {
			t.Errorf("old ReplicaSet %s (%s): spec %d / status %d replicas, want scaled to 0", rs.Name, image, replicas, rs.Status.Replicas)
		}
	}
}

// waitForUpgradedComponents waits until every baseline Component is Ready again at a CR generation
// and CMS component generation past its baseline ones, and returns them by name.
func waitForUpgradedComponents(ctx context.Context, t *testing.T, h *kindClusterHarness, baseline map[string]*unstructured.Unstructured) map[string]*unstructured.Unstructured {
	t.Helper()
	upgraded := make(map[string]*unstructured.Unstructured, len(baseline))
	for name, base := range baseline {
		upgraded[name] = waitForComponent(ctx, t, h, base.GetNamespace(), name, "Ready at the upgraded generation", func(u *unstructured.Unstructured) bool {
			return isExistingComponentReady(u) && u.GetGeneration() > base.GetGeneration() && componentGeneration(u) > componentGeneration(base)
		})
	}
	return upgraded
}

// assertUpgradedStatus verifies the sync-back of an upgraded Component: overlay spec applied over the
// baseline version, CMS ENABLED at the CR generation, status.componentGeneration equal to the CMS
// generation, Ready=True (ComponentHealthy) without Stalled.
func assertUpgradedStatus(ctx context.Context, t *testing.T, h *kindClusterHarness, base, u *unstructured.Unstructured, overlay profileOverlay) {
	t.Helper()
	assertOverlaySpec(t, base, u, overlay)
	rec, err := h.cmsServer.Store().GetComponent(ctx, u.GetName())
	if err != nil {
		t.Fatalf("CMS GetComponent(%s) failed: %v", u.GetName(), err)
	}
	if rec == nil {
		t.Fatalf("CMS GetComponent(%s) returned a nil record", u.GetName())
	}
	if rec.State != componentspb.ComponentState_COMPONENT_STATE_ENABLED || rec.Generation != u.GetGeneration() || componentGeneration(u) != rec.Generation {
		t.Errorf("%s CMS state = %v, generation = %d; CR generation = %d, status.componentGeneration = %d; want ENABLED with all generations equal", u.GetName(), rec.State, rec.Generation, u.GetGeneration(), componentGeneration(u))
	}
	assertCondition(t, u, bridge.ConditionTypeReady, bridge.ConditionStatusTrue, bridge.ReasonComponentHealthy)
	if got := conditionField(u, bridge.ConditionTypeStalled, "status"); got == bridge.ConditionStatusTrue {
		t.Errorf("%s Stalled = %s (reason %q) after upgrade, want not stalled", u.GetName(), got, conditionField(u, bridge.ConditionTypeStalled, "reason"))
	}
}

// assertOverlaySpec verifies that u carries the overlay profile label and chart version, and that
// its spec.version moved off the baseline one.
func assertOverlaySpec(t *testing.T, base, u *unstructured.Unstructured, overlay profileOverlay) {
	t.Helper()
	want := overlay.ChartVersions[u.GetName()]
	chartVersion, _, _ := unstructured.NestedString(u.Object, "spec", "chartVersion")
	version, _, _ := unstructured.NestedString(u.Object, "spec", "version")
	baseVersion, _, _ := unstructured.NestedString(base.Object, "spec", "version")
	if label := u.GetLabels()[profileLabel]; label != overlay.Profile || chartVersion != want || version != want || version == baseVersion {
		t.Errorf("%s profile label = %q, chartVersion = %q, version = %q (baseline %q); want %q, %q, %q", u.GetName(), label, chartVersion, version, baseVersion, overlay.Profile, want, want)
	}
}
