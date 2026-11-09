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
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	dpb "xmanager_cloud/api/protos/dashboard"
	xmcbigquery "xmanager_cloud/pkg/bigquery"
)

type stubBigQueryConn struct {
	tableRef string
	queryFn  func(ctx context.Context, sql string, args ...any) (xmcbigquery.Rows, error)
	pingFn   func(ctx context.Context) error
	closed   atomic.Bool
}

func (s *stubBigQueryConn) TableRef() string { return s.tableRef }

func (s *stubBigQueryConn) Query(ctx context.Context, sql string, args ...any) (xmcbigquery.Rows, error) {
	return s.queryFn(ctx, sql, args...)
}

func (s *stubBigQueryConn) Ping(ctx context.Context) error {
	if s.pingFn != nil {
		return s.pingFn(ctx)
	}
	return nil
}

func (s *stubBigQueryConn) Close() error {
	s.closed.Store(true)
	return nil
}

func TestBigQueryEngineAndSQLGeneration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if _, err := NewBigQuery(nil); err == nil {
		t.Fatal("NewBigQuery(nil) succeeded, want error")
	}
	if _, err := NewBigQuery(&stubBigQueryConn{}); err != nil {
		t.Fatalf("NewBigQuery(empty TableRef) error: %v", err)
	}

	sampleTime := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	var queries []string
	conn := &stubBigQueryConn{
		tableRef: "`proj.ds.tbl`",
		queryFn: func(_ context.Context, sql string, _ ...any) (xmcbigquery.Rows, error) {
			queries = append(queries, sql)
			if strings.Contains(sql, "ARRAY_AGG") {
				return &mockRows{Data: [][]any{{"loss", 0.05, 0.01, 0.99, 0.25, int64(500), sampleTime}}}, nil
			}
			return &mockRows{Data: [][]any{{int64(0), 0.9, 0.8, 1.0, sampleTime, "acc", int64(1), int64(1001)}}}, nil
		},
	}
	engine, err := NewBigQuery(conn)
	if err != nil {
		t.Fatalf("NewBigQuery() error: %v", err)
	}
	if err := engine.Ping(ctx); err != nil {
		t.Errorf("Ping() error: %v", err)
	}
	conn.pingFn = func(context.Context) error { return errors.New("unavailable") }
	if err := engine.Ping(ctx); err == nil {
		t.Error("Ping() succeeded on failing BigQuery conn, want error")
	}

	chartData, err := engine.FetchChartData(ctx, &dpb.Chart{
		Name: "experiments/1001/workUnits/1/charts/bq", Xid: 1001, Wid: 1,
		Plots: []*dpb.PlotSpec{
			{PlotId: "sum", Type: dpb.PlotType_SUMMARY_BOX, Data: &dpb.DataSpec{Metrics: []string{"loss"}}},
			{
				PlotId: "scat", Type: dpb.PlotType_SCATTER,
				Data: &dpb.DataSpec{
					Metrics: []string{"acc"},
					Ranges: []*dpb.DataSpec_DataRange{{
						Start: &dpb.DataSpec_DataRange_StartStep{StartStep: 0},
						End:   &dpb.DataSpec_DataRange_EndStep{EndStep: 50000},
					}},
				},
			},
		},
	}, &dpb.GetChartDataRequest{ChartWidthPixels: 500})
	if err != nil {
		t.Fatalf("FetchChartData() error: %v", err)
	}
	if len(chartData.GetPlotsData()) != 2 {
		t.Fatalf("got %d plots, want 2", len(chartData.GetPlotsData()))
	}
	joined := strings.Join(queries, "\n")
	for _, token := range []string{"`proj.ds.tbl`", "ARRAY_AGG(STRUCT(", "DIV(step, 100) * 100"} {
		if !strings.Contains(joined, token) {
			t.Errorf("BigQuery SQL missing %q:\n%s", token, joined)
		}
	}
	if err := engine.Close(); err != nil || !conn.closed.Load() {
		t.Errorf("Close() err=%v, closed=%v", err, conn.closed.Load())
	}
}
