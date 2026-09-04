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
	"sync"

	"k8s.io/cli-runtime/pkg/genericclioptions"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/release"

	"xmanager_cloud/logging/logging"
)

// HelmActionRunner abstracts the execution of Helm release upgrades via the Helm Go SDK or test doubles.
type HelmActionRunner interface {
	Upgrade(ctx context.Context, releaseName, namespace string, chartObj *chart.Chart, values map[string]any) (*release.Release, error)
}

// SDKHelmActionRunner executes Helm upgrades using the Helm Go SDK action engine.
type SDKHelmActionRunner struct {
	restClientGetter genericclioptions.RESTClientGetter
	helmDriver       string
	chartPath        string
	chartObj         *chart.Chart
	mu               sync.RWMutex
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

// WithRunnerChartPath sets the filesystem path to the Helm chart to load.
func WithRunnerChartPath(path string) SDKRunnerOption {
	return func(r *SDKHelmActionRunner) { r.chartPath = path }
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

	upgrade := action.NewUpgrade(actionConfig)
	upgrade.Namespace = namespace
	upgrade.Install = true
	upgrade.ReuseValues = true

	targetChart, err := r.resolveChart(releaseName, chartObj)
	if err != nil {
		return nil, err
	}

	rel, err := upgrade.RunWithContext(ctx, releaseName, targetChart, values)
	if err != nil {
		return nil, fmt.Errorf("helm upgrade failed for release %q in namespace %q: %w", releaseName, namespace, err)
	}
	return rel, nil
}

func (r *SDKHelmActionRunner) resolveChart(releaseName string, chartObj *chart.Chart) (*chart.Chart, error) {
	if chartObj != nil {
		return chartObj, nil
	}
	r.mu.RLock()
	cached := r.chartObj
	r.mu.RUnlock()
	if cached != nil {
		return cached, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.chartObj == nil && r.chartPath != "" {
		loaded, err := loader.Load(r.chartPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load Helm chart from %q: %w", r.chartPath, err)
		}
		r.chartObj = loaded
	}
	if r.chartObj != nil {
		return r.chartObj, nil
	}
	return nil, fmt.Errorf("no chart object or valid chartPath specified for release %q", releaseName)
}
