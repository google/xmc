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

-- Cloud Spanner requires the parent message in the bundle before a nested type can be added (the
-- emulator does not enforce this). Same pattern as WorkUnit / WorkUnit.WorkUnitType.
ALTER PROTO BUNDLE INSERT (
  cloud.xmanager.v1.Experiment,
  cloud.xmanager.v1.Experiment.ExperimentType
);

-- Experiment.ExperimentType. NULL (rows written before this column existed) reads and filters as
-- STANDARD. The column has no DEFAULT because the Spanner emulator re-applies DEFAULT values to
-- columns omitted from Update mutations on tables with a generated primary key, which would reset
-- the type on every update.
ALTER TABLE Experiments ADD COLUMN ExperimentType cloud.xmanager.v1.Experiment.ExperimentType;
