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

DROP VIEW IF EXISTS ExperimentConsistencyInfo;

DROP TABLE IF EXISTS PausedTasks;
DROP TABLE IF EXISTS PauseSchedulingState;
DROP TABLE IF EXISTS WorkUnitStatusHistory;
DROP TABLE IF EXISTS ExperimentComponentSnapshots;
DROP TABLE IF EXISTS ComponentDependencies;
DROP TABLE IF EXISTS Components;
DROP TABLE IF EXISTS ArtifactCharts;
DROP TABLE IF EXISTS ChartPlots;
DROP TABLE IF EXISTS DashboardCharts;
DROP TABLE IF EXISTS Charts;
DROP TABLE IF EXISTS Dashboards;
DROP TABLE IF EXISTS StatusMessages;
DROP TABLE IF EXISTS WorkUnitExecutions;
DROP TABLE IF EXISTS Operations;
DROP TABLE IF EXISTS WorkUnits;
DROP TABLE IF EXISTS Artifacts;
DROP TABLE IF EXISTS Experiments;

DROP FUNCTION IF EXISTS immutable_array_to_string(TEXT[], TEXT);
