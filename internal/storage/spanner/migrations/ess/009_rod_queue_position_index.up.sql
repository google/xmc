-- Copyright 2026 Google LLC
--
-- Licensed under the Apache License, Version 2.0 (the "License");
-- you may not use this file except in compliance with the License.
-- You may obtain a copy of the License at
--
--     http://www.apache.org/licenses/LICENSE-2.0
--
-- Unless required by applicable law or agreed to in writing, software
-- distributed under the License is distributed on an "AS IS" BASIS,
-- WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
-- See the License for the specific language governing permissions and
-- limitations under the License.

-- PositionInQueue represents the work unit's position in the queue for ROD sorting.
ALTER TABLE WorkUnits ADD COLUMN PositionInQueue INT64;

ALTER TABLE WorkUnits ADD COLUMN IsPendingOrRunningJob BOOL AS (
  IF(ExecutionState IN ('SCHEDULING', 'STARTING', 'RUNNING', 'INITIALIZING'), TRUE, NULL)
) STORED;

ALTER TABLE WorkUnits ADD COLUMN SortPhase INT64 AS (
  CASE
    WHEN ExecutionState IN ('RUNNING', 'STARTING', 'INITIALIZING') THEN 1
    WHEN ExecutionState IN ('SCHEDULING') THEN 0
    ELSE -1
  END
) STORED;

CREATE INDEX WorkUnitsByQueueAndPosition 
ON WorkUnits (
  QueueName, 
  SortPhase DESC, 
  PositionInQueue ASC, 
  CreateTime ASC, 
  WorkUnitId ASC
) STORING (
  Priority, 
  ExecutionState, 
  Xid,
  IsPendingOrRunningJob
  -- TODO(#186): Add Creator to STORING clause when Creator denormalization is implemented.
) WHERE IsPendingOrRunningJob IS NOT NULL;
