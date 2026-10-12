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
	"fmt"
	"slices"
	"time"

	"cloud.google.com/go/spanner"
	"google.golang.org/protobuf/proto"
	fieldmaskpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"

	dpb "xmanager_cloud/api/protos/dashboard"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage/spanner/schema/dashboardschema"
	"xmanager_cloud/internal/validator"
)

// SortChartIDs returns a new cloned and sorted slice of chart IDs, removing duplicates.
func SortChartIDs(chartIDs []string) []string {
	if len(chartIDs) == 0 {
		return nil
	}
	sorted := slices.Clone(chartIDs)
	slices.Sort(sorted)
	return slices.Compact(sorted)
}

// ChartIDsEqual reports whether two sorted chart ID slices are equal.
func ChartIDsEqual(a, b []string) bool {
	return slices.Equal(a, b)
}

// MergeDashboards merges an updated Dashboard proto message into a current Dashboard proto message based on an update mask.
func MergeDashboards(current, updated *dpb.Dashboard, updateMask *fieldmaskpb.FieldMask) (*dpb.Dashboard, error) {
	if current == nil {
		return nil, fmt.Errorf("current dashboard is nil: %w", grpcerr.ErrInvalidArgument)
	}
	if updated == nil {
		return nil, fmt.Errorf("updated dashboard is nil: %w", grpcerr.ErrInvalidArgument)
	}

	merged := proto.Clone(current).(*dpb.Dashboard)
	if updateMask == nil || len(updateMask.GetPaths()) == 0 {
		merged.Title = updated.GetTitle()
		merged.Description = updated.GetDescription()
		merged.ChartLayout = updated.GetChartLayout()
		merged.ChartIds = updated.GetChartIds()
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
		case "chart_ids", "chartIds":
			merged.ChartIds = updated.GetChartIds()
		default:
			return nil, fmt.Errorf("field %q is not updateable: %w", path, grpcerr.ErrInvalidArgument)
		}
	}
	return merged, nil
}

// MergeCharts merges an updated Chart proto message into a current Chart proto message based on an update mask.
// Note: xid and wid are immutable chart ownership fields and cannot be updated.
func MergeCharts(current, updated *dpb.Chart, updateMask *fieldmaskpb.FieldMask) (*dpb.Chart, error) {
	if current == nil {
		return nil, fmt.Errorf("current chart is nil: %w", grpcerr.ErrInvalidArgument)
	}
	if updated == nil {
		return nil, fmt.Errorf("updated chart is nil: %w", grpcerr.ErrInvalidArgument)
	}

	merged := proto.Clone(current).(*dpb.Chart)
	if updateMask == nil || len(updateMask.GetPaths()) == 0 {
		merged.Title = updated.GetTitle()
		merged.Description = updated.GetDescription()
		merged.Plots = updated.GetPlots()
		merged.PlotLayout = updated.GetPlotLayout()
		return merged, nil
	}

	for _, path := range updateMask.GetPaths() {
		switch path {
		case "title":
			merged.Title = updated.GetTitle()
		case "description":
			merged.Description = updated.GetDescription()
		case "plots":
			merged.Plots = updated.GetPlots()
		case "plot_layout", "plotLayout":
			merged.PlotLayout = updated.GetPlotLayout()
		default:
			return nil, fmt.Errorf("field %q is not updateable: %w", path, grpcerr.ErrInvalidArgument)
		}
	}
	return merged, nil
}

// buildPositionsMap encodes grid positions from the chart layout keyed by ItemId.
func buildPositionsMap(layout *dpb.LayoutSpec) (map[string][]byte, error) {
	if layout == nil {
		return nil, nil
	}
	positionsMap := make(map[string][]byte, len(layout.GetPositions()))
	for _, pos := range layout.GetPositions() {
		if pos == nil || pos.GetItemId() == "" {
			continue
		}
		posBytes, err := proto.Marshal(pos)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal grid position for %q: %w", pos.GetItemId(), err)
		}
		positionsMap[pos.GetItemId()] = posBytes
	}
	return positionsMap, nil
}

// buildDashboardChartMutations generates Spanner mutations for child DashboardCharts of a dashboard.
func buildDashboardChartMutations(dashID string, dashboard *dpb.Dashboard) ([]*spanner.Mutation, error) {
	positionsMap, err := buildPositionsMap(dashboard.GetChartLayout())
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool, len(dashboard.GetChartIds()))
	var mutations []*spanner.Mutation
	for _, rawID := range dashboard.GetChartIds() {
		chartID, err := validator.ValidateChartID(rawID)
		if err != nil {
			return nil, fmt.Errorf("invalid chart ID %q: %w", rawID, err)
		}
		if seen[chartID] {
			continue
		}
		seen[chartID] = true

		gridPos := positionsMap[chartID]

		mChart := spanner.InsertOrUpdate(dashboardschema.DashboardChartsTableName, []string{
			"DashboardId", "DashboardChartId", "ChartId", "GridPosition", "CreatedAt",
		}, []interface{}{
			dashID, chartID, chartID, gridPos, spanner.CommitTimestamp,
		})
		mutations = append(mutations, mChart)
	}

	return mutations, nil
}

// DashboardProtoToInsertMutations creates Spanner mutations to insert a Dashboard and its child DashboardCharts.
func DashboardProtoToInsertMutations(dashboard *dpb.Dashboard) ([]*spanner.Mutation, error) {
	if dashboard == nil {
		return nil, fmt.Errorf("dashboard proto must not be nil: %w", grpcerr.ErrInvalidArgument)
	}

	dashID, err := validator.ValidateDashboardName(dashboard.GetName())
	if err != nil {
		return nil, fmt.Errorf("invalid dashboard name: %w", err)
	}

	var layoutBytes []byte
	if dashboard.ChartLayout != nil {
		layoutBytes, err = proto.Marshal(dashboard.ChartLayout)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal chart layout: %w", err)
		}
	}

	m := spanner.InsertOrUpdate(dashboardschema.DashboardsTableName, []string{
		"DashboardId", "Title", "Description", "Owner", "ChartLayout", "CreatedAt", "UpdatedAt",
	}, []interface{}{
		dashID, dashboard.Title, dashboard.Description, dashboard.Owner, layoutBytes, spanner.CommitTimestamp, spanner.CommitTimestamp,
	})

	chartMutations, err := buildDashboardChartMutations(dashID, dashboard)
	if err != nil {
		return nil, err
	}

	return append([]*spanner.Mutation{m}, chartMutations...), nil
}

// DashboardWithChartsToInsertMutations creates the Spanner mutations that persist a Dashboard, the
// Charts created alongside it and the links between them.
//
// The charts share the dashboard's mutation set so that they are committed atomically:
// DashboardCharts.ChartId is a foreign key onto Charts, which Spanner validates at commit time, so
// links to charts written by the same transaction are only ever valid when both land together.
func DashboardWithChartsToInsertMutations(dashboard *dpb.Dashboard, charts []*dpb.Chart) ([]*spanner.Mutation, error) {
	var mutations []*spanner.Mutation
	for _, chart := range charts {
		chartMutations, err := ChartProtoToInsertMutations(chart)
		if err != nil {
			return nil, fmt.Errorf("invalid chart %q: %w", chart.GetName(), err)
		}
		mutations = append(mutations, chartMutations...)
	}

	dashboardMutations, err := DashboardProtoToInsertMutations(dashboard)
	if err != nil {
		return nil, err
	}
	return append(mutations, dashboardMutations...), nil
}

// DashboardProtoToUpdateMutations creates Spanner mutations to update a Dashboard and optionally its child DashboardCharts.
func DashboardProtoToUpdateMutations(dashboard *dpb.Dashboard, updateChartIDs bool) ([]*spanner.Mutation, error) {
	if dashboard == nil {
		return nil, fmt.Errorf("dashboard proto must not be nil: %w", grpcerr.ErrInvalidArgument)
	}
	if dashboard.GetName() == "" {
		return nil, fmt.Errorf("dashboard name must not be empty: %w", grpcerr.ErrInvalidArgument)
	}

	dashID, err := validator.ValidateDashboardName(dashboard.GetName())
	if err != nil {
		return nil, fmt.Errorf("invalid dashboard name: %w", err)
	}

	var layoutBytes []byte
	if dashboard.ChartLayout != nil {
		layoutBytes, err = proto.Marshal(dashboard.ChartLayout)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal chart layout: %w", err)
		}
	}

	m := spanner.UpdateMap(dashboardschema.DashboardsTableName, map[string]interface{}{
		"DashboardId": dashID,
		"Title":       dashboard.GetTitle(),
		"Description": dashboard.GetDescription(),
		"ChartLayout": layoutBytes,
		"UpdatedAt":   spanner.CommitTimestamp,
	})
	mutations := []*spanner.Mutation{m}

	if updateChartIDs {
		mDel := spanner.Delete(dashboardschema.DashboardChartsTableName, spanner.Key{dashID}.AsPrefix())
		chartMutations, err := buildDashboardChartMutations(dashID, dashboard)
		if err != nil {
			return nil, err
		}
		mutations = append(mutations, mDel)
		mutations = append(mutations, chartMutations...)
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
	ID          string    `spanner:"ChartId"`
	Title       string    `spanner:"Title"`
	Description string    `spanner:"Description"`
	Xid         int64     `spanner:"Xid"`
	Wid         int64     `spanner:"Wid"`
	PlotLayout  []byte    `spanner:"PlotLayout"`
	Owner       string    `spanner:"Owner"`
	CreatedAt   time.Time `spanner:"CreatedAt"`
	UpdatedAt   time.Time `spanner:"UpdatedAt"`
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
func DashboardRowToProto(row *spanner.Row, chartIDs []string) (*dpb.Dashboard, error) {
	if row == nil {
		return nil, fmt.Errorf("spanner row must not be nil")
	}

	var data dashboardRow
	if err := row.ToStructLenient(&data); err != nil {
		return nil, fmt.Errorf("failed to decode dashboard row: %w", err)
	}

	// Safely extract fields that may be aliased differently by the query generator (e.g., lowercase/snake_case).
	var dashID string
	if err := row.ColumnByName("DashboardId", &dashID); err == nil {
		data.ID = dashID
	} else if err := row.ColumnByName("dashboard_id", &dashID); err == nil {
		data.ID = dashID
	}

	var chartLayout []byte
	if err := row.ColumnByName("ChartLayout", &chartLayout); err == nil {
		data.ChartLayout = chartLayout
	} else if err := row.ColumnByName("chart_layout", &chartLayout); err == nil {
		data.ChartLayout = chartLayout
	}

	var createdAt time.Time
	if err := row.ColumnByName("CreatedAt", &createdAt); err == nil {
		data.CreatedAt = createdAt
	} else if err := row.ColumnByName("created", &createdAt); err == nil {
		data.CreatedAt = createdAt
	}

	var updatedAt time.Time
	if err := row.ColumnByName("UpdatedAt", &updatedAt); err == nil {
		data.UpdatedAt = updatedAt
	} else if err := row.ColumnByName("updated", &updatedAt); err == nil {
		data.UpdatedAt = updatedAt
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
		ChartIds:    SortChartIDs(chartIDs),
		ChartLayout: layout,
		CreateTime:  timestamppb.New(data.CreatedAt),
		UpdateTime:  timestamppb.New(data.UpdatedAt),
	}, nil
}

// ChartProtoToInsertMutations creates Spanner mutations to insert a Chart and its PlotSpecs.
func ChartProtoToInsertMutations(chart *dpb.Chart) ([]*spanner.Mutation, error) {
	if chart == nil {
		return nil, fmt.Errorf("chart proto must not be nil")
	}

	xid, wid, chartID, err := validator.ValidateChartName(chart.GetName())
	if err != nil {
		return nil, fmt.Errorf("invalid chart name %q: %w", chart.GetName(), err)
	}

	chartXid := chart.GetXid()
	if chartXid == 0 && xid > 0 {
		chartXid = xid
	}
	chartWid := chart.GetWid()
	if chartWid == 0 && wid > 0 {
		chartWid = wid
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
		"ChartId", "Title", "Description", "Xid", "Wid", "PlotLayout", "Owner", "CreatedAt", "UpdatedAt",
	}, []interface{}{
		chartID, chart.GetTitle(), chart.GetDescription(), chartXid, chartWid, layoutBytes, "", spanner.CommitTimestamp, spanner.CommitTimestamp,
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
			chartID, plot.PlotId, plot.Title, int64(plot.Type), plotSpecBytes,
		})
		mutations = append(mutations, mPlot)
	}

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

	// Safely extract fields that may be aliased differently by the query generator (e.g., lowercase/snake_case).
	var chartID string
	if err := row.ColumnByName("ChartId", &chartID); err == nil {
		data.ID = chartID
	} else if err := row.ColumnByName("chart_id", &chartID); err == nil {
		data.ID = chartID
	}

	var plotLayout []byte
	if err := row.ColumnByName("PlotLayout", &plotLayout); err == nil {
		data.PlotLayout = plotLayout
	} else if err := row.ColumnByName("plot_layout", &plotLayout); err == nil {
		data.PlotLayout = plotLayout
	}

	var createdAt time.Time
	if err := row.ColumnByName("CreatedAt", &createdAt); err == nil {
		data.CreatedAt = createdAt
	} else if err := row.ColumnByName("created", &createdAt); err == nil {
		data.CreatedAt = createdAt
	}

	var updatedAt time.Time
	if err := row.ColumnByName("UpdatedAt", &updatedAt); err == nil {
		data.UpdatedAt = updatedAt
	} else if err := row.ColumnByName("updated", &updatedAt); err == nil {
		data.UpdatedAt = updatedAt
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

	name := validator.ConstructRootChartName(data.ID)
	if data.Xid > 0 {
		name = validator.ConstructWorkUnitChartName(data.Xid, data.Wid, data.ID)
	}

	return &dpb.Chart{
		Name:        name,
		Title:       data.Title,
		Description: data.Description,
		Xid:         data.Xid,
		Wid:         data.Wid,
		Plots:       plots,
		PlotLayout:  layout,
		CreateTime:  timestamppb.New(data.CreatedAt),
		UpdateTime:  timestamppb.New(data.UpdatedAt),
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

	_, _, chartID, err := validator.ValidateChartName(chart.GetName())
	if err != nil {
		return nil, fmt.Errorf("invalid chart name %q: %w", chart.GetName(), err)
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
		"ChartId", "Title", "Description", "Wid", "PlotLayout", "UpdatedAt",
	}, []interface{}{
		chartID, chart.GetTitle(), chart.GetDescription(), chart.GetWid(), layoutBytes, spanner.CommitTimestamp,
	}), nil
}

// UnmarshalPlotSpec decodes a stored PlotSpec, naming the chart it belongs to when it cannot.
func UnmarshalPlotSpec(chartID string, plotSpecBytes []byte) (*dpb.PlotSpec, error) {
	var plotSpec dpb.PlotSpec
	if err := proto.Unmarshal(plotSpecBytes, &plotSpec); err != nil {
		return nil, fmt.Errorf("failed to unmarshal plot spec for chart %q: %w", chartID, err)
	}
	return &plotSpec, nil
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
	_, _, chartID, err := validator.ValidateChartName(chart.GetName())
	if err != nil {
		return nil, fmt.Errorf("invalid chart name %q: %w", chart.GetName(), err)
	}
	deleteMut := ChartPlotsToDeleteMutation(chartID)
	insertMuts, err := PlotSpecsToInsertMutations(chartID, chart.GetPlots())
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
