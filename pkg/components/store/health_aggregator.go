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

package store

import (
	"encoding/base64"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
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
func AggregatePlatformHealth(components map[string]*ComponentRecord, filter string, pageSize int32, pageToken string) (*componentspb.GetPlatformHealthResponse, error) {
	predicates, err := parsePlatformHealthFilter(filter)
	if err != nil {
		return nil, err
	}

	effectivePageSize := normalizePageSize(pageSize)
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
		protoComponents[i] = ToProtoComponent(rec)
	}

	return &componentspb.GetPlatformHealthResponse{
		Components:     protoComponents,
		NextPageToken:  nextToken,
		TotalHealthy:   healthyCount,
		TotalUnhealthy: unhealthyCount,
	}, nil
}

func normalizePageSize(pageSize int32) int {
	if pageSize <= 0 {
		return defaultPlatformHealthPageSize
	}
	if pageSize > maxPlatformHealthPageSize {
		return maxPlatformHealthPageSize
	}
	return int(pageSize)
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

func slicePage(items []*ComponentRecord, offset, pageSize int) ([]*ComponentRecord, string) {
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

func filterAndCountComponents(components map[string]*ComponentRecord, predicates []filterPredicate) ([]*ComponentRecord, int32, int32) {
	var matching []*ComponentRecord
	var healthy, unhealthy int32

	for _, rec := range components {
		if isDeletedRecord(rec) || !matchesAllPredicates(rec, predicates) {
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

func isHealthyComponent(rec *ComponentRecord) bool {
	if rec == nil {
		return false
	}
	if rec.State != componentspb.ComponentState_COMPONENT_STATE_ENABLED {
		return false
	}
	return rec.Status == nil || rec.Status.Ready
}

func matchesAllPredicates(rec *ComponentRecord, predicates []filterPredicate) bool {
	for _, p := range predicates {
		if !matchesPredicate(rec, p) {
			return false
		}
	}
	return true
}

func matchesPredicate(rec *ComponentRecord, p filterPredicate) bool {
	switch p.key {
	case "state":
		return matchesStatePredicate(rec.State, p.value)
	case "type":
		return matchesTypePredicate(rec, p.value)
	case "ready":
		return matchesReadyPredicate(rec, p.value)
	case "name":
		return matchesNamePredicate(rec.Name, p.value)
	default:
		return false
	}
}

func matchesStatePredicate(state componentspb.ComponentState, expected string) bool {
	clean := strings.ToUpper(strings.TrimSpace(expected))
	clean = strings.TrimPrefix(clean, "COMPONENT_STATE_")
	stateStr := strings.TrimPrefix(state.String(), "COMPONENT_STATE_")
	if strings.EqualFold(stateStr, clean) {
		return true
	}
	if num, err := strconv.Atoi(clean); err == nil && int32(state) == int32(num) {
		return true
	}
	return false
}

func matchesTypePredicate(rec *ComponentRecord, expected string) bool {
	if rec.Spec == nil {
		return false
	}
	clean := strings.ToUpper(strings.TrimSpace(expected))
	clean = strings.TrimPrefix(clean, "COMPONENT_TYPE_")
	typeStr := strings.TrimPrefix(rec.Spec.GetType().String(), "COMPONENT_TYPE_")
	if strings.EqualFold(typeStr, clean) {
		return true
	}
	if num, err := strconv.Atoi(clean); err == nil && int32(rec.Spec.GetType()) == int32(num) {
		return true
	}
	return false
}

func matchesReadyPredicate(rec *ComponentRecord, expected string) bool {
	if rec.Status == nil {
		return false
	}
	expectedBool, err := strconv.ParseBool(strings.TrimSpace(expected))
	if err != nil {
		return false
	}
	return rec.Status.Ready == expectedBool
}

func matchesNamePredicate(actualName, expected string) bool {
	canonicalExpected := CanonicalComponentName(expected)
	return CanonicalComponentName(actualName) == canonicalExpected
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
	var key, val string
	if idx := strings.Index(clause, "="); idx != -1 {
		key = strings.TrimSpace(clause[:idx])
		val = strings.Trim(strings.TrimSpace(clause[idx+1:]), `"'`)
	} else if idx := strings.Index(clause, ":"); idx != -1 {
		key = strings.TrimSpace(clause[:idx])
		val = strings.Trim(strings.TrimSpace(clause[idx+1:]), `"'`)
	} else {
		return filterPredicate{}, status.Errorf(codes.InvalidArgument, "invalid filter predicate %q", clause)
	}

	cleanKey := strings.ToLower(key)
	if !isValidFilterKey(cleanKey) {
		return filterPredicate{}, status.Errorf(codes.InvalidArgument, "unsupported filter key %q in %q", key, clause)
	}
	return filterPredicate{key: cleanKey, value: val}, nil
}

func isValidFilterKey(k string) bool {
	return k == "state" || k == "type" || k == "ready" || k == "name"
}
