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

ALTER PROTO BUNDLE INSERT (
  cloud.xmanager.v1.QueueInfo
);

-- PositionInQueue represents the work unit's position in the queue for ROD sorting.
-- JSON_VALUE(CAST(WorkUnitStatus AS STRING), ...) is used to safely extract position_in_queue
-- without triggering C++ pointer assertion crashes in Spanner Emulator (evaluated_column.cc:122)
-- when scheduling_information or auxiliary_info is unpopulated.
-- Sentinel fallback: max INT64 (2^63 - 1) ensures unpositioned jobs sort last in ascending queue order.
ALTER TABLE WorkUnits ADD COLUMN PositionInQueue INT64 AS (
  IFNULL(
    SAFE_CAST(
      JSON_VALUE(
        CAST(WorkUnitStatus AS STRING),
        '$.scheduling_information.auxiliary_info.queue_info.position_in_queue'
      ) AS INT64
    ),
    9223372036854775807
  )
) STORED;

ALTER TABLE WorkUnits ADD COLUMN IsPendingOrRunningJob BOOL AS (
  IF(ExecutionState IN ('SCHEDULING', 'STARTING', 'RUNNING', 'INITIALIZING'), TRUE, NULL)
) STORED;

-- SortPhase execution ordering tier:
--   1: Active execution states (RUNNING, STARTING, INITIALIZING) -> Highest priority
--   0: Waiting for allocation (SCHEDULING)
--  -1: Terminal / inactive execution states -> Lowest priority
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
  IsPendingOrRunningJob
) WHERE IsPendingOrRunningJob IS NOT NULL;
