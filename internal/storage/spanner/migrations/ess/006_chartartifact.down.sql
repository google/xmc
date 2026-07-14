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

ALTER PROTO BUNDLE DELETE (
  cloud.xmanager.v1.PlotType,
  cloud.xmanager.v1.ChartArtifact,
  cloud.xmanager.v1.ChartArtifact.Status,
  cloud.xmanager.v1.Chart,
  cloud.xmanager.v1.PlotSpec,
  cloud.xmanager.v1.LayoutSpec,
  cloud.xmanager.v1.LayoutSpec.LayoutType,
  cloud.xmanager.v1.GridPosition,
  cloud.xmanager.v1.DataSpec,
  cloud.xmanager.v1.DataSpec.DataRange,
  cloud.xmanager.v1.MappingSpec,
  cloud.xmanager.v1.GeomSpec,
  cloud.xmanager.v1.GeomSpec.GeomType,
  cloud.xmanager.v1.GeomSpec.StyleSpec,
  cloud.xmanager.v1.StatSpec,
  cloud.xmanager.v1.StatSpec.StatType,
  cloud.xmanager.v1.StatSpec.Aggregation,
  cloud.xmanager.v1.StatSpec.SmoothConfig,
  cloud.xmanager.v1.StatSpec.SmoothConfig.Algorithm,
  cloud.xmanager.v1.StatSpec.ForecastConfig,
  cloud.xmanager.v1.CoordSpec,
  cloud.xmanager.v1.CoordSpec.ScaleType,
  cloud.xmanager.v1.FacetSpec,
  cloud.xmanager.v1.FacetSpec.FacetType
);
