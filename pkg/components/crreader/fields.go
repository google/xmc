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

package crreader

import (
	"encoding/json"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	// StateDisabled indicates a Component CR is disabled.
	StateDisabled = "COMPONENT_STATE_DISABLED"
	// StateDisabling indicates a Component CR is currently disabling.
	StateDisabling = "COMPONENT_STATE_DISABLING"
	// StateDeleted indicates a Component CR has been deleted.
	StateDeleted = "COMPONENT_STATE_DELETED"
)

// IsDisabled reports whether a Component CR is nil, has spec.enabled=false,
// or is in a disabled, disabling, or deleted status state.
func IsDisabled(comp *unstructured.Unstructured) bool {
	if comp == nil || comp.Object == nil {
		return true
	}
	if enabled, ok, err := SpecEnabled(comp); err == nil && ok && !enabled {
		return true
	}
	if state, err := StatusState(comp); err == nil {
		return state == StateDisabled || state == StateDisabling || state == StateDeleted
	}
	return false
}

// SpecEnabled extracts spec.enabled from a Component CR.
func SpecEnabled(comp *unstructured.Unstructured) (bool, bool, error) {
	if comp == nil || comp.Object == nil {
		return false, false, nil
	}
	return unstructured.NestedBool(comp.Object, "spec", "enabled")
}

// StatusState extracts status.state from a Component CR.
func StatusState(comp *unstructured.Unstructured) (string, error) {
	if comp == nil || comp.Object == nil {
		return "", nil
	}
	state, _, err := unstructured.NestedString(comp.Object, "status", "state")
	return state, err
}

// Parameters extracts spec.parameters as a string map from a Component CR.
func Parameters(comp *unstructured.Unstructured) (map[string]string, error) {
	return NestedStringMap(comp, "spec", "parameters")
}

// AccessEnvVars extracts status.access.envVars as a string map from a Component CR.
func AccessEnvVars(comp *unstructured.Unstructured) (map[string]string, error) {
	return NestedStringMap(comp, "status", "access", "envVars")
}

// NestedStringMap extracts a nested map at fields from a Component CR and coerces scalar values to strings.
func NestedStringMap(comp *unstructured.Unstructured, fields ...string) (map[string]string, error) {
	if comp == nil || comp.Object == nil {
		return nil, nil
	}
	raw, ok, err := unstructured.NestedFieldNoCopy(comp.Object, fields...)
	if err != nil || !ok || raw == nil {
		return nil, err
	}
	if _, isAnyMap := raw.(map[string]any); !isAnyMap {
		if _, isStrMap := raw.(map[string]string); !isStrMap {
			return nil, fmt.Errorf("field %s is not a map (got %T)", strings.Join(fields, "."), raw)
		}
	}
	return CoerceStringMap(raw), nil
}

// CoerceStringMap converts an unstructured map[string]any or map[string]string into map[string]string,
// converting scalar types to strings and omitting nil values.
func CoerceStringMap(raw any) map[string]string {
	if strMap, ok := raw.(map[string]string); ok {
		return cloneStringMap(strMap)
	}
	m, ok := raw.(map[string]any)
	if !ok || len(m) == 0 {
		return nil
	}
	res := make(map[string]string, len(m))
	for k, v := range m {
		if v == nil {
			continue
		}
		res[k] = formatScalarValue(v)
	}
	return res
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func formatScalarValue(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case json.Number:
		return val.String()
	default:
		return fmt.Sprintf("%v", val)
	}
}
