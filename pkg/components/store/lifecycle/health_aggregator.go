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

package lifecycle

import (
	"encoding/base64"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/naming"
	"xmanager_cloud/pkg/components/store"
)

const (
	defaultPlatformHealthPageSize = 50
	maxPlatformHealthPageSize     = 1000
	offsetTokenPrefix             = "offset:"
)

type filterPredicate struct {
	key   string
	value string
}

// AggregatePlatformHealth filters components, computes health metrics, and paginates results per AIP-158.
func AggregatePlatformHealth(components map[string]*store.ComponentRecord, filter string, pageSize int32, pageToken string) (*componentspb.GetPlatformHealthResponse, error) {
	effectivePageSize, err := NormalizePageSize(pageSize)
	if err != nil {
		return nil, err
	}

	predicates, err := parsePlatformHealthFilter(filter)
	if err != nil {
		return nil, err
	}

	offset, err := parsePageToken(pageToken)
	if err != nil {
		return nil, err
	}

	matching, healthyCount, unhealthyCount := filterAndCountComponents(components, predicates)
	sort.Slice(matching, func(i, j int) bool {
		return matching[i].Name < matching[j].Name
	})

	pageRecords, nextToken := slicePage(matching, offset, effectivePageSize)
	protoComponents := make([]*componentspb.Component, len(pageRecords))
	for i, rec := range pageRecords {
		protoComponents[i] = store.ToProtoComponent(rec)
	}

	return &componentspb.GetPlatformHealthResponse{
		Components:     protoComponents,
		NextPageToken:  nextToken,
		TotalHealthy:   healthyCount,
		TotalUnhealthy: unhealthyCount,
	}, nil
}

// NormalizePageSize validates and normalizes the requested page size per AIP-158.
// It returns an InvalidArgument error if pageSize is negative, defaults to 50 when 0,
// and clamps values exceeding 1000 to the maximum of 1000.
func NormalizePageSize(pageSize int32) (int, error) {
	if pageSize < 0 {
		return 0, status.Errorf(codes.InvalidArgument, "page size cannot be negative: %d", pageSize)
	}
	if pageSize == 0 {
		return defaultPlatformHealthPageSize, nil
	}
	if pageSize > maxPlatformHealthPageSize {
		return maxPlatformHealthPageSize, nil
	}
	return int(pageSize), nil
}

func parsePageToken(token string) (int, error) {
	if token == "" {
		return 0, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return 0, status.Errorf(codes.InvalidArgument, "invalid page token %q: %v", token, err)
	}
	str := string(decoded)
	if !strings.HasPrefix(str, offsetTokenPrefix) {
		return 0, status.Errorf(codes.InvalidArgument, "invalid page token format %q", token)
	}
	offsetStr := strings.TrimPrefix(str, offsetTokenPrefix)
	offset, err := strconv.Atoi(offsetStr)
	if err != nil || offset < 0 {
		return 0, status.Errorf(codes.InvalidArgument, "invalid page token offset in %q", token)
	}
	return offset, nil
}

func slicePage(items []*store.ComponentRecord, offset, pageSize int) ([]*store.ComponentRecord, string) {
	if offset >= len(items) {
		return nil, ""
	}
	end := offset + pageSize
	if end >= len(items) {
		return items[offset:], ""
	}
	nextToken := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s%d", offsetTokenPrefix, end)))
	return items[offset:end], nextToken
}

func filterAndCountComponents(components map[string]*store.ComponentRecord, predicates []filterPredicate) ([]*store.ComponentRecord, int32, int32) {
	var matching []*store.ComponentRecord
	var healthy, unhealthy int32

	for _, rec := range components {
		if store.IsDeletedRecord(rec) || !matchesAllPredicates(rec, predicates) {
			continue
		}
		matching = append(matching, rec)
		if isHealthyComponent(rec) {
			healthy++
			continue
		}
		unhealthy++
	}
	return matching, healthy, unhealthy
}

func isHealthyComponent(rec *store.ComponentRecord) bool {
	if rec == nil {
		return false
	}
	if rec.State != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
		return false
	}
	return rec.Status == nil || rec.Status.Ready
}

func matchesAllPredicates(rec *store.ComponentRecord, predicates []filterPredicate) bool {
	for _, p := range predicates {
		if !matchesPredicate(rec, p) {
			return false
		}
	}
	return true
}

func matchesPredicate(rec *store.ComponentRecord, p filterPredicate) bool {
	switch p.key {
	case "state":
		return matchesStatePredicate(rec.State, p.value)
	case "type":
		return matchesTypePredicate(rec, p.value)
	case "ready":
		return matchesReadyPredicate(rec, p.value)
	case "name":
		return matchesNamePredicate(rec.Name, p.value)
	case "stability":
		return matchesStabilityPredicate(rec, p.value)
	default:
		return false
	}
}

func matchesStatePredicate(state componentspb.ComponentState, expected string) bool {
	return matchesProtoEnum(state.String(), int32(state), "COMPONENT_STATE_", expected)
}

func matchesTypePredicate(rec *store.ComponentRecord, expected string) bool {
	if rec.Spec == nil {
		return false
	}
	return matchesProtoEnum(rec.Spec.GetType().String(), int32(rec.Spec.GetType()), "COMPONENT_TYPE_", expected)
}

func matchesReadyPredicate(rec *store.ComponentRecord, expected string) bool {
	expectedBool, err := strconv.ParseBool(strings.TrimSpace(expected))
	if err != nil {
		return false
	}
	ready := rec.Status == nil || rec.Status.Ready
	return ready == expectedBool
}

func matchesNamePredicate(actualName, expected string) bool {
	canonicalExpected := naming.CanonicalComponentName(expected)
	return naming.CanonicalComponentName(actualName) == canonicalExpected
}

func matchesStabilityPredicate(rec *store.ComponentRecord, expected string) bool {
	stability := effectiveStability(rec)
	return matchesProtoEnum(stability.String(), int32(stability), "COMPONENT_STABILITY_", expected)
}

func matchesProtoEnum(enumStr string, enumNum int32, prefix, expected string) bool {
	clean := strings.ToUpper(strings.TrimSpace(expected))
	clean = strings.TrimPrefix(clean, prefix)
	shortName := strings.TrimPrefix(enumStr, prefix)
	if strings.EqualFold(shortName, clean) {
		return true
	}
	if num, err := strconv.Atoi(clean); err == nil && enumNum == int32(num) {
		return true
	}
	return false
}

func effectiveStability(rec *store.ComponentRecord) componentspb.ComponentStability {
	if rec == nil || rec.Spec == nil || rec.Spec.GetStability() == componentspb.ComponentStability_COMPONENT_STABILITY_UNSPECIFIED {
		return componentspb.ComponentStability_COMPONENT_STABILITY_STABLE
	}
	return rec.Spec.GetStability()
}

func parsePlatformHealthFilter(filter string) ([]filterPredicate, error) {
	trimmed := strings.TrimSpace(filter)
	if trimmed == "" {
		return nil, nil
	}

	parts := splitFilterPredicates(trimmed)
	var predicates []filterPredicate
	for _, part := range parts {
		p, err := parseSinglePredicate(part)
		if err != nil {
			return nil, err
		}
		predicates = append(predicates, p)
	}
	return predicates, nil
}

func splitFilterPredicates(filter string) []string {
	var parts []string
	andSplit := strings.Split(filter, " AND ")
	for _, seg := range andSplit {
		commaSplit := strings.Split(seg, ",")
		for _, raw := range commaSplit {
			if s := strings.TrimSpace(raw); s != "" {
				parts = append(parts, s)
			}
		}
	}
	return parts
}

func parseSinglePredicate(clause string) (filterPredicate, error) {
	idx := strings.IndexAny(clause, "=:")
	if idx == -1 {
		return filterPredicate{}, status.Errorf(codes.InvalidArgument, "invalid filter predicate %q", clause)
	}
	key := strings.TrimSpace(clause[:idx])
	val := strings.Trim(strings.TrimSpace(clause[idx+1:]), `"'`)

	cleanKey := strings.ToLower(key)
	if !isValidFilterKey(cleanKey) {
		return filterPredicate{}, status.Errorf(codes.InvalidArgument, "unsupported filter key %q in %q", key, clause)
	}
	return filterPredicate{key: cleanKey, value: val}, nil
}

func isValidFilterKey(k string) bool {
	return k == "state" || k == "type" || k == "ready" || k == "name" || k == "stability"
}
