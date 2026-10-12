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
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	componentspb "github.com/google/xmc/api/protos/components/v1alpha1"
	"github.com/google/xmc/pkg/components/naming"
)

// ParseSpecFromUnstructured converts an unstructured spec map into a typed protobuf ComponentSpec,
// alongside the boolean flags deletionProtection (default true) and enabled (default true).
func ParseSpecFromUnstructured(specObj any) (*componentspb.ComponentSpec, bool, bool) {
	specMap, ok := specObj.(map[string]any)
	if !ok || specMap == nil {
		return &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT}, true, true
	}
	spec := &componentspb.ComponentSpec{
		Type:           parseComponentType(specMap),
		DeletionPolicy: parseDeletionPolicy(specMap),
		Version:        extractStr(specMap, "version"),
		Description:    extractStr(specMap, "description"),
		WrapperRef:     parseWrapperRef(specMap),
		Manifests:      parseManifests(specMap),
		Parameters:     parseParameters(specMap),
		Dependencies:   parseDependencies(specMap),
	}
	return spec, extractBool(specMap, "deletionProtection", true), extractBool(specMap, "enabled", true)
}

// parseDependencies converts the unstructured spec.dependencies list into canonical component names,
// preserving declaration order and dropping duplicate and non-string entries. Strings with an empty
// canonical name (e.g. "" or "/") are kept verbatim so CMS rejects the spec with
// InvalidArgument instead of the bridge silently dropping a dependency edge.
func parseDependencies(m map[string]any) []string {
	raw, ok := m["dependencies"].([]any)
	if !ok {
		return nil
	}
	var res []string
	seen := make(map[string]bool, len(raw))
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			continue
		}
		name := naming.CanonicalComponentName(s)
		if name == "" {
			res = append(res, s)
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		res = append(res, name)
	}
	return res
}

// parseParameters converts an unstructured parameters map into a string map.
func parseParameters(m map[string]any) map[string]string {
	raw, ok := m["parameters"].(map[string]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	res := make(map[string]string, len(raw))
	for k, v := range raw {
		switch val := v.(type) {
		case string:
			res[k] = val
		case float64:
			if val == float64(int64(val)) {
				res[k] = strconv.FormatInt(int64(val), 10)
			} else {
				res[k] = strconv.FormatFloat(val, 'f', -1, 64)
			}
		case int:
			res[k] = strconv.Itoa(val)
		case int64:
			res[k] = strconv.FormatInt(val, 10)
		case bool:
			res[k] = strconv.FormatBool(val)
		case nil:
			// skip
		default:
			res[k] = fmt.Sprintf("%v", v)
		}
	}
	if len(res) == 0 {
		return nil
	}
	return res
}

// parseManifests converts an unstructured manifests slice into a slice of YAML string documents.
func parseManifests(m map[string]any) []string {
	raw, ok := m["manifests"].([]any)
	if !ok {
		return nil
	}
	var res []string
	for _, v := range raw {
		if s, ok := v.(string); ok {
			res = append(res, s)
		}
	}
	return res
}

// extractBool extracts a boolean key from map m, returning def if missing or of invalid type.
func extractBool(m map[string]any, k string, def bool) bool {
	if v, ok := m[k].(bool); ok {
		return v
	}
	return def
}

// extractStr extracts a string key from map m, returning "" if missing or of invalid type.
func extractStr(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

// extractInt extracts an int64 integer key from map m. Handles standard Go numeric types
// (int64, int, int32, float64), json.Number, and string representations.
func extractInt(m map[string]any, k string) int64 {
	v, ok := m[k]
	if !ok {
		return 0
	}
	switch val := v.(type) {
	case int64:
		return val
	case int:
		return int64(val)
	case int32:
		return int64(val)
	case float64:
		return int64(val)
	case json.Number:
		if i, err := val.Int64(); err == nil {
			return i
		}
	case string:
		if i, err := strconv.ParseInt(val, 10, 64); err == nil {
			return i
		}
	}
	return 0
}

// parseComponentType parses the componentType string field into its corresponding protobuf enum.
func parseComponentType(m map[string]any) componentspb.ComponentType {
	if ct, ok := m["componentType"].(string); ok && ct == "COMPONENT_TYPE_CUSTOM_3P_WRAPPER" {
		return componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER
	}
	return componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT
}

// parseDeletionPolicy parses the deletionPolicy string field into its corresponding protobuf enum (Delete vs Orphan).
func parseDeletionPolicy(m map[string]any) componentspb.DeletionPolicy {
	if dp, ok := m["deletionPolicy"].(string); ok && (dp == "Orphan" || dp == "DELETION_POLICY_ORPHAN") {
		return componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN
	}
	return componentspb.DeletionPolicy_DELETION_POLICY_DELETE
}

// parseWrapperRef extracts a 3P wrapper reference from the unstructured spec map,
// supporting both camelCase and protobuf field names.
func parseWrapperRef(m map[string]any) *componentspb.WrapperRef {
	wr, ok := m["wrapperRef"].(map[string]any)
	if !ok || wr == nil {
		return nil
	}
	return &componentspb.WrapperRef{
		CrdGroup:        firstStr(wr, "crdGroup", "group"),
		CrdVersion:      firstStr(wr, "crdVersion", "version"),
		CrdKind:         firstStr(wr, "crdKind", "kind"),
		TargetName:      firstStr(wr, "targetName", "name"),
		TargetNamespace: firstStr(wr, "targetNamespace", "namespace"),
	}
}

// firstStr returns the first non-empty string value in m among the alias keys, or "" if none is set.
func firstStr(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v := extractStr(m, k); v != "" {
			return v
		}
	}
	return ""
}

// ParseConditionsFromUnstructured extracts and parses a slice of Condition objects from the .status map,
// converting lastTransitionTime RFC3339 strings into time.Time values.
func ParseConditionsFromUnstructured(statusObj any) []Condition {
	statusMap, ok := statusObj.(map[string]any)
	if !ok || statusMap == nil {
		return nil
	}
	condsList, ok := statusMap["conditions"].([]any)
	if !ok {
		return nil
	}
	var res []Condition
	for _, cObj := range condsList {
		if cMap, ok := cObj.(map[string]any); ok {
			c := Condition{
				Type: extractStr(cMap, "type"), Status: extractStr(cMap, "status"),
				Reason: extractStr(cMap, "reason"), Message: extractStr(cMap, "message"),
				ObservedGeneration: extractInt(cMap, "observedGeneration"),
			}
			if ltt, ok := cMap["lastTransitionTime"].(string); ok {
				if t, err := time.Parse(time.RFC3339, ltt); err == nil {
					c.LastTransitionTime = t
				}
			}
			res = append(res, c)
		}
	}
	return res
}

// ParseWrapperRef extracts WrapperRef from unstructured custom resource spec map.
// Target coordinates are resolved by parseWrapperRef so their key aliases are defined once.
func ParseWrapperRef(specObj any, defaultNamespace string) WrapperRef {
	specMap, ok := specObj.(map[string]any)
	if !ok || specMap == nil {
		return WrapperRef{Namespace: defaultNamespace}
	}
	target := parseWrapperRef(specMap)
	if target == nil {
		return WrapperRef{}
	}
	// parseWrapperRef returned a target, so spec.wrapperRef is a non-nil map.
	wrapperRaw, _ := specMap["wrapperRef"].(map[string]any)
	ref := ToBridgeWrapperRef(target, defaultNamespace)
	ref.ObservedGenerationPath = firstStr(wrapperRaw, "observedGenerationPath", "observed_generation_path")
	ref.ReadyConditionPath = firstStr(wrapperRaw, "readyConditionPath", "ready_condition_path", "readyPath")
	ref.ExpectedReadyValue = firstStr(wrapperRaw, "expectedReadyValue", "expected_ready_value")
	ref.EndpointJSONPath = firstStr(wrapperRaw, "endpointJsonPath", "endpoint_json_path", "endpointJsonpath")
	ref.ConnectionSecretJSONPath = firstStr(wrapperRaw, "connectionSecretJsonPath", "connection_secret_json_path", "secretNameJsonpath")
	return ref
}
