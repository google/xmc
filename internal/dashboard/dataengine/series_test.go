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
	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/timestamppb"

	dpb "xmanager_cloud/api/protos/dashboard"
)

func TestCalculateBucketSize(t *testing.T) {
	tests := []struct {
		name         string
		ranges       []*dpb.DataSpec_DataRange
		targetPoints int32
		wantBucket   int64
	}{
		{
			name:         "empty ranges defaults to 1",
			ranges:       nil,
			targetPoints: 500,
			wantBucket:   1,
		},
		{
			name: "zero target points falls back to defaultTargetPoints",
			ranges: []*dpb.DataSpec_DataRange{
				nil,
				{
					Start: &dpb.DataSpec_DataRange_StartTime{StartTime: timestamppb.Now()},
					End:   &dpb.DataSpec_DataRange_EndStep{EndStep: 50000},
				},
				{
					Start: &dpb.DataSpec_DataRange_StartStep{StartStep: 0},
					End:   &dpb.DataSpec_DataRange_EndStep{EndStep: 50000},
				},
			},
			targetPoints: 0,
			wantBucket:   50,
		},
		{
			name: "small step range returns 1",
			ranges: []*dpb.DataSpec_DataRange{
				{
					Start: &dpb.DataSpec_DataRange_StartStep{StartStep: 1},
					End:   &dpb.DataSpec_DataRange_EndStep{EndStep: 100},
				},
			},
			targetPoints: 500,
			wantBucket:   1,
		},
		{
			name: "large step range calculates bucket size",
			ranges: []*dpb.DataSpec_DataRange{
				{
					Start: &dpb.DataSpec_DataRange_StartStep{StartStep: 0},
					End:   &dpb.DataSpec_DataRange_EndStep{EndStep: 100000},
				},
			},
			targetPoints: 1000,
			wantBucket:   100,
		},
		{
			name: "omitted start step defaults to 0 and calculates bucket size",
			ranges: []*dpb.DataSpec_DataRange{
				{
					Start: nil,
					End:   &dpb.DataSpec_DataRange_EndStep{EndStep: 100000},
				},
			},
			targetPoints: 1000,
			wantBucket:   100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateBucketSize(tt.ranges, tt.targetPoints)
			if got != tt.wantBucket {
				t.Errorf("calculateBucketSize() = %d, want %d", got, tt.wantBucket)
			}
		})
	}
}

func TestFetchSeriesData(t *testing.T) {
	sampleTime := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	laterTime := sampleTime.Add(45 * time.Second)
	middleTime := sampleTime.Add(15 * time.Second)

	tests := []struct {
		name          string
		xid           int64
		wid           int64
		plot          *dpb.PlotSpec
		widthPixels   int32
		mockData      [][]any
		mockErr       error
		wantErr       bool
		wantSeries    int
		wantPoints    int
		wantPlotsData []*dpb.ChartData_PlotData
		expectDownsam bool
	}{
		{
			name: "raw identity series preserves individual multi-replica points",
			xid:  1001,
			wid:  1,
			plot: &dpb.PlotSpec{
				PlotId: "scatter_loss",
				Type:   dpb.PlotType_SCATTER,
				Data: &dpb.DataSpec{
					Metrics: []string{"train/loss"},
				},
			},
			widthPixels: 1000,
			mockData: [][]any{
				{int64(10), 0.95, 0.95, 0.95, int64(1), sampleTime, "train/loss", int64(1), int64(1001), int64(1), "worker", "0"},
				{int64(10), 0.91, 0.91, 0.91, int64(1), middleTime, "train/loss", int64(1), int64(1001), int64(1), "worker", "1"},
				{int64(20), 0.85, 0.85, 0.85, int64(1), laterTime, "train/loss", int64(1), int64(1001), int64(1), "worker", "0"},
			},
			wantSeries:    1,
			wantPoints:    3,
			expectDownsam: false,
		},
		{
			name: "downsampled series with large step range",
			xid:  1001,
			wid:  1,
			plot: &dpb.PlotSpec{
				PlotId: "scatter_downsampled",
				Type:   dpb.PlotType_SCATTER,
				Data: &dpb.DataSpec{
					Metrics: []string{"train/loss"},
					Ranges: []*dpb.DataSpec_DataRange{
						{
							Start: &dpb.DataSpec_DataRange_StartStep{StartStep: 0},
							End:   &dpb.DataSpec_DataRange_EndStep{EndStep: 100000},
						},
					},
				},
			},
			widthPixels: 1000,
			mockData: [][]any{
				{int64(0), 0.90, 0.80, 1.00, int64(10), sampleTime, "train/loss", int64(1), int64(1001), int64(1), "worker", "0"},
				{int64(100), 0.70, 0.60, 0.80, int64(10), sampleTime, "train/loss", int64(1), int64(1001), int64(1), "worker", "0"},
			},
			wantSeries:    1,
			wantPoints:    2,
			expectDownsam: true,
		},
		{
			name: "downsampled multi-replica and retry rows collapse per bucket step with count-weighted avg",
			xid:  1001,
			wid:  0,
			plot: &dpb.PlotSpec{
				PlotId: "scatter_multi_replica",
				Type:   dpb.PlotType_SCATTER,
				Data: &dpb.DataSpec{
					Metrics: []string{"train/loss"},
					Ranges: []*dpb.DataSpec_DataRange{
						{
							Start: &dpb.DataSpec_DataRange_StartStep{StartStep: 0},
							End:   &dpb.DataSpec_DataRange_EndStep{EndStep: 100000},
						},
					},
				},
			},
			widthPixels: 1000,
			mockData: [][]any{
				// WU 1, Step 0: weighted avg = (1.0*3 + 5.0*1 + 2.0*4) / (3 + 1 + 4) = 16.0 / 8 = 2.0
				// (unweighted mean would be 8.0 / 3 = 2.6667); min = 0.5, max = 5.5, latest = laterTime.
				{int64(0), 1.0, 0.5, 1.5, int64(3), sampleTime, "train/loss", int64(1), int64(1001), int64(1), "trainer", "0"},
				{int64(0), 5.0, 4.8, 5.5, int64(1), laterTime, "train/loss", int64(1), int64(1001), int64(1), "trainer", "1"},
				{int64(0), 2.0, 1.2, 2.8, int64(4), middleTime, "train/loss", int64(1), int64(1001), int64(2), "evaluator", "0"},
				// WU 1, Step 100: single sub-series row remains unchanged.
				{int64(100), 0.75, 0.70, 0.80, int64(5), laterTime, "train/loss", int64(1), int64(1001), int64(2), "trainer", "0"},
				// WU 2, Step 100 & 200: adjacent row with identical Step=100 but distinct work_unit_id must not merge into WU 1.
				{int64(100), 9.0, 8.5, 9.5, int64(0), sampleTime, "train/loss", int64(2), int64(1001), int64(1), "trainer", "0"},
				{int64(200), 4.0, 3.8, 4.2, int64(2), laterTime, "train/loss", int64(2), int64(1001), int64(1), "trainer", "0"},
			},
			wantSeries: 2,
			wantPoints: 2,
			wantPlotsData: []*dpb.ChartData_PlotData{
				{
					PlotId: "scatter_multi_replica",
					Type:   dpb.PlotType_SCATTER,
					Content: &dpb.ChartData_PlotData_ScatterSeries{
						ScatterSeries: &dpb.ChartData_SeriesData{
							MetricName: "train/loss",
							WorkUnitId: "1",
							Xid:        1001,
							Points: []*dpb.ChartData_SeriesPoint{
								{
									Step:      0,
									Timestamp: timestamppb.New(laterTime),
									AvgValue:  2.0,
									MinValue:  0.5,
									MaxValue:  5.5,
								},
								{
									Step:      100,
									Timestamp: timestamppb.New(laterTime),
									AvgValue:  0.75,
									MinValue:  0.70,
									MaxValue:  0.80,
								},
							},
						},
					},
				},
				{
					PlotId: "scatter_multi_replica",
					Type:   dpb.PlotType_SCATTER,
					Content: &dpb.ChartData_PlotData_ScatterSeries{
						ScatterSeries: &dpb.ChartData_SeriesData{
							MetricName: "train/loss",
							WorkUnitId: "2",
							Xid:        1001,
							Points: []*dpb.ChartData_SeriesPoint{
								{
									Step:      100,
									Timestamp: timestamppb.New(sampleTime),
									AvgValue:  9.0,
									MinValue:  8.5,
									MaxValue:  9.5,
								},
								{
									Step:      200,
									Timestamp: timestamppb.New(laterTime),
									AvgValue:  4.0,
									MinValue:  3.8,
									MaxValue:  4.2,
								},
							},
						},
					},
				},
			},
			expectDownsam: true,
		},
		{
			name: "downsampled empty result returns zero series",
			xid:  1001,
			wid:  1,
			plot: &dpb.PlotSpec{
				PlotId: "scatter_empty_downsampled",
				Type:   dpb.PlotType_SCATTER,
				Data: &dpb.DataSpec{
					Metrics: []string{"train/loss"},
					Ranges: []*dpb.DataSpec_DataRange{
						{
							Start: &dpb.DataSpec_DataRange_StartStep{StartStep: 0},
							End:   &dpb.DataSpec_DataRange_EndStep{EndStep: 100000},
						},
					},
				},
			},
			widthPixels:   1000,
			mockData:      nil,
			wantSeries:    0,
			expectDownsam: true,
		},
		{
			name: "multiple series grouped by metric and work unit",
			xid:  1001,
			wid:  0,
			plot: &dpb.PlotSpec{
				PlotId: "scatter_multi",
				Type:   dpb.PlotType_SCATTER,
				Data: &dpb.DataSpec{
					Metrics: []string{"loss", "acc"},
				},
			},
			widthPixels: 1000,
			mockData: [][]any{
				{int64(10), 0.95, 0.95, 0.95, int64(1), sampleTime, "acc", int64(1), int64(1001), int64(1), "worker", "0"},
				{int64(10), 0.15, 0.15, 0.15, int64(1), sampleTime, "loss", int64(1), int64(1001), int64(1), "worker", "0"},
				{int64(10), 0.12, 0.12, 0.12, int64(1), sampleTime, "loss", int64(2), int64(1001), int64(1), "worker", "0"},
			},
			wantSeries:    3,
			wantPoints:    1,
			expectDownsam: false,
		},
		{
			name:    "nil plot returns error",
			xid:     1001,
			wid:     1,
			plot:    nil,
			wantErr: true,
		},
		{
			name: "query error",
			xid:  1001,
			wid:  0,
			plot: &dpb.PlotSpec{
				PlotId: "scatter_err",
				Type:   dpb.PlotType_SCATTER,
				Data: &dpb.DataSpec{
					Metrics: []string{"loss"},
				},
			},
			mockErr: fmt.Errorf("table not found"),
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

			plotsData, err := engine.fetchSeriesData(context.Background(), tt.xid, tt.wid, tt.plot, tt.widthPixels)
			if (err != nil) != tt.wantErr {
				t.Fatalf("fetchSeriesData() error = %v, wantErr = %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}

			if tt.expectDownsam && !strings.Contains(recordedQuery, "intDiv(step") {
				t.Errorf("expected downsampling with intDiv(step, got query: %s", recordedQuery)
			}

			if len(plotsData) != tt.wantSeries {
				t.Fatalf("fetchSeriesData() returned %d series, want %d", len(plotsData), tt.wantSeries)
			}

			for _, pd := range plotsData {
				if pd.GetScatterSeries() == nil {
					t.Fatalf("ScatterSeries is nil")
				}
				if len(pd.GetScatterSeries().GetPoints()) != tt.wantPoints {
					t.Errorf("got %d points, want %d", len(pd.GetScatterSeries().GetPoints()), tt.wantPoints)
				}
			}

			if tt.wantPlotsData != nil {
				if diff := cmp.Diff(tt.wantPlotsData, plotsData, protocmp.Transform()); diff != "" {
					t.Errorf("fetchSeriesData() plotsData mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}
