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

-- Table: Components
CREATE TABLE Components (
  ComponentId INT64 NOT NULL,
  Name STRING(MAX) NOT NULL,
  ConfigProto BYTES(MAX) NOT NULL,
  AccessPayload BYTES(MAX),
  State INT64 NOT NULL,
  Generation INT64 NOT NULL,
  ObservedGeneration INT64,
  CreateTime TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = TRUE),
  UpdateTime TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = TRUE),
  ActiveName STRING(MAX) AS (IF(State != 5, Name, NULL)) STORED,
  CONSTRAINT CK_Components_State CHECK (State IN (1, 2, 3, 4, 5))
) PRIMARY KEY (ComponentId);

CREATE UNIQUE NULL_FILTERED INDEX Idx_Components_ActiveName ON Components(ActiveName);

-- Table: ComponentDependencies
CREATE TABLE ComponentDependencies (
  ComponentId INT64 NOT NULL,
  DependencyComponentId INT64 NOT NULL,
  CONSTRAINT FK_Dependency FOREIGN KEY (DependencyComponentId) REFERENCES Components(ComponentId)
) PRIMARY KEY (ComponentId, DependencyComponentId),
  INTERLEAVE IN PARENT Components ON DELETE CASCADE;

-- Table: ExperimentComponentSnapshots
CREATE TABLE ExperimentComponentSnapshots (
  BitReversedXid INT64 NOT NULL,
  ComponentId INT64 NOT NULL,
  LockedGeneration INT64 NOT NULL,
  ConfigProto BYTES(MAX) NOT NULL,
  AccessPayload BYTES(MAX),
  CONSTRAINT FK_Snapshots_Component FOREIGN KEY (ComponentId) REFERENCES Components(ComponentId)
) PRIMARY KEY (BitReversedXid, ComponentId),
  INTERLEAVE IN PARENT Experiments ON DELETE CASCADE;

CREATE INDEX Idx_Snapshots_Component ON ExperimentComponentSnapshots(ComponentId);

-- View: ExperimentConsistencyInfo
CREATE VIEW ExperimentConsistencyInfo SQL SECURITY INVOKER AS
SELECT
  e.BitReversedXid,
  e.Xid,
  NOT EXISTS (
    SELECT 1
    FROM ExperimentComponentSnapshots s
    LEFT JOIN Components c ON s.ComponentId = c.ComponentId
    WHERE s.BitReversedXid = e.BitReversedXid
      AND (c.ComponentId IS NULL OR c.State = 5 OR (c.ObservedGeneration IS NOT NULL AND s.LockedGeneration != c.ObservedGeneration))
  ) AS IsExperimentConsistent,
  EXISTS (
    SELECT 1
    FROM ExperimentComponentSnapshots s
    LEFT JOIN Components c ON s.ComponentId = c.ComponentId
    WHERE s.BitReversedXid = e.BitReversedXid
      AND c.ComponentId IS NOT NULL
      AND c.ObservedGeneration IS NULL
  ) AS HasPendingReconciliation
FROM Experiments e
WHERE e.ActiveState = 1;
