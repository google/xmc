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

package dashboard

import (
	"context"
	"time"

	"cloud.google.com/go/spanner"
	"google.golang.org/api/iterator"
	"google.golang.org/protobuf/proto"

	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
)

type Repository struct {
	client *spanner.Client
}

func NewRepository(client *spanner.Client) *Repository {
	return &Repository{client: client}
}

func (r *Repository) CreateDashboard(ctx context.Context, dashboard *dpb.Dashboard) error {
	var layoutBytes []byte
	if dashboard.ChartLayout != nil {
		var err error
		layoutBytes, err = proto.Marshal(dashboard.ChartLayout)
		if err != nil {
			return err
		}
	}

	var mutations []*spanner.Mutation

	m := spanner.InsertOrUpdate("Dashboards", []string{
		"DashboardId", "Title", "Description", "Owner", "ChartLayout", "CreatedAt", "UpdatedAt",
	}, []interface{}{
		dashboard.Name, dashboard.Title, dashboard.Description, dashboard.Owner, layoutBytes, spanner.CommitTimestamp, spanner.CommitTimestamp,
	})
	mutations = append(mutations, m)

	for _, chart := range dashboard.Charts {
		mChart := spanner.InsertOrUpdate("DashboardCharts", []string{
			"DashboardId", "DashboardChartId", "ChartId", "GridPosition", "CreatedAt",
		}, []interface{}{
			dashboard.Name, chart.Name, chart.Name, nil, spanner.CommitTimestamp,
		})
		mutations = append(mutations, mChart)
	}

	_, err := r.client.Apply(ctx, mutations)
	return err
}

func (r *Repository) GetDashboard(ctx context.Context, dashboardId string) (*dpb.Dashboard, error) {
	txn := r.client.ReadOnlyTransaction()
	defer txn.Close()

	row, err := txn.ReadRow(ctx, "Dashboards", spanner.Key{dashboardId}, []string{
		"DashboardId", "Title", "Description", "Owner", "ChartLayout", "CreatedAt", "UpdatedAt",
	})
	if err != nil {
		return nil, err
	}

	var (
		id, title, desc, owner string
		layoutBytes            []byte
		createdAt, updatedAt   time.Time
	)
	if err := row.Columns(&id, &title, &desc, &owner, &layoutBytes, &createdAt, &updatedAt); err != nil {
		return nil, err
	}

	iter := txn.Query(ctx, spanner.Statement{
		SQL:    "SELECT ChartId, GridPosition FROM DashboardCharts WHERE DashboardId = @dashboardId",
		Params: map[string]interface{}{"dashboardId": id},
	})
	defer iter.Stop()

	var charts []*dpb.Chart
	for {
		dcRow, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		var (
			chartId string
			gridPos []byte
		)
		if err := dcRow.Columns(&chartId, &gridPos); err != nil {
			return nil, err
		}
		charts = append(charts, &dpb.Chart{Name: chartId})
	}

	var layout *dpb.LayoutSpec
	if len(layoutBytes) > 0 {
		layout = &dpb.LayoutSpec{}
		if err := proto.Unmarshal(layoutBytes, layout); err != nil {
			return nil, err
		}
	}

	return &dpb.Dashboard{
		Name:        id,
		Title:       title,
		Description: desc,
		Owner:       owner,
		Charts:      charts,
		ChartLayout: layout,
	}, nil
}

func (r *Repository) CreateChart(ctx context.Context, chart *dpb.Chart) error {
	var layoutBytes []byte
	if chart.PlotLayout != nil {
		var err error
		layoutBytes, err = proto.Marshal(chart.PlotLayout)
		if err != nil {
			return err
		}
	}

	var mutations []*spanner.Mutation

	m := spanner.InsertOrUpdate("Charts", []string{
		"ChartId", "Title", "Xid", "PlotLayout", "Owner", "CreatedAt", "UpdatedAt",
	}, []interface{}{
		chart.Name, chart.Title, chart.Xid, layoutBytes, "", spanner.CommitTimestamp, spanner.CommitTimestamp,
	})
	mutations = append(mutations, m)

	for _, plot := range chart.Plots {
		plotSpecBytes, err := proto.Marshal(plot)
		if err != nil {
			return err
		}
		mPlot := spanner.InsertOrUpdate("ChartPlots", []string{
			"ChartId", "PlotId", "Title", "PlotType", "PlotSpec",
		}, []interface{}{
			chart.Name, plot.PlotId, plot.Title, int64(plot.Type), plotSpecBytes,
		})
		mutations = append(mutations, mPlot)
	}

	mArtifact := spanner.InsertOrUpdate("ArtifactCharts", []string{
		"Xid", "WorkUnitId", "ArtifactId", "ArtifactChartId", "ChartId", "CreatedAt",
	}, []interface{}{
		chart.Xid, chart.Wid, int64(0), chart.Name, chart.Name, spanner.CommitTimestamp,
	})
	mutations = append(mutations, mArtifact)

	_, err := r.client.Apply(ctx, mutations)
	return err
}

func (r *Repository) GetChart(ctx context.Context, chartId string) (*dpb.Chart, error) {
	txn := r.client.ReadOnlyTransaction()
	defer txn.Close()

	row, err := txn.ReadRow(ctx, "Charts", spanner.Key{chartId}, []string{
		"ChartId", "Title", "Xid", "PlotLayout", "Owner", "CreatedAt", "UpdatedAt",
	})
	if err != nil {
		return nil, err
	}

	var (
		id, title, owner   string
		xid                int64
		layoutBytes        []byte
		createdAt, updated time.Time
	)
	if err := row.Columns(&id, &title, &xid, &layoutBytes, &owner, &createdAt, &updated); err != nil {
		return nil, err
	}

	iter := txn.Query(ctx, spanner.Statement{
		SQL:    "SELECT PlotId, Title, PlotType, PlotSpec FROM ChartPlots WHERE ChartId = @chartId",
		Params: map[string]interface{}{"chartId": id},
	})
	defer iter.Stop()

	var plots []*dpb.PlotSpec
	for {
		plotRow, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		var (
			plotId, pTitle string
			plotType       int64
			plotSpecBytes  []byte
		)
		if err := plotRow.Columns(&plotId, &pTitle, &plotType, &plotSpecBytes); err != nil {
			return nil, err
		}

		var plotSpec dpb.PlotSpec
		if err := proto.Unmarshal(plotSpecBytes, &plotSpec); err != nil {
			return nil, err
		}
		plots = append(plots, &plotSpec)
	}

	var layout *dpb.LayoutSpec
	if len(layoutBytes) > 0 {
		layout = &dpb.LayoutSpec{}
		if err := proto.Unmarshal(layoutBytes, layout); err != nil {
			return nil, err
		}
	}

	return &dpb.Chart{
		Name:       id,
		Title:      title,
		Xid:        xid,
		Plots:      plots,
		PlotLayout: layout,
	}, nil
}
