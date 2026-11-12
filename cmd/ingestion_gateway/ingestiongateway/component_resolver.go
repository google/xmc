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

package ingestiongateway

import (
	"cmp"
	"context"
	"fmt"
	"net"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/pkg/components/crreader"
)

const (
	// redpandaComponentName is the canonical Component CR name for the Redpanda broker.
	redpandaComponentName = "redpanda"
)

// ComponentGetter retrieves a Kubernetes Component Custom Resource by namespace and name.
type ComponentGetter = crreader.Getter

// NewK8sComponentGetter constructs a ComponentGetter backed by the in-cluster or default kubeconfig.
func NewK8sComponentGetter() (ComponentGetter, error) {
	return crreader.NewK8sGetter()
}

// ResolveRedpandaComponentConfig updates cfg with broker endpoints and default topic from the
// redpanda Component CR when the message bus is Redpanda and the component is enabled.
func ResolveRedpandaComponentConfig(ctx context.Context, cfg *Config, getter ComponentGetter) error {
	if !shouldResolveRedpandaComponent(ctx, cfg) {
		return nil
	}
	if getter == nil {
		return fmt.Errorf("component getter cannot be nil")
	}

	ns := crreader.ResolveNamespace(cfg.ComponentsNamespace)

	logging.Info(ctx, "Resolving Redpanda configuration from Component CR", "namespace", ns, "component", redpandaComponentName)
	comp, err := getter.GetComponent(ctx, ns, redpandaComponentName)
	if err != nil {
		return fmt.Errorf("failed to get component %s/%s: %w", ns, redpandaComponentName, err)
	}
	if crreader.IsDisabled(comp) {
		logDisabledRedpandaComponent(ctx, cfg, ns, comp)
		return nil
	}

	originalBrokers := cfg.RedpandaBrokers
	originalTopic := cfg.TopicName
	applyRedpandaComponentOverrides(ctx, cfg, ns, comp)
	if err := cfg.Validate(); err != nil {
		cfg.RedpandaBrokers = originalBrokers
		cfg.TopicName = originalTopic
		logging.Warn(ctx, "Validation failed after applying Redpanda Component CR overrides; restored static configuration",
			"namespace", ns, "component", redpandaComponentName, "error", err,
			"restored_brokers", cfg.RedpandaBrokers, "restored_topic", cfg.TopicName)
		return err
	}
	logging.Info(ctx, "Successfully resolved and validated Redpanda configuration from Component CR",
		"namespace", ns, "component", redpandaComponentName,
		"brokers", cfg.RedpandaBrokers, "topic", cfg.TopicName)
	return nil
}

func shouldResolveRedpandaComponent(ctx context.Context, cfg *Config) bool {
	if cfg == nil {
		logging.Info(ctx, "Skipping Redpanda Component CR resolution: config is nil")
		return false
	}
	if !cfg.UsesRedpanda() {
		logging.Info(ctx, "Skipping Redpanda Component CR resolution: message bus is not redpanda",
			"message_bus", cfg.messageBusOrDefault())
		return false
	}
	return true
}

func logDisabledRedpandaComponent(ctx context.Context, cfg *Config, ns string, comp *unstructured.Unstructured) {
	specEnabled, _, _ := crreader.SpecEnabled(comp)
	statusState, _ := crreader.StatusState(comp)
	logging.Info(ctx, "Redpanda Component CR is disabled; preserving static ingestion-gateway configuration",
		"namespace", ns, "component", redpandaComponentName,
		"spec_enabled", specEnabled, "status_state", statusState,
		"fallback_brokers", cfg.RedpandaBrokers, "fallback_topic", cfg.TopicName)
}

func applyRedpandaComponentOverrides(ctx context.Context, cfg *Config, ns string, comp *unstructured.Unstructured) {
	params, err := crreader.Parameters(comp)
	if err != nil {
		logging.Warn(ctx, "Failed to parse spec.parameters from Component CR", "error", err)
	}
	envVars, err := crreader.AccessEnvVars(comp)
	if err != nil {
		logging.Warn(ctx, "Failed to parse status.access.envVars from Component CR", "error", err)
	}
	statusState, err := crreader.StatusState(comp)
	if err != nil {
		logging.Warn(ctx, "Failed to parse status.state from Component CR", "error", err)
	}

	if brokers, source := resolveBrokersFromComponent(params, envVars); len(brokers) > 0 {
		cfg.RedpandaBrokers = brokers
		logging.Info(ctx, "Resolved Redpanda brokers from Component CR",
			"namespace", ns, "component", redpandaComponentName,
			"source", source, "status_state", statusState, "brokers", brokers)
	} else {
		logging.Warn(ctx, "Redpanda Component CR has no broker endpoint configured; retaining static brokers",
			"namespace", ns, "component", redpandaComponentName, "fallback_brokers", cfg.RedpandaBrokers)
	}
	if topic := strings.TrimSpace(params["defaultTopic"]); topic != "" {
		cfg.TopicName = topic
		logging.Info(ctx, "Resolved default topic from Component CR",
			"namespace", ns, "component", redpandaComponentName, "topic", topic)
	}
}

func resolveBrokersFromComponent(params, envVars map[string]string) ([]string, string) {
	specPort := strings.TrimSpace(params["kafkaPort"])
	envPort := cmp.Or(strings.TrimSpace(envVars["PORT"]), specPort)

	envBrokers := parseBrokersWithPort(envVars["ENDPOINT"], envPort)
	if len(envBrokers) > 1 {
		return envBrokers, "status.access.envVars.ENDPOINT"
	}

	specBrokers := parseBrokersWithPort(params["brokerEndpoints"], specPort)
	if len(specBrokers) > 1 {
		return specBrokers, "spec.parameters.brokerEndpoints"
	}

	if len(envBrokers) > 0 {
		return envBrokers, "status.access.envVars.ENDPOINT"
	}
	if len(specBrokers) > 0 {
		return specBrokers, "spec.parameters.brokerEndpoints"
	}
	return nil, ""
}

func parseBrokersWithPort(raw, port string) []string {
	brokers := parseBrokers(raw)
	for i, b := range brokers {
		brokers[i] = ensureEndpointPort(b, port)
	}
	return brokers
}

func ensureEndpointPort(endpoint, port string) string {
	endpoint = strings.TrimSpace(endpoint)
	port = strings.TrimSpace(port)
	if endpoint == "" || port == "" {
		return endpoint
	}
	if _, _, err := net.SplitHostPort(endpoint); err == nil {
		return endpoint
	}
	cleanHost := strings.TrimPrefix(strings.TrimSuffix(endpoint, "]"), "[")
	if cleanHost == "" {
		return ""
	}
	return net.JoinHostPort(cleanHost, port)
}
