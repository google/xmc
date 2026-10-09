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
 * @fileoverview Dashboard domain models, runtime constants, and Protobuf specifications.
 *
 * Architectural Structure:
 * 1. Canonical Protobuf Spec & RPC Aliases: Reuses generated Protobuf interfaces
 *    from `app/protos/dashboard_service` as the single source of truth without
 *    duplicate declarations.
 * 2. Runtime Enums / Constants (`as const`): Defines string-backed mapping objects
 *    to prevent schema drift and seamlessly match Protobuf JSON serialization.
 * 3. Client-Augmented Telemetry View Models: Defines UI view-models (`ChartData`,
 *    `PlotData`, `SummaryBoxValue`, `SeriesData`) containing client-computed analytics
 *    derived exclusively during data normalization.
 */

import type {
  Chart as ProtoChart,
  ChartData as ProtoChartData,
  CoordSpec as ProtoCoordSpec,
  CreateChartRequest as ProtoCreateChartRequest,
  CreateDashboardRequest as ProtoCreateDashboardRequest,
  Dashboard as ProtoDashboard,
  DataSpec as ProtoDataSpec,
  DeleteChartRequest as ProtoDeleteChartRequest,
  DeleteDashboardRequest as ProtoDeleteDashboardRequest,
  FacetSpec as ProtoFacetSpec,
  GeomSpec as ProtoGeomSpec,
  GetChartDataRequest as ProtoGetChartDataRequest,
  GetChartRequest as ProtoGetChartRequest,
  GetDashboardRequest as ProtoGetDashboardRequest,
  GridPosition as ProtoGridPosition,
  LayoutSpec as ProtoLayoutSpec,
  ListChartsRequest as ProtoListChartsRequest,
  ListChartsResponse as ProtoListChartsResponse,
  ListDashboardsRequest as ProtoListDashboardsRequest,
  ListDashboardsResponse as ProtoListDashboardsResponse,
  MappingSpec as ProtoMappingSpec,
  PlotSpec as ProtoPlotSpec,
  StatSpec as ProtoStatSpec,
  UpdateChartRequest as ProtoUpdateChartRequest,
  UpdateDashboardRequest as ProtoUpdateDashboardRequest,
} from 'app/protos/dashboard_service';

// ============================================================================
// 1. Canonical Protobuf Spec & RPC Aliases (Single Source of Truth)
// ============================================================================

// Domain Configuration & Specification Entities
export type Dashboard = ProtoDashboard;
export type Chart = ProtoChart;
export type PlotSpec = ProtoPlotSpec;

// Wire Telemetry Data Entities
export type SeriesPoint = ProtoChartData.SeriesPoint;
export type BarData = ProtoChartData.BarData;
export type BarBucket = ProtoChartData.BarBucket;

// Wire Spec Configurations
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

// Service RPC Request & Response Interfaces
export type CreateDashboardRequest = ProtoCreateDashboardRequest;
export type UpdateDashboardRequest = ProtoUpdateDashboardRequest;
export type ListDashboardsRequest = ProtoListDashboardsRequest;
export type ListDashboardsResponse = ProtoListDashboardsResponse;
export type CreateChartRequest = ProtoCreateChartRequest;
export type ListChartsRequest = ProtoListChartsRequest;
export type ListChartsResponse = ProtoListChartsResponse;
export type GetChartDataRequest = ProtoGetChartDataRequest;

// Consolidated Legacy DTO & RPC Aliases
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

// ============================================================================
// 2. Runtime Enums & Constants (as const String Mappings)
// ============================================================================

/**
 * Plot types matching canonical messages.proto string serialization names.
 */
export const PlotType = {
  PLOT_TYPE_UNSPECIFIED: 'PLOT_TYPE_UNSPECIFIED',
  UNSPECIFIED: 'PLOT_TYPE_UNSPECIFIED',
  SUMMARY_BOX: 'SUMMARY_BOX',
  SCATTER: 'SCATTER',
  BAR: 'BAR',
  HISTOGRAM: 'HISTOGRAM',
} as const;
export type PlotType = (typeof PlotType)[keyof typeof PlotType] | number;

/**
 * Layout types matching canonical messages.proto string serialization names.
 */
export const LayoutType = {
  LAYOUT_TYPE_UNSPECIFIED: 'LAYOUT_TYPE_UNSPECIFIED',
  UNSPECIFIED: 'LAYOUT_TYPE_UNSPECIFIED',
  GRID: 'GRID',
  WRAP: 'WRAP',
  SIDE_BY_SIDE: 'SIDE_BY_SIDE',
  STACKED: 'STACKED',
} as const;
export type LayoutType = (typeof LayoutType)[keyof typeof LayoutType] | number;

/**
 * Geometry types matching canonical messages.proto string serialization names.
 */
export const GeomType = {
  GEOM_TYPE_UNSPECIFIED: 'GEOM_TYPE_UNSPECIFIED',
  UNSPECIFIED: 'GEOM_TYPE_UNSPECIFIED',
  LINE: 'LINE',
  POINT: 'POINT',
  BAR: 'BAR',
  AREA: 'AREA',
  RIBBON: 'RIBBON',
  HLINE: 'HLINE',
  VLINE: 'VLINE',
} as const;
export type GeomType = (typeof GeomType)[keyof typeof GeomType] | number;

/**
 * Metric polarity indicating whether lower or higher values represent improvement.
 */
export const MetricPolarity = {
  LOWER_IS_BETTER: 'LOWER_IS_BETTER',
  HIGHER_IS_BETTER: 'HIGHER_IS_BETTER',
  NEUTRAL: 'NEUTRAL',
} as const;
export type MetricPolarity = (typeof MetricPolarity)[keyof typeof MetricPolarity];

// ============================================================================
// 3. Client-Augmented Telemetry View Models
// ============================================================================

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
 * Client-side view model for plot telemetry data.
 *
 * Why `PlotData` is defined here rather than directly aliasing `ProtoChartData.PlotData`
 * from `messages.proto`:
 * 1. **Client-Augmented Child Models**: In `messages.proto`, `summary_data` and `scatter_series`
 *    are raw wire messages without UI analytics. Our client view-model `PlotData` strongly
 *    types `summaryData?: SummaryBoxValue` and `scatterSeries?: SeriesData` with the client
 *    view models that contain UI-computed analytics (`deltaVsBaseline`, `polarity`,
 *    `isBaseline`, `seriesLabel`).
 * 2. **Multi-Series Overlay (`seriesList`)**: The canonical `messages.proto` `PlotData` defines
 *    a single `scatter_series`. The client view model adds `seriesList?: SeriesData[]` to support
 *    multi-run overlay comparisons (e.g., Run 1 vs Run 2 vs Baseline on the same chart).
 * 3. **Flexible Type Unification (`PlotType | string | number`)**: Unifies wire JSON strings
 *    (`'SCATTER'`), integer tags (`2`), and TypeScript `PlotType` constants.
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
