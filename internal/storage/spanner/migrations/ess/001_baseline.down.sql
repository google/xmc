-- Copyright 2025 Google LLC
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
DROP TABLE WorkUnitExecutions;
DROP TABLE Operations;
DROP INDEX WorkUnitsByQueueName;
DROP TABLE WorkUnits;
DROP SEARCH INDEX ArtifactsSearchIndex;
DROP INDEX ArtifactsByBitReversedArtifactId;
DROP TABLE Artifacts;
DROP SEARCH INDEX ExperimentsSearchIndex;
DROP INDEX ExperimentsByCreator;
DROP TABLE Experiments;

DROP PROTO BUNDLE;
