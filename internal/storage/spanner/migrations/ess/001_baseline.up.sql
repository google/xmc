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
CREATE PROTO BUNDLE (
  cloud.xmanager.v1.Url,
  cloud.xmanager.v1.ExperimentStatus,
  cloud.xmanager.v1.ExperimentStatus.ActiveState,
  cloud.xmanager.v1.ExperimentStatus.LaunchState,
  cloud.xmanager.v1.ResourceSet,
  cloud.xmanager.v1.Priority,
  cloud.xmanager.v1.StructuredMessage,
  cloud.xmanager.v1.StructuredMessage.Source,
  cloud.xmanager.v1.StructuredMessage.Severity,
  cloud.xmanager.v1.Artifact,
  cloud.xmanager.v1.Artifact.LifecyclePhase,
  cloud.xmanager.v1.Artifact.DataType,
  google.rpc.Status,
  cloud.xmanager.v1.OperationMetadata,
  cloud.xmanager.v1.OperationMetadata.ActionType,
  cloud.xmanager.v1.Executable,
  cloud.xmanager.v1.ExecutableStatus,
  cloud.xmanager.v1.SchedulingConstraints,
  cloud.xmanager.v1.WorkUnit,
  cloud.xmanager.v1.WorkUnit.WorkUnitType,
  cloud.xmanager.v1.WorkUnitStatus,
  cloud.xmanager.v1.WorkUnitStatus.WorkUnitState,
  cloud.xmanager.v1.WorkUnitExecutionMetadata,
  cloud.xmanager.v1.WorkUnitExecutionMetadata.WorkUnitExecutionType,
  cloud.xmanager.v1.WorkUnitExecution,
  cloud.xmanager.v1.WorkUnitExecution.WorkUnitExecutionState
);

CREATE TABLE Experiments (
  Xid INT64 NOT NULL,
  BitReversedXid INT64 NOT NULL AS (BIT_REVERSE(Xid, FALSE)) STORED,
  Title STRING(MAX) NOT NULL,
  Title_Tokens TOKENLIST AS (TOKENIZE_FULLTEXT(Title)) HIDDEN,
  Creator STRING(MAX) NOT NULL,
  Creator_Tokens TOKENLIST AS (TOKENIZE_FULLTEXT(Creator)) HIDDEN,
  Priority INT64 NOT NULL,
  Urls ARRAY<cloud.xmanager.v1.Url> NOT NULL,
  Tags ARRAY<STRING(MAX)> NOT NULL,
  Tags_Tokens TOKENLIST AS (TOKENIZE_FULLTEXT(Tags)) HIDDEN,
  Notes STRING(MAX) NOT NULL,
  Resources ARRAY<cloud.xmanager.v1.ResourceSet> NOT NULL,
  Status cloud.xmanager.v1.ExperimentStatus NOT NULL,
  ActiveState cloud.xmanager.v1.ExperimentStatus.ActiveState NOT NULL AS (Status.active_state) STORED,
  LaunchState cloud.xmanager.v1.ExperimentStatus.LaunchState NOT NULL AS (Status.launch_state) STORED,
  CreateTime TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = TRUE),
  UpdateTime TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = TRUE),
  CompleteTime TIMESTAMP,
) PRIMARY KEY (BitReversedXid);

CREATE INDEX ExperimentsByCreator ON Experiments (Creator);

CREATE SEARCH INDEX ExperimentsSearchIndex ON Experiments (Title_Tokens, Tags_Tokens, Creator_Tokens);

CREATE TABLE Artifacts (
  Xid INT64 NOT NULL,
  BitReversedXid INT64 NOT NULL AS (BIT_REVERSE(Xid, FALSE)) STORED,
  WorkUnitId INT64,
  ArtifactId INT64 NOT NULL,
  BitReversedArtifactId INT64 NOT NULL AS (BIT_REVERSE(ArtifactId, FALSE)) STORED,
  Url STRING(MAX) NOT NULL,
  Url_Tokens TOKENLIST AS (TOKENIZE_FULLTEXT(Url)) HIDDEN,
  Icon STRING(MAX) NOT NULL,
  DisplayName STRING(MAX) NOT NULL,
  DisplayName_Tokens TOKENLIST AS (TOKENIZE_FULLTEXT(DisplayName)) HIDDEN,
  Priority INT64 NOT NULL,
  Tags ARRAY<STRING(MAX)> NOT NULL,
  Tags_Tokens TOKENLIST AS (TOKENIZE_FULLTEXT(Tags)) HIDDEN,
  Notes STRING(MAX) NOT NULL,
  Notes_Tokens TOKENLIST AS (TOKENIZE_FULLTEXT(Notes)) HIDDEN,
  LifecyclePhase cloud.xmanager.v1.Artifact.LifecyclePhase NOT NULL,
  DataType cloud.xmanager.v1.Artifact.DataType NOT NULL,
  MimeType STRING(MAX) NOT NULL,
  MimeType_Tokens TOKENLIST AS (TOKENIZE_FULLTEXT(MimeType)) HIDDEN,
  CreateTime TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = TRUE),
  UpdateTime TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = TRUE),
) PRIMARY KEY (BitReversedXid, WorkUnitId, ArtifactId),
  INTERLEAVE IN PARENT Experiments
  ON DELETE CASCADE;

CREATE UNIQUE INDEX ArtifactsByBitReversedArtifactId ON Artifacts (BitReversedArtifactId);

CREATE SEARCH INDEX ArtifactsSearchIndex ON Artifacts (Url_Tokens, DisplayName_Tokens, Tags_Tokens, Notes_Tokens, MimeType_Tokens);

CREATE TABLE WorkUnits (
  Xid INT64 NOT NULL,
  BitReversedXid INT64 NOT NULL AS (BIT_REVERSE(Xid, FALSE)) STORED,
  WorkUnitId INT64 NOT NULL,
  WorkUnitType cloud.xmanager.v1.WorkUnit.WorkUnitType NOT NULL,
  Priority INT64 NOT NULL,
  Urls ARRAY<cloud.xmanager.v1.Url> NOT NULL,
  Tags ARRAY<STRING(MAX)> NOT NULL,
  Notes STRING(MAX) NOT NULL,
  Executable cloud.xmanager.v1.Executable NOT NULL,
  ExecutionCreateTime TIMESTAMP NOT NULL,
  ExecutionUpdateTime TIMESTAMP NOT NULL,
  ExecutionCompleteTime TIMESTAMP,
  ExecutionId INT64 NOT NULL,
  ExecutionMetadata cloud.xmanager.v1.WorkUnitExecutionMetadata NOT NULL,
  WorkUnitStatus cloud.xmanager.v1.WorkUnitStatus NOT NULL,
  QueueName STRING(MAX) NOT NULL AS (Executable.scheduling_constraints.queue_name) STORED,
  ExecutionState cloud.xmanager.v1.WorkUnitStatus.WorkUnitState NOT NULL AS (WorkUnitStatus.state) STORED,
  ExecutionStatusMessageSeverity cloud.xmanager.v1.StructuredMessage.Severity AS (GREATEST(IFNULL(WorkUnitStatus.system_message.severity, 0), IFNULL(WorkUnitStatus.user_message.severity, 0))) STORED,
  CreateTime TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = TRUE),
  UpdateTime TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = TRUE),
) PRIMARY KEY (BitReversedXid, WorkUnitId),
  INTERLEAVE IN PARENT Experiments
  ON DELETE CASCADE;

CREATE INDEX WorkUnitsByQueueName ON WorkUnits (QueueName);

CREATE TABLE Operations (
  Xid INT64 NOT NULL,
  BitReversedXid INT64 NOT NULL AS (BIT_REVERSE(Xid, FALSE)) STORED,
  WorkUnitId INT64 NOT NULL,
  OperationId STRING(MAX) NOT NULL DEFAULT (GENERATE_UUID()),
  Done BOOL NOT NULL,
  Status google.rpc.Status,
  Response cloud.xmanager.v1.WorkUnit,
  OperationMetadata cloud.xmanager.v1.OperationMetadata NOT NULL,
  CreateTime TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = TRUE),
  UpdateTime TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = TRUE),
) PRIMARY KEY (BitReversedXid, WorkUnitId, OperationId),
  INTERLEAVE IN PARENT WorkUnits
  ON DELETE CASCADE;

CREATE TABLE WorkUnitExecutions (
  Xid INT64 NOT NULL,
  BitReversedXid INT64 NOT NULL AS (BIT_REVERSE(Xid, FALSE)) STORED,
  WorkUnitId INT64 NOT NULL,
  ExecutionId INT64 NOT NULL,
  ExecutionMetadata cloud.xmanager.v1.WorkUnitExecutionMetadata NOT NULL,
  WorkUnitStatus cloud.xmanager.v1.WorkUnitStatus NOT NULL,
  CreateTime TIMESTAMP NOT NULL,
  UpdateTime TIMESTAMP NOT NULL,
  CompleteTime TIMESTAMP,
) PRIMARY KEY (BitReversedXid, WorkUnitId, ExecutionId),
  INTERLEAVE IN PARENT WorkUnits
  ON DELETE CASCADE;
