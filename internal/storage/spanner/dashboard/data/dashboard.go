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

// Package data provides data conversion and serialization helpers between Spanner rows/mutations and Dashboard Protobuf messages.
package data

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"cloud.google.com/go/spanner"
	"google.golang.org/protobuf/proto"
	fieldmaskpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"

	"task/go/status"
	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	"xmanager_cloud/storage/spanner/schema/dashboardschema"
	"xmanager_cloud/validator/validator"
)

// SortCharts returns a new slice with charts sorted alphabetically by Name.
func SortCharts(charts []*dpb.Chart) []*dpb.Chart {
	if len(charts) <= 1 {
		return slices.Clone(charts)
	}
	sorted := slices.Clone(charts)
	slices.SortFunc(sorted, func(a, b *dpb.Chart) int {
		if a == nil && b == nil {
			return 0
		}
		if a == nil {
			return -1
		}
		if b == nil {
			return 1
		}
		return cmp.Compare(a.GetName(), b.GetName())
	})
	return sorted
}

// MergeDashboards merges an updated Dashboard proto message into a current Dashboard proto message based on an update mask.
func MergeDashboards(current, updated *dpb.Dashboard, updateMask *fieldmaskpb.FieldMask) (*dpb.Dashboard, error) {
	if current == nil {
		return nil, fmt.Errorf("current dashboard is nil: %w", status.ErrInvalidArgument)
	}
	if updated == nil {
		return nil, fmt.Errorf("updated dashboard is nil: %w", status.ErrInvalidArgument)
	}

	merged := proto.Clone(current).(*dpb.Dashboard)
	if updateMask == nil || len(updateMask.GetPaths()) == 0 {
		merged.Title = updated.GetTitle()
		merged.Description = updated.GetDescription()
		merged.ChartLayout = updated.GetChartLayout()
		merged.Charts = updated.GetCharts()
		return merged, nil
	}

	for _, path := range updateMask.GetPaths() {
		switch path {
		case "title":
			merged.Title = updated.GetTitle()
		case "description":
			merged.Description = updated.GetDescription()
		case "chart_layout", "chartLayout":
			merged.ChartLayout = updated.GetChartLayout()
		case "charts":
			merged.Charts = updated.GetCharts()
		default:
			return nil, fmt.Errorf("field %q is not updateable: %w", path, status.ErrInvalidArgument)
		}
	}
	return merged, nil
}

// DashboardProtoToInsertMutations creates Spanner mutations to insert a Dashboard and its child DashboardCharts.
func DashboardProtoToInsertMutations(dashboard *dpb.Dashboard) ([]*spanner.Mutation, error) {
	if dashboard == nil {
		return nil, fmt.Errorf("dashboard proto must not be nil: %w", status.ErrInvalidArgument)
	}

	dashID, err := validator.ValidateDashboardName(dashboard.GetName())
	if err != nil {
		return nil, fmt.Errorf("invalid dashboard name: %w", err)
	}

	var layoutBytes []byte
	if dashboard.ChartLayout != nil {
		var err error
		layoutBytes, err = proto.Marshal(dashboard.ChartLayout)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal chart layout: %w", err)
		}
	}

	var mutations []*spanner.Mutation
	m := spanner.InsertOrUpdate(dashboardschema.DashboardsTableName, []string{
		"DashboardId", "Title", "Description", "Owner", "ChartLayout", "CreatedAt", "UpdatedAt",
	}, []interface{}{
		dashID, dashboard.Title, dashboard.Description, dashboard.Owner, layoutBytes, spanner.CommitTimestamp, spanner.CommitTimestamp,
	})
	mutations = append(mutations, m)

	for _, chart := range dashboard.GetCharts() {
		if chart == nil {
			return nil, fmt.Errorf("chart in dashboard charts list must not be nil: %w", status.ErrInvalidArgument)
		}
		mChart := spanner.InsertOrUpdate(dashboardschema.DashboardChartsTableName, []string{
			"DashboardId", "DashboardChartId", "ChartId", "GridPosition", "CreatedAt",
		}, []interface{}{
			dashID, chart.GetName(), chart.GetName(), nil, spanner.CommitTimestamp,
		})
		mutations = append(mutations, mChart)
	}

	return mutations, nil
}

// DashboardProtoToUpdateMutations creates Spanner mutations to update a Dashboard and optionally its child DashboardCharts.
func DashboardProtoToUpdateMutations(dashboard *dpb.Dashboard, updateCharts bool) ([]*spanner.Mutation, error) {
	if dashboard == nil {
		return nil, fmt.Errorf("dashboard proto must not be nil: %w", status.ErrInvalidArgument)
	}
	if dashboard.GetName() == "" {
		return nil, fmt.Errorf("dashboard name must not be empty: %w", status.ErrInvalidArgument)
	}

	dashID, err := validator.ValidateDashboardName(dashboard.GetName())
	if err != nil {
		return nil, fmt.Errorf("invalid dashboard name: %w", err)
	}

	var layoutBytes []byte
	if dashboard.ChartLayout != nil {
		var err error
		layoutBytes, err = proto.Marshal(dashboard.ChartLayout)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal chart layout: %w", err)
		}
	}

	var mutations []*spanner.Mutation
	m := spanner.UpdateMap(dashboardschema.DashboardsTableName, map[string]interface{}{
		"DashboardId": dashID,
		"Title":       dashboard.GetTitle(),
		"Description": dashboard.GetDescription(),
		"ChartLayout": layoutBytes,
		"UpdatedAt":   spanner.CommitTimestamp,
	})
	mutations = append(mutations, m)

	if updateCharts {
		mDel := spanner.Delete(dashboardschema.DashboardChartsTableName, spanner.Key{dashID}.AsPrefix())
		mutations = append(mutations, mDel)

		for _, chart := range dashboard.GetCharts() {
			if chart == nil {
				return nil, fmt.Errorf("chart in dashboard charts list must not be nil: %w", status.ErrInvalidArgument)
			}
			mChart := spanner.InsertOrUpdate(dashboardschema.DashboardChartsTableName, []string{
				"DashboardId", "DashboardChartId", "ChartId", "GridPosition", "CreatedAt",
			}, []interface{}{
				dashID, chart.GetName(), chart.GetName(), nil, spanner.CommitTimestamp,
			})
			mutations = append(mutations, mChart)
		}
	}

	return mutations, nil
}

type dashboardRow struct {
	ID          string    `spanner:"DashboardId"`
	Title       string    `spanner:"Title"`
	Description string    `spanner:"Description"`
	Owner       string    `spanner:"Owner"`
	ChartLayout []byte    `spanner:"ChartLayout"`
	CreatedAt   time.Time `spanner:"CreatedAt"`
	UpdatedAt   time.Time `spanner:"UpdatedAt"`
}

type chartRow struct {
	ID         string    `spanner:"ChartId"`
	Title      string    `spanner:"Title"`
	Xid        int64     `spanner:"Xid"`
	PlotLayout []byte    `spanner:"PlotLayout"`
	Owner      string    `spanner:"Owner"`
	CreatedAt  time.Time `spanner:"CreatedAt"`
	UpdatedAt  time.Time `spanner:"UpdatedAt"`
}

type artifactChartRow struct {
	BitReversedXid  int64             `spanner:"BitReversedXid"`
	WorkUnitId      spanner.NullInt64 `spanner:"WorkUnitId"`
	ArtifactId      int64             `spanner:"ArtifactId"`
	ArtifactChartId string            `spanner:"ArtifactChartId"`
}

type dashboardChartRow struct {
	DashboardId      string `spanner:"DashboardId"`
	DashboardChartId string `spanner:"DashboardChartId"`
}

// DashboardRowToProto converts a Spanner Row into a Dashboard protobuf message.
func DashboardRowToProto(row *spanner.Row, charts []*dpb.Chart) (*dpb.Dashboard, error) {
	if row == nil {
		return nil, fmt.Errorf("spanner row must not be nil")
	}

	var data dashboardRow
	if err := row.ToStructLenient(&data); err != nil {
		return nil, fmt.Errorf("failed to decode dashboard row: %w", err)
	}
	if data.ID == "" {
		return nil, fmt.Errorf("dashboard row missing DashboardId")
	}

	var layout *dpb.LayoutSpec
	if len(data.ChartLayout) > 0 {
		layout = &dpb.LayoutSpec{}
		if err := proto.Unmarshal(data.ChartLayout, layout); err != nil {
			return nil, fmt.Errorf("failed to unmarshal dashboard layout: %w", err)
		}
	}

	return &dpb.Dashboard{
		Name:        validator.ConstructDashboardName(data.ID),
		Title:       data.Title,
		Description: data.Description,
		Owner:       data.Owner,
		Charts:      SortCharts(charts),
		ChartLayout: layout,
		CreateTime:  timestamppb.New(data.CreatedAt),
		UpdateTime:  timestamppb.New(data.UpdatedAt),
	}, nil
}

// ChartProtoToInsertMutations creates Spanner mutations to insert a Chart, its PlotSpecs, and ArtifactCharts associations.
func ChartProtoToInsertMutations(chart *dpb.Chart) ([]*spanner.Mutation, error) {
	if chart == nil {
		return nil, fmt.Errorf("chart proto must not be nil")
	}

	var layoutBytes []byte
	if chart.PlotLayout != nil {
		var err error
		layoutBytes, err = proto.Marshal(chart.PlotLayout)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal plot layout: %w", err)
		}
	}

	var mutations []*spanner.Mutation
	m := spanner.InsertOrUpdate(dashboardschema.ChartsTableName, []string{
		"ChartId", "Title", "Xid", "PlotLayout", "Owner", "CreatedAt", "UpdatedAt",
	}, []interface{}{
		chart.Name, chart.Title, chart.Xid, layoutBytes, "", spanner.CommitTimestamp, spanner.CommitTimestamp,
	})
	mutations = append(mutations, m)

	for _, plot := range chart.Plots {
		plotSpecBytes, err := proto.Marshal(plot)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal plot spec: %w", err)
		}
		mPlot := spanner.InsertOrUpdate(dashboardschema.ChartPlotsTableName, []string{
			"ChartId", "PlotId", "Title", "PlotType", "PlotSpec",
		}, []interface{}{
			chart.Name, plot.PlotId, plot.Title, int64(plot.Type), plotSpecBytes,
		})
		mutations = append(mutations, mPlot)
	}

	mArtifact := spanner.InsertOrUpdate(dashboardschema.ArtifactChartsTableName, []string{
		"Xid", "WorkUnitId", "ArtifactId", "ArtifactChartId", "ChartId", "CreatedAt",
	}, []interface{}{
		chart.Xid, chart.Wid, int64(0), chart.Name, chart.Name, spanner.CommitTimestamp,
	})
	mutations = append(mutations, mArtifact)

	return mutations, nil
}

// ChartRowToProto converts a Spanner Row into a Chart protobuf message.
func ChartRowToProto(row *spanner.Row, plots []*dpb.PlotSpec) (*dpb.Chart, error) {
	if row == nil {
		return nil, fmt.Errorf("spanner row must not be nil")
	}

	var data chartRow
	if err := row.ToStructLenient(&data); err != nil {
		return nil, fmt.Errorf("failed to decode chart row: %w", err)
	}
	if data.ID == "" {
		return nil, fmt.Errorf("chart row missing ChartId")
	}

	var layout *dpb.LayoutSpec
	if len(data.PlotLayout) > 0 {
		layout = &dpb.LayoutSpec{}
		if err := proto.Unmarshal(data.PlotLayout, layout); err != nil {
			return nil, fmt.Errorf("failed to unmarshal chart layout: %w", err)
		}
	}

	return &dpb.Chart{
		Name:       data.ID,
		Title:      data.Title,
		Xid:        data.Xid,
		Plots:      plots,
		PlotLayout: layout,
	}, nil
}

// ChartProtoToUpdateMutation creates a Spanner mutation to update a Chart.
func ChartProtoToUpdateMutation(chart *dpb.Chart) (*spanner.Mutation, error) {
	if chart == nil {
		return nil, fmt.Errorf("chart proto must not be nil")
	}
	if chart.GetName() == "" {
		return nil, fmt.Errorf("chart name must not be empty")
	}

	var layoutBytes []byte
	if chart.PlotLayout != nil {
		var err error
		layoutBytes, err = proto.Marshal(chart.PlotLayout)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal plot layout: %w", err)
		}
	}

	return spanner.Update(dashboardschema.ChartsTableName, []string{
		"ChartId", "Title", "PlotLayout", "UpdatedAt",
	}, []interface{}{
		chart.GetName(), chart.GetTitle(), layoutBytes, spanner.CommitTimestamp,
	}), nil
}

// ChartPlotsToDeleteMutation creates a Spanner mutation to delete all plot specifications for a Chart.
func ChartPlotsToDeleteMutation(chartID string) *spanner.Mutation {
	return spanner.Delete(dashboardschema.ChartPlotsTableName, spanner.Key{chartID}.AsPrefix())
}

// PlotSpecsToInsertMutations creates Spanner mutations to insert or update plot specifications for a Chart.
func PlotSpecsToInsertMutations(chartID string, plots []*dpb.PlotSpec) ([]*spanner.Mutation, error) {
	var mutations []*spanner.Mutation
	for _, plot := range plots {
		if plot == nil {
			continue
		}
		plotSpecBytes, err := proto.Marshal(plot)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal plot spec: %w", err)
		}
		mPlot := spanner.InsertOrUpdate(dashboardschema.ChartPlotsTableName, []string{
			"ChartId", "PlotId", "Title", "PlotType", "PlotSpec",
		}, []interface{}{
			chartID, plot.GetPlotId(), plot.GetTitle(), int64(plot.GetType()), plotSpecBytes,
		})
		mutations = append(mutations, mPlot)
	}
	return mutations, nil
}

// ChartProtoToUpdateMutations creates Spanner mutations to update a Chart and replace its PlotSpecs.
func ChartProtoToUpdateMutations(chart *dpb.Chart) ([]*spanner.Mutation, error) {
	updateMut, err := ChartProtoToUpdateMutation(chart)
	if err != nil {
		return nil, err
	}
	deleteMut := ChartPlotsToDeleteMutation(chart.GetName())
	insertMuts, err := PlotSpecsToInsertMutations(chart.GetName(), chart.GetPlots())
	if err != nil {
		return nil, err
	}
	return append([]*spanner.Mutation{updateMut, deleteMut}, insertMuts...), nil
}

// ArtifactChartRowToDeleteMutation converts an ArtifactCharts Spanner row into a delete mutation.
func ArtifactChartRowToDeleteMutation(row *spanner.Row) (*spanner.Mutation, error) {
	if row == nil {
		return nil, fmt.Errorf("spanner row must not be nil")
	}
	var data artifactChartRow
	if err := row.ToStructLenient(&data); err != nil {
		return nil, fmt.Errorf("failed to decode artifact chart row: %w", err)
	}
	if data.ArtifactChartId == "" {
		return nil, fmt.Errorf("artifact chart row missing ArtifactChartId")
	}

	pk := spanner.Key{data.BitReversedXid, data.WorkUnitId, data.ArtifactId, data.ArtifactChartId}
	return spanner.Delete(dashboardschema.ArtifactChartsTableName, pk), nil
}

// DashboardChartRowToDeleteMutation converts a DashboardCharts Spanner row into a delete mutation.
func DashboardChartRowToDeleteMutation(row *spanner.Row) (*spanner.Mutation, error) {
	if row == nil {
		return nil, fmt.Errorf("spanner row must not be nil")
	}
	var data dashboardChartRow
	if err := row.ToStructLenient(&data); err != nil {
		return nil, fmt.Errorf("failed to decode dashboard chart row: %w", err)
	}
	if data.DashboardId == "" || data.DashboardChartId == "" {
		return nil, fmt.Errorf("dashboard chart row missing primary key columns")
	}
	pk := spanner.Key{data.DashboardId, data.DashboardChartId}
	return spanner.Delete(dashboardschema.DashboardChartsTableName, pk), nil
}
