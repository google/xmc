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
 * @fileoverview Dashboard domain models, enums, and Protobuf specifications.
 *
 * Architectural Boundary (Protobuf Specs vs Client Data View-Models):
 * - Specification & Configuration Entities (`Dashboard`, `Chart`, `PlotSpec`, `LayoutSpec`,
 *   `DataSpec`, `GeomSpec`, `CoordSpec`, RPC request/response messages) directly reuse
 *   canonical generated Protobuf declarations from `messages.jsonpb_decls` and
 *   `dashboard_service.jsonpb_decls` as the single source of truth without duplication.
 * - Client Data View Models (`ChartData`, `PlotData`, `SummaryBoxValue`, `SeriesData`,
 *   `BarData`) represent normalized telemetry data containers. They provide:
 *   1. Clean camelCase property access for UI components.
 *   2. Client-computed analytics fields (e.g. delta vs baseline, polarity, series labels)
 *      derived exclusively during UI data normalization.
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
// 1. Root Specification Entities (Direct Protobuf Reuse)
// ============================================================================

/**
 * Performance Dashboard configuration specification.
 * Directly reused from canonical messages.proto (ProtoDashboard).
 */
export type Dashboard = ProtoDashboard;

/**
 * Chart container configuration specification.
 * Directly reused from canonical messages.proto (ProtoChart).
 */
export type Chart = ProtoChart;

/**
 * Plot specification defining metrics, geometries, and statistical transforms.
 * Directly reused from canonical messages.proto (ProtoPlotSpec).
 */
export type PlotSpec = ProtoPlotSpec;

// ============================================================================
// 2. Client-Side Telemetry Data View Models (Top-Down Hierarchy)
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
 *    are raw proto messages without UI analytics. Our client view-model `PlotData` strongly
 *    types `summaryData?: SummaryBoxValue` and `scatterSeries?: SeriesData` with the client
 *    view models that contain UI-computed analytics (`deltaVsBaseline`, `polarity`,
 *    `isBaseline`, `seriesLabel`).
 * 2. **Multi-Series Overlay (`seriesList`)**: The canonical `messages.proto` `PlotData` defines
 *    a single `scatter_series`. The client view model adds `seriesList?: SeriesData[]` to support
 *    multi-run overlay comparisons (e.g., Run 1 vs Run 2 vs Baseline on the same chart).
 * 3. **Flexible Type Unification (`PlotType | string | number`)**: Unifies wire JSON strings
 *    (`'SCATTER'`), integer tags (`2`), and TypeScript `PlotType` enum values.
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
 * Coordinate point in a telemetry series curve.
 * Directly reused from canonical messages.proto (ProtoChartData.SeriesPoint).
 */
export type SeriesPoint = ProtoChartData.SeriesPoint;

/**
 * Categorical bar chart or histogram distribution data.
 * Directly reused from canonical messages.proto (ProtoChartData.BarData).
 */
export type BarData = ProtoChartData.BarData;

/**
 * Single category or bin bucket in a bar or histogram distribution.
 * Directly reused from canonical messages.proto (ProtoChartData.BarBucket).
 */
export type BarBucket = ProtoChartData.BarBucket;

// ============================================================================
// 3. Canonical Enums
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
// 4. Service Request & Response Interfaces (Direct Protobuf Reuse)
// ============================================================================

export type CreateDashboardRequest = ProtoCreateDashboardRequest;
export type UpdateDashboardRequest = ProtoUpdateDashboardRequest;
export type ListDashboardsRequest = ProtoListDashboardsRequest;
export type ListDashboardsResponse = ProtoListDashboardsResponse;
export type CreateChartRequest = ProtoCreateChartRequest;
export type GetChartDataRequest = ProtoGetChartDataRequest;

// ============================================================================
// 5. Wire & Protobuf Spec Aliases and DTO Re-exports
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
