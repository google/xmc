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

/**
 * @fileoverview Dashboard models and view-model interfaces.
 * Reuses generated Protobuf interfaces from messages.proto and
 * dashboard_service.proto for wire DTOs and provides client-side view models.
 */

import type {
  CreateChartRequest as ProtoCreateChartRequest,
  CreateDashboardRequest as ProtoCreateDashboardRequest,
  DeleteChartRequest as ProtoDeleteChartRequest,
  DeleteDashboardRequest as ProtoDeleteDashboardRequest,
  GetChartDataRequest as ProtoGetChartDataRequest,
  GetChartRequest as ProtoGetChartRequest,
  GetDashboardRequest as ProtoGetDashboardRequest,
  ListChartsRequest as ProtoListChartsRequest,
  ListChartsResponse as ProtoListChartsResponse,
  ListDashboardsRequest as ProtoListDashboardsRequest,
  ListDashboardsResponse as ProtoListDashboardsResponse,
  UpdateChartRequest as ProtoUpdateChartRequest,
  UpdateDashboardRequest as ProtoUpdateDashboardRequest,
} from 'xmanager_cloud/dashboard_service/proto/dashboard_service.jsonpb_decls';
import type {
  Chart as ProtoChart,
  ChartData as ProtoChartData,
  CoordSpec as ProtoCoordSpec,
  Dashboard as ProtoDashboard,
  DataSpec as ProtoDataSpec,
  FacetSpec as ProtoFacetSpec,
  GeomSpec as ProtoGeomSpec,
  GridPosition as ProtoGridPosition,
  LayoutSpec as ProtoLayoutSpec,
  MappingSpec as ProtoMappingSpec,
  PlotSpec as ProtoPlotSpec,
  StatSpec as ProtoStatSpec,
} from 'xmanager_cloud/dashboard_service/proto/messages.jsonpb_decls';

/**
 * Canonical plot types matching api/protos/dashboard/messages.proto PlotType enum.
 */
export enum PlotType {
  PLOT_TYPE_UNSPECIFIED = 0,
  SUMMARY_BOX = 1,
  SCATTER = 2,
  BAR = 3,
  HISTOGRAM = 4,
}

/**
 * Canonical layout types matching api/protos/dashboard/messages.proto LayoutType enum.
 */
export enum LayoutType {
  LAYOUT_TYPE_UNSPECIFIED = 0,
  GRID = 1,
  WRAP = 2,
  SIDE_BY_SIDE = 3,
  STACKED = 4,
}

/**
 * Canonical geometry types matching api/protos/dashboard/messages.proto GeomType enum.
 */
export enum GeomType {
  GEOM_TYPE_UNSPECIFIED = 0,
  LINE = 1,
  POINT = 2,
  BAR = 3,
  AREA = 4,
  RIBBON = 5,
  HLINE = 6,
  VLINE = 7,
}

/**
 * Metric polarity indicating whether lower or higher values represent improvement.
 */
export enum MetricPolarity {
  LOWER_IS_BETTER = 'LOWER_IS_BETTER',
  HIGHER_IS_BETTER = 'HIGHER_IS_BETTER',
  NEUTRAL = 'NEUTRAL',
}

// Wire Protobuf RPC request/response aliases
export type {
  ProtoCreateChartRequest,
  ProtoCreateDashboardRequest,
  ProtoDeleteChartRequest,
  ProtoDeleteDashboardRequest,
  ProtoGetChartDataRequest,
  ProtoGetChartRequest,
  ProtoGetDashboardRequest,
  ProtoListChartsRequest,
  ProtoListChartsResponse,
  ProtoListDashboardsRequest,
  ProtoListDashboardsResponse,
  ProtoUpdateChartRequest,
  ProtoUpdateDashboardRequest,
};

// Client-Side Request / Response Interfaces
export interface CreateDashboardRequest {
  dashboard: Dashboard;
  dashboardId?: string;
}

export interface UpdateDashboardRequest {
  dashboard: Dashboard;
  updateMask?: string;
}

export interface ListDashboardsRequest {
  pageSize?: number;
  pageToken?: string;
  filter?: string;
}

export interface ListDashboardsResponse {
  dashboards?: Dashboard[];
  nextPageToken?: string;
}

export interface CreateChartRequest {
  parent: string;
  chart: Chart;
  chartId?: string;
}

export interface GetChartDataRequest {
  name: string;
  parentExperiment?: string;
  parentWorkunit?: string;
  chartWidthPixels?: number;
}

// Wire Protobuf DTO aliases matching canonical messages.proto
export type RawDashboard = ProtoDashboard;
export type RawChart = ProtoChart;
export type RawPlotSpec = ProtoPlotSpec;
export type RawChartData = ProtoChartData;
export type RawPlotData = ProtoChartData.PlotData;
export type RawSeriesData = ProtoChartData.SeriesData;
export type RawSeriesPoint = ProtoChartData.SeriesPoint;
export type RawBarData = ProtoChartData.BarData;
export type RawBarBucket = ProtoChartData.BarBucket;
export type RawSummaryBoxValue = ProtoChartData.SummaryBoxValue;

// Wire Spec Type Aliases matching canonical messages.proto
export type GridPosition = ProtoGridPosition;
export type LayoutSpec = ProtoLayoutSpec;
export type DataRange = ProtoDataSpec.DataRange;
export type DataSpec = ProtoDataSpec;
export type MappingSpec = ProtoMappingSpec;
export type StyleSpec = ProtoGeomSpec.StyleSpec;
export type GeomSpec = ProtoGeomSpec;
export type SmoothConfig = ProtoStatSpec.SmoothConfig;
export type ForecastConfig = ProtoStatSpec.ForecastConfig;
export type StatSpec = ProtoStatSpec;
export type CoordSpec = ProtoCoordSpec;
export type FacetSpec = ProtoFacetSpec;

// ---------------------------------------------------------------------------
// Client-Side View Models & Domain Types
// ---------------------------------------------------------------------------

export interface SummaryBoxValue {
  metricName?: string;
  latestValue?: number;
  minValue?: number;
  maxValue?: number;
  meanValue?: number;
  latestStep?: number | string;
  latestIteration?: number | string;
  latestTimestamp?: string;
  deltaVsBaseline?: number;
  deltaPercentVsBaseline?: number;
  baselineValue?: number;
  polarity?: MetricPolarity;
}

export interface SeriesPoint {
  step?: number | string;
  iteration?: number | string;
  timestamp?: string;
  avgValue?: number;
  minValue?: number;
  maxValue?: number;
}

export interface SeriesData {
  metricName?: string;
  workUnitId?: string;
  xid?: number | string;
  points?: SeriesPoint[];
  seriesLabel?: string;
  isBaseline?: boolean;
}

export interface BarBucket {
  categoryOrBin?: string;
  valueOrCount?: number;
}

export interface BarData {
  metricName?: string;
  buckets?: BarBucket[];
}

export interface PlotData {
  plotId?: string;
  type?: PlotType | string | number;
  summaryData?: SummaryBoxValue;
  scatterSeries?: SeriesData;
  barData?: BarData;
  seriesList?: SeriesData[];
  content?: {
    summaryData?: SummaryBoxValue;
    scatterSeries?: SeriesData;
    barData?: BarData;
  };
}

export interface ChartData {
  name?: string;
  xid?: number | string;
  wid?: number | string;
  plotsData?: PlotData[];
}

export interface PlotSpec {
  plotId: string;
  title?: string;
  type: PlotType | string | number;
  data: DataSpec;
  mapping?: MappingSpec;
  geoms?: GeomSpec[];
  stats?: StatSpec;
  coords?: CoordSpec;
  facets?: FacetSpec;
}

export interface Chart {
  name?: string;
  title: string;
  xid: number;
  wid?: number;
  plots: PlotSpec[];
  description?: string;
  plotLayout?: LayoutSpec;
  createTime?: string;
  updateTime?: string;
}

export interface Dashboard {
  name?: string;
  title: string;
  description?: string;
  owner?: string;
  charts?: Chart[];
  chartLayout?: LayoutSpec;
  createTime?: string;
  updateTime?: string;
}
