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

package dataengine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/testing/protocmp"

	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	spb "xmanager_cloud/dashboard_service/proto/service_go_proto"
)

// TestCase defines a single test scenario for the DataEngine.
type TestCase struct {
	ID          string                   `json:"id"`
	Description string                   `json:"description"`
	GoldenFile  string                   `json:"golden_file"`
	ChartJSON   json.RawMessage          `json:"chart"`
	RequestJSON json.RawMessage          `json:"request"`
	Chart       *dpb.Chart               `json:"-"`
	Request     *spb.GetChartDataRequest `json:"-"`
}

// LoadTestCases parses the test case specifications file.
func LoadTestCases(filePath string) ([]*TestCase, error) {
	resolvedPath, err := FindFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to locate test cases file: %w", err)
	}

	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read test cases file %q: %w", resolvedPath, err)
	}

	var rawCases []*TestCase
	if err := json.Unmarshal(data, &rawCases); err != nil {
		return nil, fmt.Errorf("failed to parse test cases JSON: %w", err)
	}

	unmarshaler := protojson.UnmarshalOptions{DiscardUnknown: true}
	for _, tc := range rawCases {
		tc.Chart = &dpb.Chart{}
		if err := unmarshaler.Unmarshal(tc.ChartJSON, tc.Chart); err != nil {
			return nil, fmt.Errorf("failed to unmarshal chart for test case %q: %w", tc.ID, err)
		}

		tc.Request = &spb.GetChartDataRequest{}
		if err := unmarshaler.Unmarshal(tc.RequestJSON, tc.Request); err != nil {
			return nil, fmt.Errorf("failed to unmarshal request for test case %q: %w", tc.ID, err)
		}
	}

	return rawCases, nil
}

// CompareChartData compares actual ChartData against the golden reference file.
func CompareChartData(actual *dpb.ChartData, goldenFilePath string) (string, error) {
	resolvedPath, err := FindFile(goldenFilePath)
	if err != nil {
		return "", fmt.Errorf("golden file %q not found: %w", goldenFilePath, err)
	}

	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return "", fmt.Errorf("failed to read golden file %q: %w", resolvedPath, err)
	}

	expected := &dpb.ChartData{}
	unmarshaler := protojson.UnmarshalOptions{DiscardUnknown: true}
	if err := unmarshaler.Unmarshal(data, expected); err != nil {
		return "", fmt.Errorf("failed to parse golden protobuf JSON %q: %w", resolvedPath, err)
	}

	diff := cmp.Diff(expected, actual, protocmp.Transform())
	return diff, nil
}

// SaveGolden marshals actual ChartData to formatted JSON and writes it to disk.
func SaveGolden(goldenFilePath string, actual *dpb.ChartData) error {
	marshaler := protojson.MarshalOptions{
		Multiline:       true,
		Indent:          "  ",
		UseProtoNames:   true,
		EmitUnpopulated: false,
	}

	bytes, err := marshaler.Marshal(actual)
	if err != nil {
		return fmt.Errorf("failed to marshal chart data to JSON: %w", err)
	}

	dir := filepath.Dir(goldenFilePath)
	if dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %q: %w", dir, err)
		}
	}

	if err := os.WriteFile(goldenFilePath, bytes, 0644); err != nil {
		return fmt.Errorf("failed to write golden file %q: %w", goldenFilePath, err)
	}

	return nil
}
