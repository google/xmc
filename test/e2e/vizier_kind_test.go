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
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// BYO Vizier reference install (deploy/addons/hpo/vizier), registered as the stable-2026a
// `vizier` component.
const (
	// vizierImageEnv overrides the Vizier server image, which must already be in the local Docker
	// daemon because no public image exists. It defaults to vizierImageRepository tagged with the
	// chart appVersion, the image the chart's KinD overlay runs.
	vizierImageEnv              = "XMC_VIZIER_IMAGE"
	vizierImageRepository       = "xmc-vizier"
	vizierGRPCPort              = "7077"
	vizierChartRelPath          = "deploy/addons/hpo/vizier"
	vizierComponentChartRelPath = "deploy/profiles/stable-2026a/components/vizier"
)

func TestVizierReferenceInstallKind(t *testing.T) {
	image := requireLocalVizierImage(t)
	harness := setupKindClusterHarness(t, image)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	ns := createTestNamespace(ctx, t, harness, "xmc-vizier-e2e")
	installVizierKind(ctx, t, harness, ns, image)
	waitForDeploymentAvailable(ctx, t, harness, ns, "vizier")

	host := fmt.Sprintf("vizier-service.%s.svc.cluster.local", ns)
	endpoint := net.JoinHostPort(host, vizierGRPCPort)
	applyVizierComponent(ctx, t, harness, ns, endpoint)
	envVars := waitForComponentReady(ctx, t, harness, ns, "vizier")
	assertComponentEnvVars(t, envVars, endpoint, host, vizierGRPCPort, "")
}

// requireLocalVizierImage returns the Vizier image to test. A missing default image skips the
// test; a missing image named by vizierImageEnv fails it, so a mistyped override cannot pass.
func requireLocalVizierImage(t *testing.T) string {
	t.Helper()
	image := strings.TrimSpace(os.Getenv(vizierImageEnv))
	explicit := image != ""
	if !explicit {
		image = vizierImageRepository + ":" + vizierChartAppVersion(t)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := inspectLocalDockerImage(ctx, image)
	if err == nil {
		return image
	}
	hint := fmt.Sprintf("build it with `docker build -t %s %s/image`", image, vizierChartRelPath)
	if explicit {
		t.Fatalf("%s=%q is not in the local Docker daemon (%v); %s", vizierImageEnv, image, err, hint)
	}
	t.Skipf("Vizier image %q is not in the local Docker daemon (%v); %s or set %s", image, err, hint, vizierImageEnv)
	return ""
}

// vizierChartAppVersion returns the appVersion of the Vizier chart, the tag its images default to.
func vizierChartAppVersion(t *testing.T) string {
	t.Helper()
	path := filepath.Join(locateRepoPath(t, vizierChartRelPath), "Chart.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read %s: %v", path, err)
	}
	docs, err := decodeYAMLDocuments(string(raw))
	if err != nil || len(docs) != 1 {
		t.Fatalf("failed to decode %s into one document (got %d): %v", path, len(docs), err)
	}
	appVersion, ok := docs[0]["appVersion"].(string)
	if !ok || appVersion == "" {
		t.Fatalf("%s has no string appVersion: %v", path, docs[0]["appVersion"])
	}
	return appVersion
}

// installVizierKind renders the reference chart with its KinD overlay into ns, running image, and
// applies it.
func installVizierKind(ctx context.Context, t *testing.T, h *kindClusterHarness, ns, image string) {
	t.Helper()
	repository, tag, err := splitImageRef(image)
	if err != nil {
		t.Fatalf("invalid Vizier image %q: %v", image, err)
	}
	chartDir := locateRepoPath(t, vizierChartRelPath)
	rendered := renderHelmTemplate(ctx, t, "vizier", chartDir,
		"--namespace", ns,
		"--values", filepath.Join(chartDir, "values-kind.yaml"),
		"--set", "image.repository="+repository,
		"--set-string", "image.tag="+tag)
	applyRenderedManifests(ctx, t, h, rendered)
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
	applyRenderedManifests(ctx, t, h, rendered)
}
