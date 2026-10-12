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

// Package summary provides the CustomComponentsInfoProvider library for the XMC Admin Panel.
package summary

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"

	componentspb "github.com/google/xmc/api/protos/components/v1alpha1"
	"github.com/google/xmc/pkg/components/store"
)

// CustomComponentsInfoProvider defines the interface for component health check and configuration retrieval.
type CustomComponentsInfoProvider interface {
	// GetComponentsHealth returns health status for all custom components.
	GetComponentsHealth(ctx context.Context) ([]ComponentHealth, error)

	// GetComponentsConfig returns configuration parameters for all custom components.
	GetComponentsConfig(ctx context.Context) ([]ComponentConfig, error)
}

// ComponentType distinguishes between 1P native and 3P custom components.
type ComponentType int

const (
	ComponentTypeUnspecified ComponentType = iota
	ComponentTypeCustom1PComponent
	ComponentTypeCustom3PWrapper
)

// String returns human-readable name of the ComponentType.
func (t ComponentType) String() string {
	switch t {
	case ComponentTypeCustom1PComponent:
		return "CUSTOM_1P_COMPONENT"
	case ComponentTypeCustom3PWrapper:
		return "CUSTOM_3P_WRAPPER"
	default:
		return "UNSPECIFIED"
	}
}

// HealthState represents the aggregated operational status of a component.
type HealthState int

const (
	HealthStateUnspecified HealthState = iota
	HealthStateHealthy
	HealthStateUnhealthy
	HealthStateDisabled
)

// String returns human-readable name of the HealthState.
func (s HealthState) String() string {
	switch s {
	case HealthStateHealthy:
		return "HEALTHY"
	case HealthStateUnhealthy:
		return "UNHEALTHY"
	case HealthStateDisabled:
		return "DISABLED"
	default:
		return "UNSPECIFIED"
	}
}

// ComponentHealth represents the health status of a specific component.
type ComponentHealth struct {
	Name          string
	Type          ComponentType
	HealthState   HealthState
	StatusMessage string
}

// ConfigParameter exposes a standardized configuration property.
type ConfigParameter struct {
	Key   string
	Value string
}

// ComponentConfig represents configuration parameters of a specific component.
type ComponentConfig struct {
	Name             string
	Type             ComponentType
	ConfigParameters []ConfigParameter
}

// Ensure StoreComponentsProvider implements CustomComponentsInfoProvider at compile time.
var _ CustomComponentsInfoProvider = (*StoreComponentsProvider)(nil)

// StoreComponentsProvider implements CustomComponentsInfoProvider backed by store.Store.
type StoreComponentsProvider struct {
	store store.Store
}

// NewStoreComponentsProvider creates a new StoreComponentsProvider instance.
func NewStoreComponentsProvider(s store.Store) (*StoreComponentsProvider, error) {
	if s == nil {
		return nil, fmt.Errorf("store must not be nil")
	}
	return &StoreComponentsProvider{store: s}, nil
}

// GetComponentsHealth retrieves health summaries for all components.
func (p *StoreComponentsProvider) GetComponentsHealth(ctx context.Context) ([]ComponentHealth, error) {
	comps, err := p.store.ListComponents(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list components for health: %w", err)
	}

	results := make([]ComponentHealth, 0, len(comps))
	for _, c := range comps {
		if c == nil {
			continue
		}
		results = append(results, evaluateComponentHealth(c))
	}
	return results, nil
}

func evaluateComponentHealth(c *store.ComponentRecord) ComponentHealth {
	cType := mapComponentType(c.Spec)
	hState := HealthStateUnhealthy
	msg := fmt.Sprintf("State: %s", c.State.String())

	switch c.State {
	case componentspb.ComponentState_COMPONENT_STATE_ENABLED:
		if c.Status != nil && c.Status.GetReady() {
			hState = HealthStateHealthy
			msg = "Healthy and Operational"
		} else {
			hState = HealthStateUnhealthy
			if c.Status != nil && c.Status.GetHealthMessage() != "" {
				msg = c.Status.GetHealthMessage()
			} else {
				msg = "Component enabled but not ready"
			}
		}
	case componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION:
		hState = HealthStateUnhealthy
		msg = "Pending GKE reconciliation"
	case componentspb.ComponentState_COMPONENT_STATE_DISABLED:
		hState = HealthStateDisabled
		msg = "Component disabled"
	case componentspb.ComponentState_COMPONENT_STATE_DISABLING:
		hState = HealthStateUnhealthy
		msg = "Component draining"
	}

	return ComponentHealth{
		Name:          c.Name,
		Type:          cType,
		HealthState:   hState,
		StatusMessage: msg,
	}
}

func mapComponentType(spec *componentspb.ComponentSpec) ComponentType {
	if spec == nil {
		return ComponentTypeUnspecified
	}
	switch spec.GetType() {
	case componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT:
		return ComponentTypeCustom1PComponent
	case componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER:
		return ComponentTypeCustom3PWrapper
	default:
		return ComponentTypeUnspecified
	}
}

// GetComponentsConfig retrieves configuration parameters for all components.
func (p *StoreComponentsProvider) GetComponentsConfig(ctx context.Context) ([]ComponentConfig, error) {
	comps, err := p.store.ListComponents(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list components for config: %w", err)
	}

	results := make([]ComponentConfig, 0, len(comps))
	for _, c := range comps {
		if c == nil {
			continue
		}
		results = append(results, extractComponentConfig(c))
	}
	return results, nil
}

func extractComponentConfig(c *store.ComponentRecord) ComponentConfig {
	cType := mapComponentType(c.Spec)
	paramsMap := make(map[string]string)

	if c.Spec != nil && c.Spec.GetVersion() != "" {
		paramsMap["version"] = c.Spec.GetVersion()
	}

	populateStatusParameters(paramsMap, c.Status)

	if c.Spec != nil {
		populateCustomParameters(paramsMap, c.Spec.GetParameters())
	}

	keys := make([]string, 0, len(paramsMap))
	for k := range paramsMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	params := make([]ConfigParameter, len(keys))
	for i, k := range keys {
		params[i] = ConfigParameter{
			Key:   k,
			Value: paramsMap[k],
		}
	}

	return ComponentConfig{
		Name:             c.Name,
		Type:             cType,
		ConfigParameters: params,
	}
}

func populateCustomParameters(paramsMap map[string]string, customParams map[string]string) {
	for k, v := range customParams {
		if _, exists := paramsMap[k]; exists {
			paramsMap[fmt.Sprintf("param.%s", k)] = v
		} else {
			paramsMap[k] = v
		}
	}
}

func populateStatusParameters(paramsMap map[string]string, status *componentspb.ComponentAccessStatus) {
	if status == nil || status.GetCoordinates() == nil {
		return
	}
	endpoint := formatCoordinatesEndpoint(status.GetCoordinates())
	if endpoint != "" {
		paramsMap["endpoint"] = endpoint
	}
}

func formatCoordinatesEndpoint(coord *componentspb.ResolvedComponentCoordinates) string {
	if coord == nil {
		return ""
	}
	scheme := coord.GetScheme()
	if scheme == "" {
		scheme = "http"
	}
	host := coord.GetHost()
	if host == "" {
		host = coord.GetServiceName()
	}
	if host == "" {
		return ""
	}
	if coord.GetPort() > 0 {
		return fmt.Sprintf("%s://%s", scheme, net.JoinHostPort(host, strconv.Itoa(int(coord.GetPort()))))
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("%s://%s", scheme, host)
}
