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
ALTER PROTO BUNDLE INSERT (
  cloud.xmanager.v1.AuxiliaryInfo
);

CREATE TABLE StatusMessages (
  Xid INT64 NOT NULL,
  BitReversedXid INT64 NOT NULL AS (BIT_REVERSE(Xid, FALSE)) STORED,
  WorkUnitId INT64,
  MessageId INT64 NOT NULL,
  ExecutionId INT64,
  Severity cloud.xmanager.v1.StructuredMessage.Severity NOT NULL,
  Source cloud.xmanager.v1.StructuredMessage.Source NOT NULL,
  UserFacingText STRING(MAX) NOT NULL,
  UserFacingUrl cloud.xmanager.v1.Url,
  CreateTime TIMESTAMP NOT NULL,
  PreviousMessage cloud.xmanager.v1.StructuredMessage,
  AuxiliaryInfo cloud.xmanager.v1.AuxiliaryInfo,
  ExpirationTime TIMESTAMP AS (IF(Severity = 'ERROR' OR Severity = 'FATAL',
    TIMESTAMP_ADD(CreateTime, INTERVAL 180 DAY),
    TIMESTAMP_ADD(CreateTime, INTERVAL 30 DAY))) STORED,
) PRIMARY KEY (BitReversedXid, CreateTime DESC, WorkUnitId, MessageId),
  INTERLEAVE IN PARENT Experiments
  ON DELETE CASCADE,
  ROW DELETION POLICY (OLDER_THAN(ExpirationTime, INTERVAL 0 DAY));
