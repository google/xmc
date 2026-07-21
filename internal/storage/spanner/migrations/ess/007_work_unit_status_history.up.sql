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
CREATE TABLE WorkUnitStatusHistory (
  Xid INT64 NOT NULL,
  BitReversedXid INT64 NOT NULL AS (BIT_REVERSE(Xid, FALSE)) STORED,
  WorkUnitId INT64 NOT NULL,
  HistoryId INT64 NOT NULL,
  ExecutionId INT64,
  QueueName STRING(MAX),
  PreviousState cloud.xmanager.v1.WorkUnitStatus.WorkUnitState,
  NewState cloud.xmanager.v1.WorkUnitStatus.WorkUnitState NOT NULL,
  DominantResource STRING(MAX),
  DominantResourceUnits INT64,
  TransitionTime TIMESTAMP NOT NULL,
  ExpirationTime TIMESTAMP AS (TIMESTAMP_ADD(TransitionTime, INTERVAL 30 DAY)) STORED,
) PRIMARY KEY (BitReversedXid, WorkUnitId, HistoryId),
  INTERLEAVE IN PARENT WorkUnits
  ON DELETE CASCADE,
  ROW DELETION POLICY (OLDER_THAN(ExpirationTime, INTERVAL 0 DAY));

CREATE INDEX WorkUnitStatusHistoryByQueueAndTransitionTime ON WorkUnitStatusHistory (QueueName, TransitionTime DESC, NewState, PreviousState);
