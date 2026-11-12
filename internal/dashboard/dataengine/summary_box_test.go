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
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	dpb "xmanager_cloud/api/protos/dashboard"
)

func TestFetchSummaryBox(t *testing.T) {
	sampleTime := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name          string
		xid           int64
		wid           int64
		plot          *dpb.PlotSpec
		mockData      [][]any
		mockErr       error
		wantErr       bool
		wantCount     int
		wantLatestVal float64
		wantLatestStp int64
	}{
		{
			name: "single metric success",
			xid:  1001,
			wid:  1,
			plot: &dpb.PlotSpec{
				PlotId: "summary_loss",
				Type:   dpb.PlotType_SUMMARY_BOX,
				Data: &dpb.DataSpec{
					Metrics: []string{"train/loss"},
				},
			},
			mockData: [][]any{
				{"train/loss", 0.052, 0.050, 0.950, 0.320, int64(1000), sampleTime},
			},
			wantCount:     1,
			wantLatestVal: 0.052,
			wantLatestStp: 1000,
		},
		{
			name: "multiple metrics success",
			xid:  1001,
			wid:  0,
			plot: &dpb.PlotSpec{
				PlotId: "summary_multi",
				Type:   dpb.PlotType_SUMMARY_BOX,
				Data: &dpb.DataSpec{
					Metrics: []string{"train/loss", "eval/accuracy"},
				},
			},
			mockData: [][]any{
				{"eval/accuracy", 0.985, 0.500, 0.990, 0.850, int64(1000), sampleTime},
				{"train/loss", 0.052, 0.050, 0.950, 0.320, int64(1000), sampleTime},
			},
			wantCount:     2,
			wantLatestVal: 0.985,
			wantLatestStp: 1000,
		},
		{
			name: "query error",
			xid:  1001,
			wid:  0,
			plot: &dpb.PlotSpec{
				PlotId: "summary_err",
				Type:   dpb.PlotType_SUMMARY_BOX,
				Data: &dpb.DataSpec{
					Metrics: []string{"train/loss"},
				},
			},
			mockErr: fmt.Errorf("connection refused"),
			wantErr: true,
		},
		{
			name:    "nil plot spec",
			xid:     1001,
			wid:     0,
			plot:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var recordedQuery string
			mockConn := &mockClickHouseConn{
				queryFn: func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
					recordedQuery = query
					if tt.mockErr != nil {
						return nil, tt.mockErr
					}
					return &mockRows{Data: tt.mockData}, nil
				},
			}

			engine, err := New(mockConn)
			if err != nil {
				t.Fatalf("New() unexpected error: %v", err)
			}

			plotsData, err := engine.fetchSummaryBox(context.Background(), tt.xid, tt.wid, tt.plot)
			if (err != nil) != tt.wantErr {
				t.Fatalf("fetchSummaryBox() error = %v, wantErr = %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}

			if !strings.Contains(recordedQuery, "argMax(metric_value, (timestamp, step))") {
				t.Errorf("query missing argMax metric_value: %s", recordedQuery)
			}
			if !strings.Contains(recordedQuery, "argMax(step, (timestamp, step))") {
				t.Errorf("query missing argMax step: %s", recordedQuery)
			}

			if len(plotsData) != tt.wantCount {
				t.Fatalf("fetchSummaryBox() returned %d plotsData, want %d", len(plotsData), tt.wantCount)
			}

			first := plotsData[0].GetSummaryData()
			if first == nil {
				t.Fatalf("plotsData[0].SummaryData is nil")
			}
			if first.GetLatestValue() != tt.wantLatestVal {
				t.Errorf("LatestValue = %f, want %f", first.GetLatestValue(), tt.wantLatestVal)
			}
			if first.GetLatestStep() != tt.wantLatestStp {
				t.Errorf("LatestStep = %d, want %d", first.GetLatestStep(), tt.wantLatestStp)
			}
		})
	}
}
