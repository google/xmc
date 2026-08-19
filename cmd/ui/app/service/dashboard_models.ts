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
 * @fileoverview Dashboard domain view-models, enums, and Protobuf wire DTO aliases.
 *
 * Architectural Boundary (Client View Models vs Proto Wire DTOs):
 * - Wire DTOs and Spec types (e.g. `GridPosition`, `LayoutSpec`, `DataSpec`, `CoordSpec`,
 *   `GeomSpec`, etc.) directly reuse canonical generated Protobuf declarations from
 *   `messages.jsonpb_decls` to ensure a single source of truth without duplication.
 * - Client View Models (e.g. `Dashboard`, `Chart`, `PlotSpec`, `PlotData`, `SeriesData`,
 *   `SummaryBoxValue`) represent sanitized, UI-optimized domain entities. They provide:
 *   1. Strong typing and required properties where wire schemas permit optionals.
 *   2. Sanitized numeric IDs (`number` instead of int64 `string | number` wire DTOs).
 *   3. Client-computed analytics fields (e.g. delta vs baseline, polarity) derived
 *      exclusively during UI data normalization.
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

// ============================================================================
// 1. Root Domain View Models (Top-Down Hierarchy)
// ============================================================================

/**
 * Client-side domain view model for a Performance Dashboard.
 * Normalizes wire DTOs into guaranteed camelCase properties and typed chart arrays.
 */
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

/**
 * Client-side domain view model for a Chart container.
 * Converts raw int64 string/number IDs (`xid`, `wid`) into strongly-typed numbers.
 */
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

/**
 * Client-side domain view model for a Plot specification.
 * Reuses canonical Proto spec objects (`DataSpec`, `MappingSpec`, `GeomSpec`, etc.)
 * while enforcing required `plotId` and `data` properties.
 */
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

/**
 * Normalized container holding telemetry data payloads for all plots in a chart.
 */
export interface ChartData {
  name?: string;
  xid?: number | string;
  wid?: number | string;
  plotsData?: PlotData[];
}

/**
 * Normalized data payload for a specific plot, dispatching to scalar KPI summaries,
 * line/scatter series, or bar/histogram category distributions.
 */
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

/**
 * Client-side view model for scalar KPI summary box values.
 *
 * Provenance Note:
 * Fields `deltaVsBaseline`, `deltaPercentVsBaseline`, `baselineValue`, and `polarity`
 * do not exist in the canonical `messages.proto` wire schema. They represent
 * UI-computed analytics derived during client-side metrics normalization.
 */
export interface SummaryBoxValue {
  metricName?: string;
  latestValue?: number;
  minValue?: number;
  maxValue?: number;
  meanValue?: number;
  latestStep?: number | string;
  latestIteration?: number | string;
  latestTimestamp?: string;
  /** UI-computed absolute difference against baseline value. */
  deltaVsBaseline?: number;
  /** UI-computed percentage difference against baseline value. */
  deltaPercentVsBaseline?: number;
  /** UI-computed reference baseline value used for delta calculation. */
  baselineValue?: number;
  /** UI-computed metric polarity (higher vs lower is better). */
  polarity?: MetricPolarity;
}

/**
 * Client-side view model for line or scatter telemetry series data.
 *
 * Provenance Note:
 * Fields `isBaseline` and `seriesLabel` do not exist in the canonical wire schema.
 * They represent UI-computed display labels and baseline flags derived during normalization.
 */
export interface SeriesData {
  metricName?: string;
  workUnitId?: string;
  xid?: number | string;
  points?: SeriesPoint[];
  /** UI-computed display label for multi-series legend rendering. */
  seriesLabel?: string;
  /** UI-computed flag marking this series as a comparison baseline. */
  isBaseline?: boolean;
}

/**
 * Client-side view model representing a single coordinate point in a series.
 */
export interface SeriesPoint {
  step?: number | string;
  iteration?: number | string;
  timestamp?: string;
  avgValue?: number;
  minValue?: number;
  maxValue?: number;
}

/**
 * Client-side view model for categorical bar chart or histogram distributions.
 */
export interface BarData {
  metricName?: string;
  buckets?: BarBucket[];
}

/**
 * Client-side view model for a single bar or histogram bucket.
 */
export interface BarBucket {
  categoryOrBin?: string;
  valueOrCount?: number;
}

// ============================================================================
// 2. Canonical Enums
// ============================================================================

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

// ============================================================================
// 3. Service Request & Response Interfaces
// ============================================================================

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

// ============================================================================
// 4. Wire & Protobuf Spec Aliases and DTO Re-exports
// ============================================================================

// Directly reuse canonical generated Protobuf declarations from messages.jsonpb_decls
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
