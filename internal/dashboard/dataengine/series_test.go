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
		expectDownsam bool
	}{
		{
			name: "raw identity series success",
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
				{int64(10), 0.95, 0.95, 0.95, sampleTime, "train/loss", int64(1), int64(1001)},
				{int64(20), 0.85, 0.85, 0.85, sampleTime, "train/loss", int64(1), int64(1001)},
				{int64(30), 0.75, 0.75, 0.75, sampleTime, "train/loss", int64(1), int64(1001)},
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
				{int64(0), 0.90, 0.80, 1.00, sampleTime, "train/loss", int64(1), int64(1001)},
				{int64(100), 0.70, 0.60, 0.80, sampleTime, "train/loss", int64(1), int64(1001)},
			},
			wantSeries:    1,
			wantPoints:    2,
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
				{int64(10), 0.95, 0.95, 0.95, sampleTime, "acc", int64(1), int64(1001)},
				{int64(10), 0.15, 0.15, 0.15, sampleTime, "loss", int64(1), int64(1001)},
				{int64(10), 0.12, 0.12, 0.12, sampleTime, "loss", int64(2), int64(1001)},
			},
			wantSeries:    3,
			wantPoints:    1,
			expectDownsam: false,
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
		})
	}
}
