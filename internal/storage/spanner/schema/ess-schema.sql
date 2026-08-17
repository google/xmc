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
# SQLFORMAT:OFF

CREATE PROTO BUNDLE (
  -- Protos used in Experiments
  cloud.xmanager.v1.Url,
  cloud.xmanager.v1.ExperimentStatus,
  cloud.xmanager.v1.ExperimentStatus.ActiveState,
  cloud.xmanager.v1.ExperimentStatus.LaunchState,
  cloud.xmanager.v1.ResourceSet,
  cloud.xmanager.v1.Priority,

  -- Protos used in StructuredMessages
  cloud.xmanager.v1.StructuredMessage,
  cloud.xmanager.v1.StructuredMessage.Source,
  cloud.xmanager.v1.StructuredMessage.Severity,
  cloud.xmanager.v1.AuxiliaryInfo,
  cloud.xmanager.v1.QueueInfo,

  -- Protos used in Artifacts
  cloud.xmanager.v1.Artifact,
  cloud.xmanager.v1.Artifact.AdditionalInfo,
  cloud.xmanager.v1.Artifact.LifecyclePhase,
  cloud.xmanager.v1.Artifact.DataType,
  cloud.xmanager.v1.ArtifactPayload,
  cloud.xmanager.v1.ManagedXprofArtifact,
  cloud.xmanager.v1.XProfUIInstance,
  cloud.xmanager.v1.CodeSourceArtifact,
  cloud.xmanager.v1.TextArtifact,
  cloud.xmanager.v1.UrlArtifact,
  cloud.xmanager.v1.PlotType,
  cloud.xmanager.v1.ChartArtifact,
  cloud.xmanager.v1.ChartArtifact.Status,
  cloud.xmanager.v1.Chart,
  cloud.xmanager.v1.PlotSpec,
  cloud.xmanager.v1.LayoutSpec,
  cloud.xmanager.v1.LayoutSpec.LayoutType,
  cloud.xmanager.v1.GridPosition,
  cloud.xmanager.v1.DataSpec,
  cloud.xmanager.v1.DataSpec.DataRange,
  cloud.xmanager.v1.MappingSpec,
  cloud.xmanager.v1.GeomSpec,
  cloud.xmanager.v1.GeomSpec.GeomType,
  cloud.xmanager.v1.GeomSpec.StyleSpec,
  cloud.xmanager.v1.StatSpec,
  cloud.xmanager.v1.StatSpec.StatType,
  cloud.xmanager.v1.StatSpec.Aggregation,
  cloud.xmanager.v1.StatSpec.SmoothConfig,
  cloud.xmanager.v1.StatSpec.SmoothConfig.Algorithm,
  cloud.xmanager.v1.StatSpec.ForecastConfig,
  cloud.xmanager.v1.CoordSpec,
  cloud.xmanager.v1.CoordSpec.ScaleType,
  cloud.xmanager.v1.FacetSpec,
  cloud.xmanager.v1.FacetSpec.FacetType,

  -- Protos used in Operations
  google.rpc.Status,
  cloud.xmanager.v1.OperationMetadata,
  cloud.xmanager.v1.OperationMetadata.ActionType,

  -- Protos used in WorkUnits
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
  cloud.xmanager.v1.WorkUnitExecution.WorkUnitExecutionState,
);

-- Table that holds the core experiments data.
CREATE TABLE Experiments (
  -- XID of the experiment.
  Xid INT64 NOT NULL,
  -- Bit-reversed XID for primary key.
  BitReversedXid INT64 NOT NULL AS (BIT_REVERSE(Xid, FALSE)) STORED,

  -- The title of the experiment.
  Title STRING(MAX) NOT NULL,
  -- Tokenized title of the experiment.
  Title_Tokens TOKENLIST AS (TOKENIZE_FULLTEXT(Title)) HIDDEN,
  -- Creator of the experiment.
  Creator STRING(MAX) NOT NULL,
  -- Tokenized creator of the experiment.
  Creator_Tokens TOKENLIST AS (TOKENIZE_FULLTEXT(Creator)) HIDDEN,
  -- The priority of the experiment. Higher values indicate higher priority.
  Priority INT64 NOT NULL,
  -- The URLs of the experiment.
  Urls ARRAY<cloud.xmanager.v1.Url> NOT NULL,
  -- The tags of the experiment.
  Tags ARRAY<STRING(MAX)> NOT NULL,
  -- Tokenized tags of the experiment.
  Tags_Tokens TOKENLIST AS (TOKENIZE_FULLTEXT(Tags)) HIDDEN,
  -- The notes of the experiment.
  Notes STRING(MAX) NOT NULL,

  -- The resources of the experiment's work units, denormalized from the WorkUnits table.
  Resources ARRAY<cloud.xmanager.v1.ResourceSet> NOT NULL,

  -- The status of the experiment.
  Status cloud.xmanager.v1.ExperimentStatus NOT NULL,

  -- State of the experiment, stored for searching.
  ActiveState cloud.xmanager.v1.ExperimentStatus.ActiveState NOT NULL AS (Status.active_state) STORED,
  -- State of the experiment's launch process, stored for searching.
  LaunchState cloud.xmanager.v1.ExperimentStatus.LaunchState NOT NULL AS (Status.launch_state) STORED,

  -- The time the experiment was created.
  CreateTime TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = TRUE),
  -- The last time the experiment was updated, including on creation.
  UpdateTime TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = TRUE),
  -- The last time the experiment was moved to a terminal state.
  CompleteTime TIMESTAMP,
) PRIMARY KEY (BitReversedXid);

CREATE INDEX ExperimentsByCreator ON Experiments (Creator);

-- Allows full-text search on the Experiments by creator, title, and tags.
CREATE SEARCH INDEX ExperimentsSearchIndex ON Experiments (Title_Tokens, Tags_Tokens, Creator_Tokens);

CREATE TABLE Artifacts (
  -- The associated XID.
  Xid INT64 NOT NULL,
  -- The bit-reversed XID of the experiment for indexing.
  BitReversedXid INT64 NOT NULL AS (BIT_REVERSE(Xid, FALSE)) STORED,
  -- The work unit ID. This may be NULL for artifacts that are not associated with a work unit.
  WorkUnitId INT64,
  -- The artifact ID associated with the artifact.
  ArtifactId INT64 NOT NULL,
  -- The bit-reversed artifact ID for indexing.
  BitReversedArtifactId INT64 NOT NULL AS (BIT_REVERSE(ArtifactId, FALSE)) STORED,

  -- The link of the artifact.
  Url STRING(MAX) NOT NULL,
  -- Tokenized URL of the artifact.
  Url_Tokens TOKENLIST AS (TOKENIZE_FULLTEXT(Url)) HIDDEN,
  -- The icon of the artifact.
  Icon STRING(MAX) NOT NULL,
  -- The display name of the artifact.
  DisplayName STRING(MAX) NOT NULL,
  -- Tokenized display name of the artifact.
  DisplayName_Tokens TOKENLIST AS (TOKENIZE_FULLTEXT(DisplayName)) HIDDEN,
  -- The priority of the artifact when being shown in the UI. Lower numbers indicate higher priority.
  Priority INT64 NOT NULL,

  -- The tags of an artifact.
  Tags ARRAY<STRING(MAX)> NOT NULL,
  -- Tokenized tags of the artifact.
  Tags_Tokens TOKENLIST AS (TOKENIZE_FULLTEXT(Tags)) HIDDEN,
  -- The notes of an artifact.
  Notes STRING(MAX) NOT NULL,
  -- Tokenized notes of the artifact.
  Notes_Tokens TOKENLIST AS (TOKENIZE_FULLTEXT(Notes)) HIDDEN,

  -- The lifecycle phase of the artifact.
  LifecyclePhase cloud.xmanager.v1.Artifact.LifecyclePhase NOT NULL,
  -- The type of artifact.
  DataType cloud.xmanager.v1.Artifact.DataType NOT NULL,
  -- The MIME type of the artifact.
  MimeType STRING(MAX) NOT NULL,
  -- Tokenized MIME type of the artifact.
  MimeType_Tokens TOKENLIST AS (TOKENIZE_FULLTEXT(MimeType)) HIDDEN,
  -- Additional info for the artifact.
  AdditionalInfo cloud.xmanager.v1.Artifact.AdditionalInfo,
  -- Strongly-typed payload for the artifact.
  Payload cloud.xmanager.v1.ArtifactPayload,
  -- Searchable content for the artifact.
  SearchableContent STRING(MAX),
  -- Tokenized searchable content of the artifact.
  SearchableContent_Tokens TOKENLIST AS (TOKENIZE_FULLTEXT(SearchableContent)) HIDDEN,

  -- The creation time of the artifact.
  CreateTime TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = TRUE),
  -- The last time the artifact was updated, including on creation.
  UpdateTime TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = TRUE),
) PRIMARY KEY (BitReversedXid, WorkUnitId, ArtifactId),
  INTERLEAVE IN PARENT Experiments
  ON DELETE CASCADE;

-- To efficiently find artifacts by just their artifact ID.
CREATE UNIQUE INDEX ArtifactsByBitReversedArtifactId ON Artifacts (BitReversedArtifactId);

-- Allows full-text search on the Artifacts by URL, display name, MIME type, tags, and notes.
CREATE SEARCH INDEX ArtifactsSearchIndex ON Artifacts (Url_Tokens, DisplayName_Tokens, Tags_Tokens, Notes_Tokens, MimeType_Tokens, SearchableContent_Tokens);

-- Table that holds the core work units data. Interleaved with Experiments.
CREATE TABLE WorkUnits (
  -- The associated XID.
  Xid INT64 NOT NULL,
  -- The bit-reversed XID of the experiment for indexing.
  BitReversedXid INT64 NOT NULL AS (BIT_REVERSE(Xid, FALSE)) STORED,
  -- The work unit ID.
  WorkUnitId INT64 NOT NULL,

  -- The type of the work unit.
  WorkUnitType cloud.xmanager.v1.WorkUnit.WorkUnitType NOT NULL,
  -- The priority of the work unit. Higher values indicate higher priority.
  Priority INT64 NOT NULL,
  -- The URLs of the experiment.
  Urls ARRAY<cloud.xmanager.v1.Url> NOT NULL,
  -- The tags of the work unit.
  Tags ARRAY<STRING(MAX)> NOT NULL,
  -- The notes of the experiment.
  Notes STRING(MAX) NOT NULL,

  -- The executable of the work unit.
  
  Executable cloud.xmanager.v1.Executable NOT NULL,

  -- Create time of the current work unit execution.
  ExecutionCreateTime TIMESTAMP NOT NULL,
  -- Update time of the current work unit execution.
  ExecutionUpdateTime TIMESTAMP NOT NULL,
  -- Complete time of the current work unit execution.
  ExecutionCompleteTime TIMESTAMP,
  -- The current execution ID of the work unit.
  ExecutionId INT64 NOT NULL,
  -- Metadata about the current work unit execution.
  ExecutionMetadata cloud.xmanager.v1.WorkUnitExecutionMetadata NOT NULL,
  -- The status of the current work unit execution.
  WorkUnitStatus cloud.xmanager.v1.WorkUnitStatus NOT NULL,

  -- The queue name of the work unit. Stored for indexing.
  QueueName STRING(MAX) NOT NULL AS (Executable.scheduling_constraints.queue_name) STORED,
  -- The state of the current work unit execution. Stored for searching.
  ExecutionState cloud.xmanager.v1.WorkUnitStatus.WorkUnitState NOT NULL AS (WorkUnitStatus.state) STORED,
  -- The severity of the current work unit structured status message. This is useful to query for
  -- the highest severity messages to propagate up to the experiment.
  ExecutionStatusMessageSeverity cloud.xmanager.v1.StructuredMessage.Severity AS (GREATEST(IFNULL(WorkUnitStatus.system_message.severity, 0), IFNULL(WorkUnitStatus.user_message.severity, 0))) STORED,
  -- PositionInQueue represents the work unit's position in the queue for ROD sorting.
  -- JSON_VALUE(CAST(WorkUnitStatus AS STRING), ...) is used to safely extract position_in_queue
  -- without triggering C++ pointer assertion crashes in Spanner Emulator (evaluated_column.cc:122)
  -- when scheduling_information or auxiliary_info is unpopulated.
  PositionInQueue INT64 AS (
  IFNULL(
    SAFE_CAST(
      JSON_VALUE(
        CAST(WorkUnitStatus AS STRING),
        '$.scheduling_information.auxiliary_info.queue_info.position_in_queue'
      ) AS INT64
    ),
    9223372036854775807
  )
  ) STORED,

  IsPendingOrRunningJob BOOL AS (
    IF(ExecutionState IN ('SCHEDULING', 'STARTING', 'RUNNING', 'INITIALIZING'), TRUE, NULL)
  ) STORED,

  SortPhase INT64 AS (
  CASE
    WHEN ExecutionState IN ('RUNNING', 'STARTING', 'INITIALIZING') THEN 1
    WHEN ExecutionState IN ('SCHEDULING') THEN 0
    ELSE -1
  END
  ) STORED,

  -- The time the work unit was created.
  CreateTime TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = TRUE),
  -- The last time the work unit was updated, including on creation.
  UpdateTime TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = TRUE),
) PRIMARY KEY (BitReversedXid, WorkUnitId),
  INTERLEAVE IN PARENT Experiments
  ON DELETE CASCADE;

-- To efficiently find work units assigned to a specific queue.
CREATE INDEX WorkUnitsByQueueName ON WorkUnits (QueueName);

-- Partial index for active work unit queries by position.
CREATE INDEX WorkUnitsByQueueAndPosition 
ON WorkUnits (
  QueueName, 
  SortPhase DESC, 
  PositionInQueue ASC, 
  CreateTime ASC, 
  WorkUnitId ASC
) STORING (IsPendingOrRunningJob) WHERE IsPendingOrRunningJob IS NOT NULL;

-- Table that holds the list of operations.
CREATE TABLE Operations (
  -- The associated XID.
  Xid INT64 NOT NULL,
  -- The bit-reversed XID of the experiment for indexing.
  BitReversedXid INT64 NOT NULL AS (BIT_REVERSE(Xid, FALSE)) STORED,
  -- The work unit ID.
  WorkUnitId INT64 NOT NULL,
  -- The operation ID.
  OperationId STRING(MAX) NOT NULL DEFAULT (GENERATE_UUID()),

  -- If the operation is done.
  Done BOOL NOT NULL,

  -- Error message if the operation failed.
  Status google.rpc.Status,
  -- Response message if the operation succeeded.
  Response cloud.xmanager.v1.WorkUnit,

  -- The metadata of the operation.
  OperationMetadata cloud.xmanager.v1.OperationMetadata NOT NULL,

  -- The time the operation was created.
  CreateTime TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = TRUE),
  -- The last time the operation was updated, including on creation.
  UpdateTime TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = TRUE),
) PRIMARY KEY (BitReversedXid, WorkUnitId, OperationId),
  INTERLEAVE IN PARENT WorkUnits
  ON DELETE CASCADE;

-- Table that holds the work unit's historical executions. Interleaved with WorkUnits.
CREATE TABLE WorkUnitExecutions (
  -- The associated XID.
  Xid INT64 NOT NULL,
  -- The bit-reversed XID of the experiment for indexing.
  BitReversedXid INT64 NOT NULL AS (BIT_REVERSE(Xid, FALSE)) STORED,
  -- The work unit ID.
  WorkUnitId INT64 NOT NULL,
  -- The unique ID of the work unit execution.
  ExecutionId INT64 NOT NULL,

  -- Metadata about the work unit execution.
  ExecutionMetadata cloud.xmanager.v1.WorkUnitExecutionMetadata NOT NULL,
  -- The status of the work unit execution.
  WorkUnitStatus cloud.xmanager.v1.WorkUnitStatus NOT NULL,

  -- The time the work unit execution was created.
  CreateTime TIMESTAMP NOT NULL,
  -- The last time the work unit was updated.
  UpdateTime TIMESTAMP NOT NULL,
  -- The time the work unit execution was completed.
  CompleteTime TIMESTAMP,
) PRIMARY KEY (BitReversedXid, WorkUnitId, ExecutionId),
  INTERLEAVE IN PARENT WorkUnits
  ON DELETE CASCADE;

-- Table that holds the status message history for experiments and work units. Interleaved with Experiments.
CREATE TABLE StatusMessages (
  -- The associated XID.
  Xid INT64 NOT NULL,

  -- The bit-reversed XID of the experiment for indexing.
  BitReversedXid INT64 NOT NULL AS (BIT_REVERSE(Xid, FALSE)) STORED,

  -- The work unit ID. NULL for experiment-level messages.
  WorkUnitId INT64,

  -- The unique ID for the message to avoid collisions if multiple messages are created at the same time.
  MessageId INT64 NOT NULL,

  -- The unique ID of the work unit execution. NULL for experiment-level messages.
  ExecutionId INT64,

  -- Severity of the structured message.
  Severity cloud.xmanager.v1.StructuredMessage.Severity NOT NULL,

  -- Source of the structured message.
  Source cloud.xmanager.v1.StructuredMessage.Source NOT NULL,

  -- User-facing text of the structured message.
  UserFacingText STRING(MAX) NOT NULL,

  -- User-facing URL of the structured message.
  UserFacingUrl cloud.xmanager.v1.Url,

  -- The time the structured message was created.
  CreateTime TIMESTAMP NOT NULL,

  -- The old message that was replaced by this one.
  PreviousMessage cloud.xmanager.v1.StructuredMessage,

  -- Auxiliary info associated with the message.
  AuxiliaryInfo cloud.xmanager.v1.AuxiliaryInfo,

  -- The time the structured message will expire.
  ExpirationTime TIMESTAMP AS (IF(Severity = 'ERROR' OR Severity = 'FATAL',
    TIMESTAMP_ADD(CreateTime, INTERVAL 180 DAY),
    TIMESTAMP_ADD(CreateTime, INTERVAL 30 DAY))) STORED,
) PRIMARY KEY (BitReversedXid, CreateTime DESC, WorkUnitId, MessageId),
  INTERLEAVE IN PARENT Experiments
  ON DELETE CASCADE,
  ROW DELETION POLICY (OLDER_THAN(ExpirationTime, INTERVAL 0 DAY));

CREATE TABLE Dashboards (
  DashboardId STRING(MAX) NOT NULL,
  Title STRING(MAX) NOT NULL,
  Description STRING(MAX) NOT NULL,
  Owner STRING(MAX) NOT NULL,
  ChartLayout BYTES(MAX),
  CreatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = TRUE),
  UpdatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = TRUE),
) PRIMARY KEY (DashboardId);

CREATE TABLE Charts (
  ChartId STRING(MAX) NOT NULL,
  Title STRING(MAX) NOT NULL,
  Xid INT64 NOT NULL,
  PlotLayout BYTES(MAX),
  Owner STRING(MAX) NOT NULL,
  CreatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = TRUE),
  UpdatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = TRUE),
) PRIMARY KEY (ChartId);

CREATE TABLE DashboardCharts (
  DashboardId STRING(MAX) NOT NULL,
  DashboardChartId STRING(MAX) NOT NULL,
  ChartId STRING(MAX) NOT NULL,
  GridPosition BYTES(MAX),
  CreatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = TRUE),
  CONSTRAINT FK_DashboardChart_ChartId FOREIGN KEY (ChartId) REFERENCES Charts(ChartId)
) PRIMARY KEY (DashboardId, DashboardChartId),
  INTERLEAVE IN PARENT Dashboards ON DELETE CASCADE;

CREATE INDEX DashboardChartsByChartId ON DashboardCharts (ChartId);

CREATE TABLE ChartPlots (
  ChartId STRING(MAX) NOT NULL,
  PlotId STRING(MAX) NOT NULL,
  Title STRING(MAX) NOT NULL,
  PlotType INT64 NOT NULL,
  PlotSpec BYTES(MAX),
) PRIMARY KEY (ChartId, PlotId),
  INTERLEAVE IN PARENT Charts ON DELETE CASCADE;

CREATE TABLE ArtifactCharts (
  Xid INT64 NOT NULL,
  BitReversedXid INT64 NOT NULL AS (BIT_REVERSE(Xid, FALSE)) STORED,
  WorkUnitId INT64,
  ArtifactId INT64 NOT NULL,
  ArtifactChartId STRING(MAX) NOT NULL,
  ChartId STRING(MAX) NOT NULL,
  CreatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = TRUE),
  CONSTRAINT FK_ArtifactChart_ChartId FOREIGN KEY (ChartId) REFERENCES Charts(ChartId)
) PRIMARY KEY (BitReversedXid, WorkUnitId, ArtifactId, ArtifactChartId),
  INTERLEAVE IN PARENT Artifacts ON DELETE CASCADE;

CREATE INDEX ArtifactChartsByChartId ON ArtifactCharts (ChartId);

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
  -- The name of the queue assigned to the work unit.
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
  INTERLEAVE IN PARENT WorkUnits
  ON DELETE CASCADE,
  ROW DELETION POLICY (OLDER_THAN(ExpirationTime, INTERVAL 0 DAY));

-- Index for efficient sliding-window queue velocity and turnover rate queries.
CREATE INDEX WorkUnitStatusHistoryByQueueAndTransitionTime ON WorkUnitStatusHistory (QueueName, TransitionTime DESC, NewState, PreviousState) STORING (ResourceType, ResourceUnits);


