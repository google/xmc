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

package helm

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/cli-runtime/pkg/resource"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage/driver"

	stable2026a "xmanager_cloud/deploy/profiles/stable-2026a"
	"xmanager_cloud/internal/logging"
)

// HelmActionRunner abstracts the execution of Helm release upgrades, uninstalls, and status queries via the Helm Go SDK or test doubles.
type HelmActionRunner interface {
	Upgrade(ctx context.Context, releaseName, namespace string, chartObj *chart.Chart, values map[string]any) (*release.Release, error)
	Uninstall(ctx context.Context, releaseName, namespace string) (*release.UninstallReleaseResponse, error)
	// Status returns the latest recorded revision of the release. Missing releases yield an error matching driver.ErrReleaseNotFound.
	Status(ctx context.Context, releaseName, namespace string) (*release.Release, error)
	// MissingResources returns "Kind/name" for every object in rel's manifest that no longer exists in the cluster.
	MissingResources(ctx context.Context, rel *release.Release) ([]string, error)
}

// componentReleaseMaxHistory caps the revisions Helm keeps per component release, so repeated upgrades (including
// drift repairs) never accumulate unbounded release Secrets. The SDK default of 0 means unlimited.
const componentReleaseMaxHistory = 10

// SDKHelmActionRunner executes Helm upgrades using the Helm Go SDK action engine.
type SDKHelmActionRunner struct {
	restClientGetter  genericclioptions.RESTClientGetter
	helmDriver        string
	chartPath         string
	chartPathResolver func(releaseName string) string
	chartObj          *chart.Chart
	chartCache        map[string]*chart.Chart
	mu                sync.RWMutex
}

var _ HelmActionRunner = (*SDKHelmActionRunner)(nil)

// SDKRunnerOption configures an SDKHelmActionRunner.
type SDKRunnerOption func(*SDKHelmActionRunner)

// WithDriver sets the Helm storage driver (e.g. "secrets", "configmaps", "memory").
func WithDriver(driver string) SDKRunnerOption {
	return func(r *SDKHelmActionRunner) {
		if driver != "" {
			r.helmDriver = driver
		}
	}
}

// WithRunnerChartPath sets the base or explicit filesystem path to the Helm chart to load.
func WithRunnerChartPath(path string) SDKRunnerOption {
	return func(r *SDKHelmActionRunner) { r.chartPath = path }
}

// WithRunnerChartPathResolver sets a dynamic chart path resolver keyed by Helm release name.
func WithRunnerChartPathResolver(resolver func(releaseName string) string) SDKRunnerOption {
	return func(r *SDKHelmActionRunner) { r.chartPathResolver = resolver }
}

// WithRunnerChartObj provides a pre-loaded Chart object for upgrades.
func WithRunnerChartObj(c *chart.Chart) SDKRunnerOption {
	return func(r *SDKHelmActionRunner) { r.chartObj = c }
}

// NewSDKHelmActionRunner creates an initialized SDKHelmActionRunner.
func NewSDKHelmActionRunner(getter genericclioptions.RESTClientGetter, opts ...SDKRunnerOption) *SDKHelmActionRunner {
	runner := &SDKHelmActionRunner{
		restClientGetter: getter,
		helmDriver:       "secrets",
		chartCache:       make(map[string]*chart.Chart),
	}
	for _, opt := range opts {
		opt(runner)
	}
	return runner
}

// Upgrade executes an upgrade action on the Helm release using the Helm Go SDK.
func (r *SDKHelmActionRunner) Upgrade(ctx context.Context, releaseName, namespace string, chartObj *chart.Chart, values map[string]any) (*release.Release, error) {
	actionConfig, err := r.newActionConfig(ctx, namespace)
	if err != nil {
		return nil, err
	}

	targetChart, err := r.resolveChart(releaseName, chartObj)
	if err != nil {
		return nil, err
	}

	histClient := action.NewHistory(actionConfig)
	histClient.Max = 1
	if _, histErr := histClient.Run(releaseName); errors.Is(histErr, driver.ErrReleaseNotFound) {
		install := action.NewInstall(actionConfig)
		install.ReleaseName = releaseName
		install.Namespace = namespace
		rel, err := install.RunWithContext(ctx, targetChart, values)
		if err != nil {
			return nil, fmt.Errorf("helm install failed for release %q in namespace %q: %w", releaseName, namespace, err)
		}
		return rel, nil
	}

	upgrade := newUpgradeAction(actionConfig, namespace)
	rel, err := upgrade.RunWithContext(ctx, releaseName, targetChart, values)
	if err != nil {
		return nil, fmt.Errorf("helm upgrade failed for release %q in namespace %q: %w", releaseName, namespace, err)
	}
	return rel, nil
}

// newUpgradeAction configures a Helm upgrade of a component release in namespace, capped at
// componentReleaseMaxHistory stored revisions.
func newUpgradeAction(actionConfig *action.Configuration, namespace string) *action.Upgrade {
	upgrade := action.NewUpgrade(actionConfig)
	upgrade.Namespace = namespace
	upgrade.MaxHistory = componentReleaseMaxHistory
	return upgrade
}

// Uninstall uninstalls a Helm release using the Helm Go SDK action engine.
func (r *SDKHelmActionRunner) Uninstall(ctx context.Context, releaseName, namespace string) (*release.UninstallReleaseResponse, error) {
	actionConfig, err := r.newActionConfig(ctx, namespace)
	if err != nil {
		return nil, err
	}

	uninstall := action.NewUninstall(actionConfig)
	rel, err := uninstall.Run(releaseName)
	if err != nil {
		return nil, fmt.Errorf("helm uninstall failed for release %q in namespace %q: %w", releaseName, namespace, err)
	}
	return rel, nil
}

// Status returns the latest recorded revision of a Helm release using the Helm Go SDK action engine.
func (r *SDKHelmActionRunner) Status(ctx context.Context, releaseName, namespace string) (*release.Release, error) {
	actionConfig, err := r.newActionConfig(ctx, namespace)
	if err != nil {
		return nil, err
	}

	rel, err := action.NewStatus(actionConfig).Run(releaseName)
	if err != nil {
		return nil, fmt.Errorf("helm status failed for release %q in namespace %q: %w", releaseName, namespace, err)
	}
	return rel, nil
}

// MissingResources parses rel's rendered manifest (hooks excluded) and GETs every object from the cluster, returning
// "Kind/name" for each one that is NotFound. Context cancellation, an object without a REST mapping, or any other
// lookup error aborts the check and is returned.
func (r *SDKHelmActionRunner) MissingResources(ctx context.Context, rel *release.Release) ([]string, error) {
	if rel == nil {
		return nil, errors.New("helm release is nil")
	}
	actionConfig, err := r.newActionConfig(ctx, rel.Namespace)
	if err != nil {
		return nil, err
	}
	resources, err := actionConfig.KubeClient.Build(strings.NewReader(rel.Manifest), false)
	if err != nil {
		return nil, fmt.Errorf("failed to parse manifest of Helm release %q: %w", rel.Name, err)
	}
	return findMissingResources(ctx, rel.Name, resources)
}

// findMissingResources GETs each resource and returns "Kind/name" for every one that is NotFound.
func findMissingResources(ctx context.Context, releaseName string, resources []*resource.Info) ([]string, error) {
	var missing []string
	for _, info := range resources {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// info.Get() dereferences info.Mapping, so an unmapped object cannot be looked up.
		if info.Mapping == nil {
			return nil, fmt.Errorf("%s %q of Helm release %q has no REST mapping", resourceKind(info), info.Name, releaseName)
		}
		getErr := info.Get()
		if apierrors.IsNotFound(getErr) {
			missing = append(missing, resourceKind(info)+"/"+info.Name)
			continue
		}
		if getErr != nil {
			return nil, fmt.Errorf("failed to get %s %q of Helm release %q: %w", resourceKind(info), info.Name, releaseName, getErr)
		}
	}
	return missing, nil
}

// resourceKind returns the Kind of info, preferring its REST mapping and falling back to the decoded object.
func resourceKind(info *resource.Info) string {
	if info.Mapping != nil {
		return info.Mapping.GroupVersionKind.Kind
	}
	if info.Object != nil {
		if kind := info.Object.GetObjectKind().GroupVersionKind().Kind; kind != "" {
			return kind
		}
	}
	return "Unknown"
}

// newActionConfig initializes a Helm action configuration scoped to namespace using the runner's storage driver.
func (r *SDKHelmActionRunner) newActionConfig(ctx context.Context, namespace string) (*action.Configuration, error) {
	if r == nil || r.restClientGetter == nil {
		return nil, errors.New("helm action runner or restClientGetter is uninitialized")
	}
	actionConfig := new(action.Configuration)
	if err := actionConfig.Init(r.restClientGetter, namespace, r.helmDriver, func(format string, v ...interface{}) {
		logging.Debug(ctx, fmt.Sprintf(format, v...))
	}); err != nil {
		return nil, fmt.Errorf("failed to initialize Helm action configuration: %w", err)
	}
	return actionConfig, nil
}

func (r *SDKHelmActionRunner) resolveTargetChartPath(releaseName string) string {
	if r.chartPathResolver != nil {
		if resolved := r.chartPathResolver(releaseName); resolved != "" {
			return resolved
		}
	}
	if r.chartPath == "" {
		return ""
	}
	return ResolveComponentChartPath(r.chartPath, ComponentRef{Name: componentNameFromRelease(releaseName)}, "")
}

func (r *SDKHelmActionRunner) resolveChart(releaseName string, chartObj *chart.Chart) (*chart.Chart, error) {
	if chartObj != nil {
		return chartObj, nil
	}
	r.mu.RLock()
	preloaded := r.chartObj
	r.mu.RUnlock()
	if preloaded != nil {
		return preloaded, nil
	}

	targetPath := r.resolveTargetChartPath(releaseName)
	if targetPath == "" {
		return defaultComponentChart(releaseName), nil
	}

	r.mu.RLock()
	cached := r.chartCache[targetPath]
	r.mu.RUnlock()
	if cached != nil {
		return cached, nil
	}

	loaded, err := loader.Load(targetPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load Helm chart from %q: %w", targetPath, err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.chartCache == nil {
		r.chartCache = make(map[string]*chart.Chart)
	}
	if existing := r.chartCache[targetPath]; existing != nil {
		return existing, nil
	}
	r.chartCache[targetPath] = loaded
	return loaded, nil
}

func defaultComponentChart(name string) *chart.Chart {
	root := "components/" + componentNameFromRelease(name)
	var files []*loader.BufferedFile
	_ = fs.WalkDir(stable2026a.EmbeddedCharts, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if data, readErr := stable2026a.EmbeddedCharts.ReadFile(p); readErr == nil {
			files = append(files, &loader.BufferedFile{Name: strings.TrimPrefix(p, root+"/"), Data: data})
		}
		return nil
	})
	if len(files) > 0 {
		if ch, err := loader.LoadFiles(files); err == nil {
			return ch
		}
	}
	return &chart.Chart{
		Metadata: &chart.Metadata{
			APIVersion: "v2",
			Name:       name,
			Version:    "0.1.0",
		},
	}
}
