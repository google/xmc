-- Copyright 2026 Google LLC
--
-- Licensed under the Apache License, Version 2.0 (the "License");
-- you may not use this file except in compliance with the License.
-- You may obtain a copy of the License at
--
--      http://www.apache.org/licenses/LICENSE-2.0
--
-- Unless required by applicable law or agreed to in writing, software
-- distributed under the License is distributed on an "AS IS" BASIS,
-- WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
-- See the License for the specific language governing permissions and
-- limitations under the License.

CREATE TABLE Dashboards (
  DashboardId STRING(MAX) NOT NULL,
  Title STRING(MAX) NOT NULL,
  Description STRING(MAX) NOT NULL,
  Owner STRING(MAX) NOT NULL,
  ChartLayout BYTES(MAX),
  CreatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = TRUE),
  UpdatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = TRUE),
) PRIMARY KEY (DashboardId);

CREATE TABLE Charts (
  ChartId STRING(MAX) NOT NULL,
  Title STRING(MAX) NOT NULL,
  Xid INT64 NOT NULL,
  PlotLayout BYTES(MAX),
  Owner STRING(MAX) NOT NULL,
  CreatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = TRUE),
  UpdatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = TRUE),
) PRIMARY KEY (ChartId);

CREATE TABLE DashboardCharts (
  DashboardId STRING(MAX) NOT NULL,
  DashboardChartId STRING(MAX) NOT NULL,
  ChartId STRING(MAX) NOT NULL,
  GridPosition BYTES(MAX),
  CreatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = TRUE),
  CONSTRAINT FK_DashboardChart_ChartId FOREIGN KEY (ChartId) REFERENCES Charts(ChartId)
) PRIMARY KEY (DashboardId, DashboardChartId),
  INTERLEAVE IN PARENT Dashboards ON DELETE CASCADE;

CREATE INDEX DashboardChartsByChartId ON DashboardCharts (ChartId);

CREATE TABLE ChartPlots (
  ChartId STRING(MAX) NOT NULL,
  PlotId STRING(MAX) NOT NULL,
  Title STRING(MAX) NOT NULL,
  PlotType INT64 NOT NULL,
  PlotSpec BYTES(MAX),
) PRIMARY KEY (ChartId, PlotId),
  INTERLEAVE IN PARENT Charts ON DELETE CASCADE;

CREATE TABLE ArtifactCharts (
  Xid INT64 NOT NULL,
  BitReversedXid INT64 NOT NULL AS (BIT_REVERSE(Xid, FALSE)) STORED,
  WorkUnitId INT64,
  ArtifactId INT64 NOT NULL,
  ArtifactChartId STRING(MAX) NOT NULL,
  ChartId STRING(MAX) NOT NULL,
  CreatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = TRUE),
  CONSTRAINT FK_ArtifactChart_ChartId FOREIGN KEY (ChartId) REFERENCES Charts(ChartId)
) PRIMARY KEY (BitReversedXid, WorkUnitId, ArtifactId, ArtifactChartId),
  INTERLEAVE IN PARENT Artifacts ON DELETE CASCADE;

CREATE INDEX ArtifactChartsByChartId ON ArtifactCharts (ChartId);

