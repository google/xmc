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

-- Protos used in PausedTasks.
ALTER PROTO BUNDLE INSERT (
  cloud.xmanager.v1.Action,
  cloud.xmanager.v1.CreateAction,
  cloud.xmanager.v1.DeleteAction,
  cloud.xmanager.v1.RestartAction,
  cloud.xmanager.v1.UpdateAction,
);

-- Picks up the new SCHEDULING_PAUSED value; ENUM columns reject values missing from the stored bundle.
ALTER PROTO BUNDLE UPDATE (cloud.xmanager.v1.WorkUnitStatus.WorkUnitState);

-- Singleton table persisting the global scheduling pause toggle and resumption metadata.
CREATE TABLE PauseSchedulingState (
  -- Singleton key. Always 1.
  Id INT64 NOT NULL,
  -- Whether Actuator scheduling is currently paused.
  SchedulingPaused BOOL NOT NULL DEFAULT (FALSE),
  -- Identifies the last triggered resumption run. NULL while paused.
  ResumptionRunId STRING(64),
  -- Identifies the resumption worker currently holding the run. Used for fencing stale workers.
  ResumptionWorkerId STRING(64),
  -- Commit time of the last state change.
  UpdatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = TRUE),
  CONSTRAINT Check_Id_Is_One CHECK (Id = 1),
) PRIMARY KEY (Id);

-- Tasks intercepted by the Actuator while scheduling is paused. Interleaved with WorkUnits.
CREATE TABLE PausedTasks (
  -- The associated XID.
  Xid INT64 NOT NULL,
  -- The bit-reversed XID of the experiment for indexing.
  BitReversedXid INT64 NOT NULL AS (BIT_REVERSE(Xid, FALSE)) STORED,
  -- The work unit ID.
  WorkUnitId INT64 NOT NULL,
  -- Unique ID of the buffered task.
  TaskId STRING(MAX) NOT NULL,
  -- The task target: "experiments/{xid}/workUnits/{wid}/executions/{execution_id}".
  Target STRING(MAX) NOT NULL,
  -- The buffered actuator action.
  ActionProto cloud.xmanager.v1.Action NOT NULL,
  -- The operation name the task updates on completion.
  OperationName STRING(MAX) NOT NULL,
  -- Trace ID propagated from the original task.
  TraceId STRING(MAX) NOT NULL,
  -- Work unit priority at buffering time. Higher values are replayed first.
  Priority INT64 NOT NULL,
  -- The time the task was buffered. Commit timestamps are not allowed because generated columns reference it.
  CreateTime TIMESTAMP NOT NULL,
  -- The time the task was resolved during resumption. NULL while pending.
  ProcessedTime TIMESTAMP,
  -- The resolution outcome (e.g. replayed, skipped).
  ResolutionStatus STRING(32),
  -- The resumption run that resolved the task.
  ResumptionRunId STRING(64),
  -- TRUE while pending, NULL once processed. Backs the null-filtered PausedTasksUnprocessed index.
  IsPending BOOL AS (IF(ProcessedTime IS NULL, TRUE, NULL)) STORED,
  -- Expiration time for automatic row pruning via TTL policy (7 days after last activity).
  ExpirationTime TIMESTAMP AS (TIMESTAMP_ADD(IFNULL(ProcessedTime, CreateTime), INTERVAL 7 DAY)) STORED,
) PRIMARY KEY (BitReversedXid, WorkUnitId, TaskId),
  INTERLEAVE IN PARENT WorkUnits
  ON DELETE CASCADE,
  ROW DELETION POLICY (OLDER_THAN(ExpirationTime, INTERVAL 0 DAY));

-- Replay ordering index for pending paused tasks. IsPending must be stored for the null filter.
CREATE INDEX PausedTasksUnprocessed ON PausedTasks (Priority DESC, CreateTime) STORING (ActionProto, Target, OperationName, TraceId, IsPending) WHERE IsPending IS NOT NULL;
