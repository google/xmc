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

// Package bridge implements dynamic 3P status detection and JSONPath evaluation
// for XMC components.
package bridge

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/util/jsonpath"
)

// K8sStatusDetector implements StatusDetector using client-go dynamic client and RESTMapper.
type K8sStatusDetector struct {
	dynClient  dynamic.Interface
	restMapper meta.RESTMapper
}

var _ StatusDetector = (*K8sStatusDetector)(nil)

// NewK8sStatusDetector creates a new K8sStatusDetector instance.
func NewK8sStatusDetector(dynClient dynamic.Interface, restMapper meta.RESTMapper) *K8sStatusDetector {
	return &K8sStatusDetector{
		dynClient:  dynClient,
		restMapper: restMapper,
	}
}

var (
	defaultEndpointCandidates = []string{
		"{.status.endpoint}",
		"{.status.endpoints[0]}",
		"{.status.url}",
		"{.status.host}",
		"{.status.address.url}",
		"{.status.head.serviceName}",
	}

	defaultSecretCandidates = []string{
		"{.status.connectionSecret}",
		"{.status.secretName}",
		"{.status.auth.secretName}",
		"{.status.secret.name}",
		"{.status.connectionSecretRef.name}",
	}
)

func handleK8sError(err error, ref WrapperRef) (*ResourceStatus, error) {
	if meta.IsNoMatchError(err) {
		return &ResourceStatus{Found: false, Ready: false, Message: fmt.Sprintf("CRD not installed: %v", err)}, nil
	}
	if apierrors.IsNotFound(err) {
		return &ResourceStatus{Found: false, Ready: false, Message: fmt.Sprintf("resource %s/%s not found", ref.Namespace, ref.Name)}, nil
	}
	return nil, err
}

// DetectStatus queries the target 3P resource via the dynamic client and evaluates its status.
// It gracefully handles uninstalled CRDs (meta.IsNoMatchError) and missing resources (apierrors.IsNotFound).
func (d *K8sStatusDetector) DetectStatus(ctx context.Context, ref WrapperRef) (*ResourceStatus, error) {
	if d.dynClient == nil {
		return nil, errors.New("dynamic client is uninitialized")
	}
	if ref.Name == "" || ref.Kind == "" {
		return nil, errors.New("wrapperRef name and kind are required")
	}
	if strings.EqualFold(ref.Kind, "Secret") {
		return nil, errors.New("access to Secret resources via status detector is forbidden")
	}
	gvr, isNamespaced, err := ResolveGVR(ref, d.restMapper)
	if err != nil {
		if status, handleErr := handleK8sError(err, ref); handleErr == nil {
			return status, nil
		}
		return nil, fmt.Errorf("failed to resolve GVR: %w", err)
	}
	nri := d.dynClient.Resource(gvr)
	var ri dynamic.ResourceInterface = nri
	if isNamespaced && ref.Namespace != "" {
		ri = nri.Namespace(ref.Namespace)
	}
	obj, err := ri.Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		if status, handleErr := handleK8sError(err, ref); handleErr == nil {
			return status, nil
		}
		return nil, fmt.Errorf("failed to fetch dynamic resource: %w", err)
	}
	return ExtractResourceStatus(obj, ref)
}

// ResolveGVR resolves the GroupVersionResource for a WrapperRef using RESTMapper or fallback pluralization.
func ResolveGVR(ref WrapperRef, restMapper meta.RESTMapper) (schema.GroupVersionResource, bool, error) {
	gvk := schema.GroupVersionKind{Group: ref.Group, Version: ref.Version, Kind: ref.Kind}
	if restMapper != nil {
		var versions []string
		if gvk.Version != "" {
			versions = []string{gvk.Version}
		}
		mapping, err := restMapper.RESTMapping(gvk.GroupKind(), versions...)
		if err != nil {
			return schema.GroupVersionResource{}, false, err
		}
		isNamespaced := mapping.Scope.Name() == meta.RESTScopeNameNamespace
		return mapping.Resource, isNamespaced, nil
	}
	gvr, _ := meta.UnsafeGuessKindToResource(gvk)
	return gvr, true, nil
}

// NormalizeJSONPath converts various JSONPath formats (bare expressions, dot prefixes, braces) into standard client-go syntax: {.path}.
func NormalizeJSONPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
		trimmed = strings.TrimSpace(trimmed[1 : len(trimmed)-1])
	}
	trimmed = strings.TrimPrefix(trimmed, "$")
	if !strings.HasPrefix(trimmed, ".") && !strings.HasPrefix(trimmed, "[") {
		trimmed = "." + trimmed
	}
	return fmt.Sprintf("{%s}", trimmed)
}

// ErrInvalidWrapperSpec marks an error caused by an invalid JSONPath expression configured in the Component wrapperRef spec.
var ErrInvalidWrapperSpec = errors.New("invalid wrapper spec")

// ErrContractBreach marks an error caused by a malformed 3P status payload or JSONPath execution failure against the 3P object.
var ErrContractBreach = errors.New("3p status contract breach")

// ValidateWrapperRefJSONPaths validates the syntax of all non-empty JSONPath queries configured on ref.
func ValidateWrapperRefJSONPaths(ref WrapperRef) error {
	for _, p := range []string{ref.ObservedGenerationPath, ref.ReadyConditionPath, ref.EndpointJSONPath, ref.ConnectionSecretJSONPath} {
		if strings.TrimSpace(p) == "" {
			continue
		}
		if err := jsonpath.New("validator").AllowMissingKeys(true).Parse(NormalizeJSONPath(p)); err != nil {
			return fmt.Errorf("%w: invalid jsonpath expression %q: %w", ErrInvalidWrapperSpec, p, err)
		}
	}
	return nil
}

// EvaluateJSONPath parses and executes a JSONPath expression against the provided data object.
func EvaluateJSONPath(data any, path string) (string, error) {
	if data == nil || strings.TrimSpace(path) == "" {
		return "", nil
	}
	if u, ok := data.(*unstructured.Unstructured); ok {
		if u == nil {
			return "", nil
		}
		data = u.UnstructuredContent()
	} else if u, ok := data.(unstructured.Unstructured); ok {
		data = u.UnstructuredContent()
	}
	normalized := NormalizeJSONPath(path)
	jp := jsonpath.New("detector").AllowMissingKeys(true)
	if err := jp.Parse(normalized); err != nil {
		return "", fmt.Errorf("%w: invalid jsonpath expression %q: %w", ErrInvalidWrapperSpec, path, err)
	}
	var buf bytes.Buffer
	if err := jp.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute jsonpath %q: %w", path, err)
	}
	return strings.TrimSpace(buf.String()), nil
}

func contractBreachErrorf(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrContractBreach}, args...)...)
}

func wrapStatusExtractionError(contextMsg string, err error) error {
	if errors.Is(err, ErrInvalidWrapperSpec) {
		return fmt.Errorf("%s: %w", contextMsg, err)
	}
	return contractBreachErrorf("%s: %w", contextMsg, err)
}

// ExtractResourceStatus inspects the unstructured Kubernetes object according to WrapperRef queries,
// extracting observedGeneration, readiness status, endpoints, and connection secrets.
func ExtractResourceStatus(obj *unstructured.Unstructured, ref WrapperRef) (*ResourceStatus, error) {
	if obj == nil {
		return &ResourceStatus{Found: false, Ready: false, Message: "object is nil"}, nil
	}
	rawStatus, err := validateStatusPayload(obj.Object)
	if err != nil {
		return nil, err
	}
	obsGen, err := extractObservedGeneration(obj, ref.ObservedGenerationPath)
	if err != nil {
		return nil, wrapStatusExtractionError("failed to extract observedGeneration", err)
	}
	ready, readyMsg, err := evaluateReadiness(obj, ref.ReadyConditionPath, ref.ExpectedReadyValue)
	if err != nil {
		return nil, wrapStatusExtractionError("failed to evaluate readiness", err)
	}
	if obsGen > 0 && obsGen < obj.GetGeneration() {
		ready = false
		readyMsg = fmt.Sprintf("Observed generation %d is stale compared to metadata generation %d", obsGen, obj.GetGeneration())
	}
	endpoint, err := extractFirstMatch(obj, ref.EndpointJSONPath, defaultEndpointCandidates...)
	if err != nil {
		return nil, wrapStatusExtractionError("failed to extract endpoint", err)
	}
	secret, err := extractFirstMatch(obj, ref.ConnectionSecretJSONPath, defaultSecretCandidates...)
	if err != nil {
		return nil, wrapStatusExtractionError("failed to extract connection secret", err)
	}
	return &ResourceStatus{
		Found:              true,
		Ready:              ready,
		ObservedGeneration: obsGen,
		Endpoint:           endpoint,
		ConnectionSecret:   secret,
		Message:            readyMsg,
		RawStatus:          rawStatus,
	}, nil
}

// validateStatusPayload ensures .status (when present) is a map and .status.conditions (when present) is a slice of maps with non-empty string types.
func validateStatusPayload(root map[string]any) (map[string]any, error) {
	raw, exists := root["status"]
	if !exists || raw == nil {
		return nil, nil
	}
	statusMap, ok := raw.(map[string]any)
	if !ok {
		return nil, contractBreachErrorf("malformed 3P status payload: expected object for .status, got %T", raw)
	}
	condSlice, found, err := unstructured.NestedSlice(root, "status", "conditions")
	if err != nil {
		return nil, contractBreachErrorf("malformed 3P status.conditions: expected array, got %T", statusMap["conditions"])
	}
	if !found {
		return statusMap, nil
	}
	for i, elem := range condSlice {
		condMap, ok := elem.(map[string]any)
		if !ok {
			return nil, contractBreachErrorf("malformed 3P status.conditions[%d]: expected object, got %T", i, elem)
		}
		if typ, ok := condMap["type"].(string); !ok || strings.TrimSpace(typ) == "" {
			return nil, contractBreachErrorf("malformed 3P status.conditions[%d]: condition type must be a non-empty string", i)
		}
	}
	return statusMap, nil
}

// toStringValue converts any unstructured scalar value to its canonical string representation.
func toStringValue(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case bool:
		if val {
			return "True"
		}
		return "False"
	case fmt.Stringer:
		return val.String()
	default:
		if val != nil {
			return fmt.Sprintf("%v", val)
		}
		return ""
	}
}

// parseGenerationValue converts a string representation of generation into an int64.
func parseGenerationValue(val string) (int64, error) {
	if val == "" {
		return 0, nil
	}
	if i, err := strconv.ParseInt(val, 10, 64); err == nil {
		return i, nil
	}
	if f, err := strconv.ParseFloat(val, 64); err == nil {
		return int64(f), nil
	}
	return 0, fmt.Errorf("invalid generation value %q", val)
}

// extractObservedGeneration extracts the observed generation from status.observedGeneration or via explicit JSONPath.
func extractObservedGeneration(obj *unstructured.Unstructured, path string) (int64, error) {
	if path != "" {
		val, err := EvaluateJSONPath(obj.Object, path)
		if err != nil {
			return 0, err
		}
		if val != "" {
			return parseGenerationValue(val)
		}
	}
	if obsVal, found, _ := unstructured.NestedFieldNoCopy(obj.Object, "status", "observedGeneration"); found {
		switch v := obsVal.(type) {
		case int64:
			return v, nil
		case int32:
			return int64(v), nil
		case int:
			return int64(v), nil
		case float64:
			return int64(v), nil
		default:
			return parseGenerationValue(toStringValue(v))
		}
	}
	return obj.GetGeneration(), nil
}

// getConditionStatusAndMessage extracts the status and message strings for a given condition type from .status.conditions.
func getConditionStatusAndMessage(obj *unstructured.Unstructured, condType string) (string, string, bool) {
	if obj == nil {
		return "", "", false
	}
	conditions, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if err != nil || !found {
		return "", "", false
	}
	for _, c := range conditions {
		condMap, ok := c.(map[string]any)
		if !ok {
			continue
		}
		t, _ := condMap["type"].(string)
		if strings.EqualFold(t, condType) {
			statusStr := toStringValue(condMap["status"])
			message, _ := condMap["message"].(string)
			return statusStr, message, true
		}
	}
	return "", "", false
}

// evaluateDefaultReadiness evaluates readiness when no explicit JSONPath query is specified,
// checking status.conditions[type=Ready] or status.phase (Running/Ready/Active).
func evaluateDefaultReadiness(obj *unstructured.Unstructured, expected string) (bool, string, error) {
	status, message, found := getConditionStatusAndMessage(obj, "Ready")
	if found {
		if strings.EqualFold(strings.TrimSpace(status), strings.TrimSpace(expected)) {
			return true, "Readiness condition satisfied", nil
		}
		if message == "" {
			message = fmt.Sprintf("Expected ready value %q, got %q", expected, status)
		}
		return false, message, nil
	}
	if phase, found, _ := unstructured.NestedString(obj.Object, "status", "phase"); found &&
		(strings.EqualFold(phase, "Running") || strings.EqualFold(phase, "Ready") || strings.EqualFold(phase, "Active")) {
		return true, fmt.Sprintf("Phase is %s", phase), nil
	}
	return false, "Readiness status not reported", nil
}

// evaluateReadiness evaluates whether the target resource is Ready according to query path and expected value.
func evaluateReadiness(obj *unstructured.Unstructured, path, expectedValue string) (bool, string, error) {
	expected := expectedValue
	if expected == "" {
		expected = defaultReadyValue
	}
	if path == "" {
		return evaluateDefaultReadiness(obj, expected)
	}

	val, err := EvaluateJSONPath(obj.Object, path)
	if err != nil {
		return false, "", err
	}
	if val == "" {
		return false, "Readiness status not reported", nil
	}
	if strings.EqualFold(strings.TrimSpace(val), strings.TrimSpace(expected)) {
		return true, "Readiness condition satisfied", nil
	}
	return false, extractConditionFailureMessage(obj, path, expected, val), nil
}

// extractConditionFailureMessage attempts to retrieve sibling .message or Ready condition messages upon readiness failure.
func extractConditionFailureMessage(obj *unstructured.Unstructured, path, expected, val string) string {
	if msgPath := deriveSiblingMessagePath(path); msgPath != "" {
		if msg, err := EvaluateJSONPath(obj.Object, msgPath); err == nil && msg != "" {
			return msg
		}
	}
	if _, message, found := getConditionStatusAndMessage(obj, "Ready"); found && message != "" {
		return message
	}
	return fmt.Sprintf("Expected ready value %q, got %q", expected, val)
}

// deriveSiblingMessagePath replaces a trailing .status with .message for sibling condition message discovery.
func deriveSiblingMessagePath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	hasBrace := strings.HasSuffix(trimmed, "}")
	if hasBrace {
		trimmed = strings.TrimSpace(strings.TrimSuffix(trimmed, "}"))
	}
	const statusSuffix = ".status"
	if !strings.HasSuffix(trimmed, statusSuffix) {
		return ""
	}
	msgPath := trimmed[:len(trimmed)-len(statusSuffix)] + ".message"
	if hasBrace {
		msgPath += "}"
	}
	return msgPath
}

// extractFirstMatch evaluates explicitPath if provided, or iterates through candidate JSONPath queries until a match is found.
func extractFirstMatch(obj *unstructured.Unstructured, explicitPath string, candidates ...string) (string, error) {
	if explicitPath != "" {
		val, err := EvaluateJSONPath(obj.Object, explicitPath)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(val), nil
	}
	for _, c := range candidates {
		if val, err := EvaluateJSONPath(obj.Object, c); err == nil && val != "" {
			return strings.TrimSpace(val), nil
		}
	}
	return "", nil
}
