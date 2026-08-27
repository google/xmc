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
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

func createTestComponentsMap() map[string]*ComponentRecord {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	delTime := now.Add(1 * time.Hour)

	return map[string]*ComponentRecord{
		"comp-healthy-1": {
			Name:  "comp-healthy-1",
			State: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			Spec: &componentspb.ComponentSpec{
				Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
				Version: "v1.0.0",
			},
			Status: &componentspb.ComponentAccessStatus{
				State: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
				Ready: true,
			},
		},
		"comp-healthy-no-status": {
			Name:  "comp-healthy-no-status",
			State: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			Spec: &componentspb.ComponentSpec{
				Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
				Version: "v1.0.0",
			},
			Status: nil,
		},
		"comp-unhealthy-not-ready": {
			Name:  "comp-unhealthy-not-ready",
			State: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			Spec: &componentspb.ComponentSpec{
				Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
				Version: "v1.0.0",
			},
			Status: &componentspb.ComponentAccessStatus{
				State: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
				Ready: false,
			},
		},
		"comp-disabled": {
			Name:  "comp-disabled",
			State: componentspb.ComponentState_COMPONENT_STATE_DISABLED,
			Spec: &componentspb.ComponentSpec{
				Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
				Version: "v1.0.0",
			},
			Status: &componentspb.ComponentAccessStatus{
				State: componentspb.ComponentState_COMPONENT_STATE_DISABLED,
				Ready: false,
			},
		},
		"comp-disabling": {
			Name:  "comp-disabling",
			State: componentspb.ComponentState_COMPONENT_STATE_DISABLING,
			Spec: &componentspb.ComponentSpec{
				Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
				Version: "v1.0.0",
			},
			Status: &componentspb.ComponentAccessStatus{
				State: componentspb.ComponentState_COMPONENT_STATE_DISABLING,
				Ready: true,
			},
		},
		"comp-deleted": {
			Name:       "comp-deleted",
			State:      componentspb.ComponentState_COMPONENT_STATE_DELETED,
			DeleteTime: &delTime,
		},
	}
}

func TestAggregatePlatformHealth_FilteringAndCounts(t *testing.T) {
	components := createTestComponentsMap()

	tests := []struct {
		name          string
		filter        string
		wantTotal     int
		wantHealthy   int32
		wantUnhealthy int32
		wantErrCode   codes.Code
	}{
		{
			name:          "EmptyFilter_AllNonDeleted",
			filter:        "",
			wantTotal:     5,
			wantHealthy:   2,
			wantUnhealthy: 3,
		},
		{
			name:          "Filter_StateEnabled",
			filter:        "state=ENABLED",
			wantTotal:     3,
			wantHealthy:   2,
			wantUnhealthy: 1,
		},
		{
			name:          "Filter_StateDisabled",
			filter:        "state=DISABLED",
			wantTotal:     1,
			wantHealthy:   0,
			wantUnhealthy: 1,
		},
		{
			name:          "Filter_StateDisabling",
			filter:        "state=DISABLING",
			wantTotal:     1,
			wantHealthy:   0,
			wantUnhealthy: 1,
		},
		{
			name:          "Filter_StateWithPrefix",
			filter:        "state=COMPONENT_STATE_ENABLED",
			wantTotal:     3,
			wantHealthy:   2,
			wantUnhealthy: 1,
		},
		{
			name:          "Filter_StateNumeric",
			filter:        "state=1",
			wantTotal:     3,
			wantHealthy:   2,
			wantUnhealthy: 1,
		},
		{
			name:          "Filter_Type1P",
			filter:        "type=CUSTOM_1P_COMPONENT",
			wantTotal:     4,
			wantHealthy:   2,
			wantUnhealthy: 2,
		},
		{
			name:          "Filter_Type3P",
			filter:        "type=CUSTOM_3P_WRAPPER",
			wantTotal:     1,
			wantHealthy:   0,
			wantUnhealthy: 1,
		},
		{
			name:          "Filter_TypeNumeric",
			filter:        "type=2",
			wantTotal:     1,
			wantHealthy:   0,
			wantUnhealthy: 1,
		},
		{
			name:          "Filter_ReadyTrue",
			filter:        "ready=true",
			wantTotal:     2,
			wantHealthy:   1,
			wantUnhealthy: 1,
		},
		{
			name:          "Filter_ReadyFalse",
			filter:        "ready=false",
			wantTotal:     2,
			wantHealthy:   0,
			wantUnhealthy: 2,
		},
		{
			name:          "Filter_NameCanonical",
			filter:        "name=comp-healthy-1",
			wantTotal:     1,
			wantHealthy:   1,
			wantUnhealthy: 0,
		},
		{
			name:          "Filter_NameFullResource",
			filter:        "name=components/comp-healthy-1",
			wantTotal:     1,
			wantHealthy:   1,
			wantUnhealthy: 0,
		},
		{
			name:          "CompoundFilter_And",
			filter:        "state=ENABLED AND ready=true",
			wantTotal:     1,
			wantHealthy:   1,
			wantUnhealthy: 0,
		},
		{
			name:          "CompoundFilter_Comma",
			filter:        "type=CUSTOM_1P_COMPONENT,state=ENABLED",
			wantTotal:     2,
			wantHealthy:   2,
			wantUnhealthy: 0,
		},
		{
			name:          "Filter_ColonDelimiter",
			filter:        "state:ENABLED",
			wantTotal:     3,
			wantHealthy:   2,
			wantUnhealthy: 1,
		},
		{
			name:          "Filter_InvalidReadyBoolean",
			filter:        "ready=notabool",
			wantTotal:     0,
			wantHealthy:   0,
			wantUnhealthy: 0,
		},
		{
			name:        "Error_UnknownFilterKey",
			filter:      "unknown_key=foo",
			wantErrCode: codes.InvalidArgument,
		},
		{
			name:        "Error_InvalidFilterSyntax",
			filter:      "just_a_string_without_delimiter",
			wantErrCode: codes.InvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := AggregatePlatformHealth(components, tc.filter, 0, "")
			if tc.wantErrCode != codes.OK {
				if status.Code(err) != tc.wantErrCode {
					t.Errorf("got code %v, want %v (err: %v)", status.Code(err), tc.wantErrCode, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("AggregatePlatformHealth() unexpected error: %v", err)
			}
			if len(resp.GetComponents()) != tc.wantTotal {
				t.Errorf("len(Components) = %d, want %d", len(resp.GetComponents()), tc.wantTotal)
			}
			if resp.GetTotalHealthy() != tc.wantHealthy {
				t.Errorf("TotalHealthy = %d, want %d", resp.GetTotalHealthy(), tc.wantHealthy)
			}
			if resp.GetTotalUnhealthy() != tc.wantUnhealthy {
				t.Errorf("TotalUnhealthy = %d, want %d", resp.GetTotalUnhealthy(), tc.wantUnhealthy)
			}
		})
	}
}

func TestAggregatePlatformHealth_Pagination(t *testing.T) {
	components := make(map[string]*ComponentRecord)
	for i := 0; i < 25; i++ {
		name := fmt.Sprintf("comp-%02d", i)
		components[name] = &ComponentRecord{
			Name:  name,
			State: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			Spec: &componentspb.ComponentSpec{
				Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
				Version: "v1.0.0",
			},
			Status: &componentspb.ComponentAccessStatus{
				State: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
				Ready: true,
			},
		}
	}

	// Page 1: pageSize = 10
	resp1, err := AggregatePlatformHealth(components, "", 10, "")
	if err != nil {
		t.Fatalf("Page 1 failed: %v", err)
	}
	if len(resp1.GetComponents()) != 10 {
		t.Fatalf("Page 1 len = %d, want 10", len(resp1.GetComponents()))
	}
	if resp1.GetComponents()[0].GetName() != "components/comp-00" {
		t.Errorf("First item = %q, want 'components/comp-00'", resp1.GetComponents()[0].GetName())
	}
	if resp1.GetComponents()[9].GetName() != "components/comp-09" {
		t.Errorf("Last item = %q, want 'components/comp-09'", resp1.GetComponents()[9].GetName())
	}
	if resp1.GetNextPageToken() == "" {
		t.Fatal("Page 1 NextPageToken is empty")
	}
	if resp1.GetTotalHealthy() != 25 {
		t.Errorf("TotalHealthy = %d, want 25", resp1.GetTotalHealthy())
	}

	// Page 2: using NextPageToken from Page 1
	resp2, err := AggregatePlatformHealth(components, "", 10, resp1.GetNextPageToken())
	if err != nil {
		t.Fatalf("Page 2 failed: %v", err)
	}
	if len(resp2.GetComponents()) != 10 {
		t.Fatalf("Page 2 len = %d, want 10", len(resp2.GetComponents()))
	}
	if resp2.GetComponents()[0].GetName() != "components/comp-10" {
		t.Errorf("Page 2 first item = %q, want 'components/comp-10'", resp2.GetComponents()[0].GetName())
	}
	if resp2.GetNextPageToken() == "" {
		t.Fatal("Page 2 NextPageToken is empty")
	}

	// Page 3: final page
	resp3, err := AggregatePlatformHealth(components, "", 10, resp2.GetNextPageToken())
	if err != nil {
		t.Fatalf("Page 3 failed: %v", err)
	}
	if len(resp3.GetComponents()) != 5 {
		t.Fatalf("Page 3 len = %d, want 5", len(resp3.GetComponents()))
	}
	if resp3.GetComponents()[0].GetName() != "components/comp-20" {
		t.Errorf("Page 3 first item = %q, want 'components/comp-20'", resp3.GetComponents()[0].GetName())
	}
	if resp3.GetNextPageToken() != "" {
		t.Errorf("Page 3 NextPageToken = %q, want empty string", resp3.GetNextPageToken())
	}

	// Offset out of range
	outOfRangeToken := base64.StdEncoding.EncodeToString([]byte("offset:100"))
	respEnd, err := AggregatePlatformHealth(components, "", 10, outOfRangeToken)
	if err != nil {
		t.Fatalf("Out of range token failed: %v", err)
	}
	if len(respEnd.GetComponents()) != 0 {
		t.Errorf("len(Components) = %d, want 0", len(respEnd.GetComponents()))
	}
	if respEnd.GetNextPageToken() != "" {
		t.Errorf("NextPageToken = %q, want empty", respEnd.GetNextPageToken())
	}
}

func TestAggregatePlatformHealth_PageTokenValidation(t *testing.T) {
	components := createTestComponentsMap()

	tests := []struct {
		name      string
		pageToken string
		wantErr   codes.Code
	}{
		{
			name:      "InvalidBase64",
			pageToken: "not-valid-base64!@#$",
			wantErr:   codes.InvalidArgument,
		},
		{
			name:      "InvalidPrefix",
			pageToken: base64.StdEncoding.EncodeToString([]byte("cursor:10")),
			wantErr:   codes.InvalidArgument,
		},
		{
			name:      "NonNumericOffset",
			pageToken: base64.StdEncoding.EncodeToString([]byte("offset:abc")),
			wantErr:   codes.InvalidArgument,
		},
		{
			name:      "NegativeOffset",
			pageToken: base64.StdEncoding.EncodeToString([]byte("offset:-5")),
			wantErr:   codes.InvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := AggregatePlatformHealth(components, "", 10, tc.pageToken)
			if status.Code(err) != tc.wantErr {
				t.Errorf("got code %v, want %v (err: %v)", status.Code(err), tc.wantErr, err)
			}
		})
	}
}

func TestAggregatePlatformHealth_PageSizeBounding(t *testing.T) {
	components := createTestComponentsMap()

	// Page size 0 -> defaults to 50
	respDefault, err := AggregatePlatformHealth(components, "", 0, "")
	if err != nil {
		t.Fatalf("Default page size failed: %v", err)
	}
	if len(respDefault.GetComponents()) != 5 {
		t.Errorf("len(Components) = %d, want 5", len(respDefault.GetComponents()))
	}

	// Page size > 1000 -> capped at 1000
	respCapped, err := AggregatePlatformHealth(components, "", 2000, "")
	if err != nil {
		t.Fatalf("Capped page size failed: %v", err)
	}
	if len(respCapped.GetComponents()) != 5 {
		t.Errorf("len(Components) = %d, want 5", len(respCapped.GetComponents()))
	}
}

func TestHealthAggregator_InternalHelpers(t *testing.T) {
	if isHealthyComponent(nil) {
		t.Error("isHealthyComponent(nil) = true, want false")
	}

	recNilSpec := &ComponentRecord{Name: "nil-spec", Spec: nil}
	if matchesTypePredicate(recNilSpec, "CUSTOM_1P_COMPONENT") {
		t.Error("matchesTypePredicate with nil Spec = true, want false")
	}

	rec := &ComponentRecord{Name: "dummy"}
	if matchesPredicate(rec, filterPredicate{key: "unknown", value: "val"}) {
		t.Error("matchesPredicate with unknown key = true, want false")
	}
}
