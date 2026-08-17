// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
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
	"sort"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/store"
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

// HealthState represents the aggregated operational status of a component.
type HealthState int

const (
	HealthStateUnspecified HealthState = iota
	HealthStateHealthy
	HealthStateUnhealthy
)

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

// StoreComponentsProvider implements CustomComponentsInfoProvider backed by Store.
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

	var results []ComponentHealth
	for _, c := range comps {
		cType := ComponentTypeUnspecified
		if c.ConfigProto != nil {
			if c.ConfigProto.GetComponentType() == componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT {
				cType = ComponentTypeCustom1PComponent
			} else if c.ConfigProto.GetComponentType() == componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER {
				cType = ComponentTypeCustom3PWrapper
			}
		}

		hState := HealthStateUnhealthy
		msg := fmt.Sprintf("State: %s", c.State.String())
		if c.State == componentspb.ComponentState_COMPONENT_STATE_ENABLED {
			hState = HealthStateHealthy
			msg = "Healthy and Operational"
		} else if c.State == componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION {
			msg = "Pending GKE reconciliation"
		}

		results = append(results, ComponentHealth{
			Name:          c.Name,
			Type:          cType,
			HealthState:   hState,
			StatusMessage: msg,
		})
	}
	return results, nil
}

// GetComponentsConfig retrieves configuration parameters for all components.
func (p *StoreComponentsProvider) GetComponentsConfig(ctx context.Context) ([]ComponentConfig, error) {
	comps, err := p.store.ListComponents(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list components for config: %w", err)
	}

	var results []ComponentConfig
	for _, c := range comps {
		cType := ComponentTypeUnspecified
		var params []ConfigParameter
		if c.ConfigProto != nil {
			if c.ConfigProto.GetComponentType() == componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT {
				cType = ComponentTypeCustom1PComponent
			} else if c.ConfigProto.GetComponentType() == componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER {
				cType = ComponentTypeCustom3PWrapper
			}

			// Add sorted parameters for deterministic output.
			var keys []string
			for k := range c.ConfigProto.GetParameters() {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				params = append(params, ConfigParameter{
					Key:   k,
					Value: c.ConfigProto.GetParameters()[k],
				})
			}
		}

		results = append(results, ComponentConfig{
			Name:             c.Name,
			Type:             cType,
			ConfigParameters: params,
		})
	}
	return results, nil
}
