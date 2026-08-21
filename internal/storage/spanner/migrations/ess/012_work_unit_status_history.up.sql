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

-- WorkUnitStatusHistory tracks historical state transitions of work units for telemetry and queue velocity metrics.
CREATE TABLE WorkUnitStatusHistory (
  -- The associated experiment ID (XID).
  Xid INT64 NOT NULL,
  -- Bit-reversed XID for distribution across Spanner split boundaries.
  BitReversedXid INT64 NOT NULL AS (BIT_REVERSE(Xid, FALSE)) STORED,
  -- The associated work unit ID.
  WorkUnitId INT64 NOT NULL,
  -- Unique identifier for the status history entry.
  HistoryId INT64 NOT NULL,
  -- The associated work unit execution ID.
  ExecutionId INT64,
  -- The name of the queue assigned to the work unit. Should match the kueue ClusterQueue.
  QueueName STRING(MAX),
  -- The previous work unit state prior to transition.
  PreviousState cloud.xmanager.v1.WorkUnitStatus.WorkUnitState,
  -- The new work unit state after transition.
  NewState cloud.xmanager.v1.WorkUnitStatus.WorkUnitState NOT NULL,
  -- Compute resource type requested (e.g. nvidia-tesla-h100, TPU, CPU).
  ResourceType STRING(MAX),
  -- Quantity of compute resource units requested.
  ResourceUnits INT64,
  -- Timestamp of state transition.
  TransitionTime TIMESTAMP NOT NULL,
  -- Stored alias for query compatibility.
  CreateTime TIMESTAMP NOT NULL AS (TransitionTime) STORED,
  -- Expiration time for automatic row pruning via TTL policy (30 days).
  ExpirationTime TIMESTAMP AS (TIMESTAMP_ADD(TransitionTime, INTERVAL 30 DAY)) STORED,
) PRIMARY KEY (BitReversedXid, WorkUnitId, HistoryId),
  ROW DELETION POLICY (OLDER_THAN(ExpirationTime, INTERVAL 0 DAY));

-- Index for efficient sliding-window queue velocity and turnover rate queries.
CREATE INDEX WorkUnitStatusHistoryByQueueAndTransitionTime ON WorkUnitStatusHistory (QueueName, TransitionTime DESC, NewState, PreviousState) STORING (ResourceType, ResourceUnits);
