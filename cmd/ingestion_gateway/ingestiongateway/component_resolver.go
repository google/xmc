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
	"context"
	"fmt"
	"net"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"xmanager_cloud/logging/logging"
)

const (
	// redpandaComponentName is the canonical Component CR name for the Redpanda broker.
	redpandaComponentName = "redpanda"
	// defaultComponentsNamespace is the default Kubernetes namespace for XMC Component CRs.
	defaultComponentsNamespace = "xmc"
)

var componentGVR = schema.GroupVersionResource{
	Group:    "xmc.dev",
	Version:  "v1alpha1",
	Resource: "components",
}

// ComponentGetter retrieves a Kubernetes Component Custom Resource by namespace and name.
type ComponentGetter interface {
	GetComponent(ctx context.Context, namespace, name string) (*unstructured.Unstructured, error)
}

type k8sComponentGetter struct {
	client dynamic.Interface
}

// NewK8sComponentGetter constructs a ComponentGetter backed by the in-cluster or default kubeconfig.
func NewK8sComponentGetter() (ComponentGetter, error) {
	restCfg, err := rest.InClusterConfig()
	if err != nil {
		rules := clientcmd.NewDefaultClientConfigLoadingRules()
		restCfg, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{}).ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to build kubernetes client config: %w", err)
		}
	}
	dynClient, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic kubernetes client: %w", err)
	}
	return &k8sComponentGetter{client: dynClient}, nil
}

// GetComponent fetches an unstructured Component resource from the Kubernetes API server.
func (g *k8sComponentGetter) GetComponent(ctx context.Context, namespace, name string) (*unstructured.Unstructured, error) {
	return g.client.Resource(componentGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
}

// ResolveRedpandaComponentConfig updates cfg with broker endpoints and default topic from the
// redpanda Component CR when cfg.EnableComponents is true and the component is enabled.
func ResolveRedpandaComponentConfig(ctx context.Context, cfg *Config, getter ComponentGetter) error {
	if !shouldResolveRedpandaComponent(ctx, cfg) {
		return nil
	}
	if getter == nil {
		return fmt.Errorf("component getter cannot be nil when enable_components is true")
	}

	ns := strings.TrimSpace(cfg.ComponentsNamespace)
	if ns == "" {
		ns = defaultComponentsNamespace
	}

	logging.Info(ctx, "Resolving Redpanda configuration from Component CR", "namespace", ns, "component", redpandaComponentName)
	comp, err := getter.GetComponent(ctx, ns, redpandaComponentName)
	if err != nil {
		return fmt.Errorf("failed to get component %s/%s: %w", ns, redpandaComponentName, err)
	}
	if isRedpandaComponentDisabled(comp) {
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
	if !cfg.EnableComponents {
		logging.Info(ctx, "Skipping Redpanda Component CR resolution: enable_components is false",
			"brokers", cfg.RedpandaBrokers, "topic", cfg.TopicName)
		return false
	}
	if bus := cfg.messageBusOrDefault(); bus != MessageBusRedpanda {
		logging.Info(ctx, "Skipping Redpanda Component CR resolution: message bus is not redpanda",
			"message_bus", bus)
		return false
	}
	return true
}

func logDisabledRedpandaComponent(ctx context.Context, cfg *Config, ns string, comp *unstructured.Unstructured) {
	var specEnabled bool
	var statusState string
	if comp != nil && comp.Object != nil {
		specEnabled, _, _ = unstructured.NestedBool(comp.Object, "spec", "enabled")
		statusState, _, _ = unstructured.NestedString(comp.Object, "status", "state")
	}
	logging.Info(ctx, "Redpanda Component CR is disabled; preserving static ingestion-gateway configuration",
		"namespace", ns, "component", redpandaComponentName,
		"spec_enabled", specEnabled, "status_state", statusState,
		"fallback_brokers", cfg.RedpandaBrokers, "fallback_topic", cfg.TopicName)
}

func isRedpandaComponentDisabled(comp *unstructured.Unstructured) bool {
	if comp == nil || comp.Object == nil {
		return true
	}
	if enabled, ok, err := unstructured.NestedBool(comp.Object, "spec", "enabled"); err == nil && ok && !enabled {
		return true
	}
	if state, ok, err := unstructured.NestedString(comp.Object, "status", "state"); err == nil && ok {
		return state == "COMPONENT_STATE_DISABLED" || state == "COMPONENT_STATE_DISABLING" || state == "COMPONENT_STATE_DELETED"
	}
	return false
}

func applyRedpandaComponentOverrides(ctx context.Context, cfg *Config, ns string, comp *unstructured.Unstructured) {
	params, _, err := unstructured.NestedStringMap(comp.Object, "spec", "parameters")
	if err != nil {
		logging.Warn(ctx, "Failed to parse spec.parameters from Component CR", "error", err)
	}
	envVars, _, err := unstructured.NestedStringMap(comp.Object, "status", "access", "envVars")
	if err != nil {
		logging.Warn(ctx, "Failed to parse status.access.envVars from Component CR", "error", err)
	}
	statusState, _, err := unstructured.NestedString(comp.Object, "status", "state")
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
	port := strings.TrimSpace(envVars["PORT"])
	if port == "" {
		port = strings.TrimSpace(params["kafkaPort"])
	}
	if endpoint := strings.TrimSpace(envVars["ENDPOINT"]); endpoint != "" {
		brokers := parseBrokers(endpoint)
		for i, b := range brokers {
			brokers[i] = ensureEndpointPort(b, port)
		}
		return brokers, "status.access.envVars.ENDPOINT"
	}
	if brokerEndpoints := strings.TrimSpace(params["brokerEndpoints"]); brokerEndpoints != "" {
		return parseBrokers(brokerEndpoints), "spec.parameters.brokerEndpoints"
	}
	return nil, ""
}

func ensureEndpointPort(endpoint, port string) string {
	if port == "" {
		return endpoint
	}
	if _, _, err := net.SplitHostPort(endpoint); err == nil {
		return endpoint
	}
	return net.JoinHostPort(endpoint, port)
}
