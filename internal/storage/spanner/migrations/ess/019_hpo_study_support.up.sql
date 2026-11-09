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

-- Hyperparameter optimization study definitions. Nested types are bundled with their parent
-- messages.
ALTER PROTO BUNDLE INSERT (
  cloud.xmanager.v1.StudyConfig,
  cloud.xmanager.v1.MetricSpec,
  cloud.xmanager.v1.MetricSpec.GoalType,
  cloud.xmanager.v1.ParameterSpec,
  cloud.xmanager.v1.ParameterSpec.ScaleType,
  cloud.xmanager.v1.DoubleValueSpec,
  cloud.xmanager.v1.IntegerValueSpec,
  cloud.xmanager.v1.CategoricalValueSpec,
  cloud.xmanager.v1.DiscreteValueSpec
);

-- Experiment has been bundled since 018; refresh its bundled descriptor so that it includes the new
-- study_config field.
ALTER PROTO BUNDLE UPDATE (cloud.xmanager.v1.Experiment);

-- Experiment.study_config. NULL for experiments that are not studies, including all rows written
-- before this column existed.
ALTER TABLE Experiments ADD COLUMN StudyConfig cloud.xmanager.v1.StudyConfig;
