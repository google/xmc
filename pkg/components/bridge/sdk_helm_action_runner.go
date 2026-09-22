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
	"errors"
	"fmt"
	"strings"
	"sync"

	"k8s.io/cli-runtime/pkg/genericclioptions"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage/driver"

	"xmanager_cloud/logging/logging"
)

// HelmActionRunner abstracts the execution of Helm release upgrades and uninstalls via the Helm Go SDK or test doubles.
type HelmActionRunner interface {
	Upgrade(ctx context.Context, releaseName, namespace string, chartObj *chart.Chart, values map[string]any) (*release.Release, error)
	Uninstall(ctx context.Context, releaseName, namespace string) (*release.UninstallReleaseResponse, error)
}

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
	if r == nil || r.restClientGetter == nil {
		return nil, errors.New("helm action runner or restClientGetter is uninitialized")
	}
	actionConfig := new(action.Configuration)
	if err := actionConfig.Init(r.restClientGetter, namespace, r.helmDriver, func(format string, v ...interface{}) {
		logging.Debug(ctx, fmt.Sprintf(format, v...))
	}); err != nil {
		return nil, fmt.Errorf("failed to initialize Helm action configuration: %w", err)
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

	upgrade := action.NewUpgrade(actionConfig)
	upgrade.Namespace = namespace
	rel, err := upgrade.RunWithContext(ctx, releaseName, targetChart, values)
	if err != nil {
		return nil, fmt.Errorf("helm upgrade failed for release %q in namespace %q: %w", releaseName, namespace, err)
	}
	return rel, nil
}

// Uninstall uninstalls a Helm release using the Helm Go SDK action engine.
func (r *SDKHelmActionRunner) Uninstall(ctx context.Context, releaseName, namespace string) (*release.UninstallReleaseResponse, error) {
	if r == nil || r.restClientGetter == nil {
		return nil, errors.New("helm action runner or restClientGetter is uninitialized")
	}
	actionConfig := new(action.Configuration)
	if err := actionConfig.Init(r.restClientGetter, namespace, r.helmDriver, func(format string, v ...interface{}) {
		logging.Debug(ctx, fmt.Sprintf(format, v...))
	}); err != nil {
		return nil, fmt.Errorf("failed to initialize Helm action configuration: %w", err)
	}

	uninstall := action.NewUninstall(actionConfig)
	rel, err := uninstall.Run(releaseName)
	if err != nil {
		return nil, fmt.Errorf("helm uninstall failed for release %q in namespace %q: %w", releaseName, namespace, err)
	}
	return rel, nil
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
	compName := strings.TrimPrefix(releaseName, "xmc-cmp-")
	return ResolveComponentChartPath(r.chartPath, ComponentRef{Name: compName}, "")
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
	return &chart.Chart{
		Metadata: &chart.Metadata{
			APIVersion: "v2",
			Name:       name,
			Version:    "0.1.0",
		},
	}
}
