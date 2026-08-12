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
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
)

func TestLoadTestCases(t *testing.T) {
	cases, err := LoadTestCases("testdata/test_cases.json")
	if err != nil {
		t.Fatalf("LoadTestCases failed: %v", err)
	}

	if len(cases) == 0 {
		t.Fatal("expected at least one test case")
	}

	for _, tc := range cases {
		if tc.ID == "" {
			t.Error("test case missing ID")
		}
		if tc.Chart == nil {
			t.Errorf("test case %q missing parsed Chart proto", tc.ID)
		}
		if tc.Request == nil {
			t.Errorf("test case %q missing parsed Request proto", tc.ID)
		}
	}
}

func TestCompareChartDataAndSaveGolden(t *testing.T) {
	tempDir := t.TempDir()
	goldenPath := filepath.Join(tempDir, "sample.golden.json")

	sampleTime := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	sampleData := &dpb.ChartData{
		Name: "experiments/9001/charts/sample",
		PlotsData: []*dpb.ChartData_PlotData{
			{
				PlotId: "kpi_sample",
				Type:   dpb.PlotType_SUMMARY_BOX,
				Content: &dpb.ChartData_PlotData_SummaryData{
					SummaryData: &dpb.ChartData_SummaryBoxValue{
						MetricName:      "loss",
						LatestValue:     0.15,
						MinValue:        0.10,
						MaxValue:        1.50,
						MeanValue:       0.45,
						LatestStep:      500,
						LatestTimestamp: timestamppb.New(sampleTime),
					},
				},
			},
		},
	}

	if err := SaveGolden(goldenPath, sampleData); err != nil {
		t.Fatalf("SaveGolden failed: %v", err)
	}

	diff, err := CompareChartData(sampleData, goldenPath)
	if err != nil {
		t.Fatalf("CompareChartData failed: %v", err)
	}
	if diff != "" {
		t.Errorf("unexpected diff against newly saved golden file:\n%s", diff)
	}
}
