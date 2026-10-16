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

-- Canonical PostgreSQL ESS schema, mirroring internal/storage/spanner/schema/ess-schema.sql.
-- Protos are BYTEA and proto enums BIGINT; proto-derived columns are populated on write.

-- array_to_string is STABLE; generated columns require an IMMUTABLE wrapper.
CREATE OR REPLACE FUNCTION immutable_array_to_string(arr TEXT[], sep TEXT) RETURNS TEXT
  LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT AS $$ SELECT array_to_string(arr, sep); $$;

-- Table that holds the core experiments data.
CREATE TABLE Experiments (
  Xid BIGINT NOT NULL,
  Title TEXT NOT NULL,
  Title_Tokens TSVECTOR GENERATED ALWAYS AS (to_tsvector('simple'::regconfig, coalesce(Title, ''))) STORED,
  Creator TEXT NOT NULL,
  Creator_Tokens TSVECTOR GENERATED ALWAYS AS (to_tsvector('simple'::regconfig, coalesce(Creator, ''))) STORED,
  Priority BIGINT NOT NULL, -- Higher values indicate higher priority.
  Urls BYTEA[] NOT NULL, -- cloud.xmanager.v1.Url
  Tags TEXT[] NOT NULL,
  Tags_Tokens TSVECTOR GENERATED ALWAYS AS (to_tsvector('simple'::regconfig, coalesce(immutable_array_to_string(Tags, ' '), ''))) STORED,
  Notes TEXT NOT NULL,
  Resources BYTEA[] NOT NULL, -- cloud.xmanager.v1.ResourceSet, denormalized from WorkUnits.
  Status BYTEA NOT NULL, -- cloud.xmanager.v1.ExperimentStatus
  ActiveState BIGINT NOT NULL, -- ExperimentStatus.ActiveState, populated on write from Status.
  LaunchState BIGINT NOT NULL, -- ExperimentStatus.LaunchState, populated on write from Status.
  CreateTime TIMESTAMPTZ NOT NULL,
  UpdateTime TIMESTAMPTZ NOT NULL,
  CompleteTime TIMESTAMPTZ,
  PRIMARY KEY (Xid)
);

CREATE INDEX ExperimentsByCreator ON Experiments (Creator);
CREATE INDEX ExperimentsSearchIndex ON Experiments USING GIN (Title_Tokens, Tags_Tokens, Creator_Tokens);

-- WorkUnitId is nullable (experiment-level artifacts), so it is kept outside the primary key.
CREATE TABLE Artifacts (
  Xid BIGINT NOT NULL,
  WorkUnitId BIGINT,
  ArtifactId BIGINT NOT NULL,
  Url TEXT NOT NULL,
  Url_Tokens TSVECTOR GENERATED ALWAYS AS (to_tsvector('simple'::regconfig, coalesce(Url, ''))) STORED,
  Icon TEXT NOT NULL,
  DisplayName TEXT NOT NULL,
  DisplayName_Tokens TSVECTOR GENERATED ALWAYS AS (to_tsvector('simple'::regconfig, coalesce(DisplayName, ''))) STORED,
  Priority BIGINT NOT NULL, -- Lower numbers indicate higher UI priority.
  Tags TEXT[] NOT NULL,
  Tags_Tokens TSVECTOR GENERATED ALWAYS AS (to_tsvector('simple'::regconfig, coalesce(immutable_array_to_string(Tags, ' '), ''))) STORED,
  Notes TEXT NOT NULL,
  Notes_Tokens TSVECTOR GENERATED ALWAYS AS (to_tsvector('simple'::regconfig, coalesce(Notes, ''))) STORED,
  LifecyclePhase BIGINT NOT NULL, -- Artifact.LifecyclePhase
  DataType BIGINT NOT NULL, -- Artifact.DataType
  MimeType TEXT NOT NULL,
  MimeType_Tokens TSVECTOR GENERATED ALWAYS AS (to_tsvector('simple'::regconfig, coalesce(MimeType, ''))) STORED,
  AdditionalInfo BYTEA, -- cloud.xmanager.v1.Artifact.AdditionalInfo
  Payload BYTEA, -- cloud.xmanager.v1.ArtifactPayload
  PayloadType TEXT NOT NULL DEFAULT '', -- Populated on write from Payload for payload_type filtering.
  SearchableContent TEXT,
  SearchableContent_Tokens TSVECTOR GENERATED ALWAYS AS (to_tsvector('simple'::regconfig, coalesce(SearchableContent, ''))) STORED,
  CreateTime TIMESTAMPTZ NOT NULL,
  UpdateTime TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (Xid, ArtifactId),
  FOREIGN KEY (Xid) REFERENCES Experiments (Xid) ON DELETE CASCADE
);

CREATE UNIQUE INDEX ArtifactsByArtifactId ON Artifacts (ArtifactId);
CREATE INDEX ArtifactsByXidAndWorkUnitId ON Artifacts (Xid, WorkUnitId, ArtifactId);
CREATE INDEX ArtifactsSearchIndex ON Artifacts USING GIN (Url_Tokens, DisplayName_Tokens, Tags_Tokens, Notes_Tokens, MimeType_Tokens, SearchableContent_Tokens);

-- Table that holds the core work units data.
CREATE TABLE WorkUnits (
  Xid BIGINT NOT NULL,
  WorkUnitId BIGINT NOT NULL,
  WorkUnitType BIGINT NOT NULL, -- WorkUnit.WorkUnitType
  Priority BIGINT NOT NULL, -- Higher values indicate higher priority.
  Urls BYTEA[] NOT NULL, -- cloud.xmanager.v1.Url
  Tags TEXT[] NOT NULL,
  Notes TEXT NOT NULL,
  Executable BYTEA NOT NULL, -- cloud.xmanager.v1.Executable
  ExecutionCreateTime TIMESTAMPTZ NOT NULL,
  ExecutionUpdateTime TIMESTAMPTZ NOT NULL,
  ExecutionCompleteTime TIMESTAMPTZ,
  ExecutionId BIGINT NOT NULL,
  ExecutionMetadata BYTEA NOT NULL, -- cloud.xmanager.v1.WorkUnitExecutionMetadata
  WorkUnitStatus BYTEA NOT NULL, -- cloud.xmanager.v1.WorkUnitStatus
  QueueName TEXT NOT NULL, -- Populated on write from Executable.scheduling_constraints.queue_name.
  ExecutionState BIGINT NOT NULL, -- WorkUnitStatus.WorkUnitState, populated on write from WorkUnitStatus.state.
  ExecutionStatusMessageSeverity BIGINT, -- Max severity of the system/user status messages, populated on write.
  PositionInQueue BIGINT NOT NULL DEFAULT 9223372036854775807, -- ROD queue position, populated on write.
  -- 1=INITIALIZING, 2=SCHEDULING, 3=STARTING, 4=RUNNING.
  IsPendingOrRunningJob BOOLEAN GENERATED ALWAYS AS (CASE WHEN ExecutionState IN (1, 2, 3, 4) THEN TRUE ELSE NULL END) STORED,
  SortPhase BIGINT GENERATED ALWAYS AS (CASE WHEN ExecutionState IN (1, 3, 4) THEN 1 WHEN ExecutionState IN (2) THEN 0 ELSE -1 END) STORED,
  CreateTime TIMESTAMPTZ NOT NULL,
  UpdateTime TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (Xid, WorkUnitId),
  FOREIGN KEY (Xid) REFERENCES Experiments (Xid) ON DELETE CASCADE
);

CREATE INDEX WorkUnitsByQueueName ON WorkUnits (QueueName);
-- Partial index for active work unit queries by position.
CREATE INDEX WorkUnitsByQueueAndPosition ON WorkUnits (QueueName, SortPhase DESC, PositionInQueue ASC, CreateTime ASC, WorkUnitId ASC)
  INCLUDE (IsPendingOrRunningJob, Xid) WHERE IsPendingOrRunningJob IS NOT NULL;

-- Table that holds the list of operations.
CREATE TABLE Operations (
  Xid BIGINT NOT NULL,
  WorkUnitId BIGINT NOT NULL,
  OperationId TEXT NOT NULL DEFAULT (gen_random_uuid()::text),
  Done BOOLEAN NOT NULL,
  Status BYTEA, -- google.rpc.Status, set if the operation failed.
  Response BYTEA, -- cloud.xmanager.v1.WorkUnit, set if the operation succeeded.
  OperationMetadata BYTEA NOT NULL, -- cloud.xmanager.v1.OperationMetadata
  CreateTime TIMESTAMPTZ NOT NULL,
  UpdateTime TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (Xid, WorkUnitId, OperationId),
  FOREIGN KEY (Xid, WorkUnitId) REFERENCES WorkUnits (Xid, WorkUnitId) ON DELETE CASCADE
);

-- Table that holds the work unit's historical executions.
CREATE TABLE WorkUnitExecutions (
  Xid BIGINT NOT NULL,
  WorkUnitId BIGINT NOT NULL,
  ExecutionId BIGINT NOT NULL,
  ExecutionMetadata BYTEA NOT NULL, -- cloud.xmanager.v1.WorkUnitExecutionMetadata
  WorkUnitStatus BYTEA NOT NULL, -- cloud.xmanager.v1.WorkUnitStatus
  CreateTime TIMESTAMPTZ NOT NULL,
  UpdateTime TIMESTAMPTZ NOT NULL,
  CompleteTime TIMESTAMPTZ,
  PRIMARY KEY (Xid, WorkUnitId, ExecutionId),
  FOREIGN KEY (Xid, WorkUnitId) REFERENCES WorkUnits (Xid, WorkUnitId) ON DELETE CASCADE
);

-- Status message history for experiments and work units. WorkUnitId and ExecutionId are NULL for
-- experiment-level messages, so WorkUnitId is kept outside the primary key.
CREATE TABLE StatusMessages (
  Xid BIGINT NOT NULL,
  WorkUnitId BIGINT,
  MessageId BIGINT NOT NULL, -- Avoids collisions between messages created at the same time.
  ExecutionId BIGINT,
  Severity BIGINT NOT NULL, -- StructuredMessage.Severity
  Source BIGINT NOT NULL, -- StructuredMessage.Source
  UserFacingText TEXT NOT NULL,
  UserFacingUrl BYTEA, -- cloud.xmanager.v1.Url
  CreateTime TIMESTAMPTZ NOT NULL,
  PreviousMessage BYTEA, -- cloud.xmanager.v1.StructuredMessage replaced by this one.
  AuxiliaryInfo BYTEA, -- cloud.xmanager.v1.AuxiliaryInfo
  -- 40=ERROR, 50=FATAL. UTC conversion keeps the expression IMMUTABLE.
  ExpirationTime TIMESTAMPTZ GENERATED ALWAYS AS (((CreateTime AT TIME ZONE 'UTC') + CASE WHEN Severity IN (40, 50) THEN INTERVAL '180 days' ELSE INTERVAL '30 days' END) AT TIME ZONE 'UTC') STORED,
  PRIMARY KEY (Xid, CreateTime, MessageId),
  FOREIGN KEY (Xid) REFERENCES Experiments (Xid) ON DELETE CASCADE
);

CREATE INDEX StatusMessagesByXidAndCreateTime ON StatusMessages (Xid, CreateTime DESC, WorkUnitId, MessageId);

CREATE TABLE Dashboards (
  DashboardId TEXT NOT NULL,
  Title TEXT NOT NULL,
  Description TEXT NOT NULL,
  Owner TEXT NOT NULL,
  ChartLayout BYTEA,
  CreatedAt TIMESTAMPTZ NOT NULL,
  UpdatedAt TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (DashboardId)
);

CREATE TABLE Charts (
  ChartId TEXT NOT NULL,
  Title TEXT NOT NULL,
  Description TEXT,
  Xid BIGINT NOT NULL,
  Wid BIGINT NOT NULL DEFAULT 0,
  PlotLayout BYTEA,
  Owner TEXT NOT NULL,
  CreatedAt TIMESTAMPTZ NOT NULL,
  UpdatedAt TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (ChartId)
);

CREATE TABLE DashboardCharts (
  DashboardId TEXT NOT NULL,
  DashboardChartId TEXT NOT NULL,
  ChartId TEXT NOT NULL,
  GridPosition BYTEA,
  CreatedAt TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (DashboardId, DashboardChartId),
  FOREIGN KEY (DashboardId) REFERENCES Dashboards (DashboardId) ON DELETE CASCADE,
  CONSTRAINT FK_DashboardChart_ChartId FOREIGN KEY (ChartId) REFERENCES Charts (ChartId)
);

CREATE INDEX DashboardChartsByChartId ON DashboardCharts (ChartId);

CREATE TABLE ChartPlots (
  ChartId TEXT NOT NULL,
  PlotId TEXT NOT NULL,
  Title TEXT NOT NULL,
  PlotType BIGINT NOT NULL,
  PlotSpec BYTEA,
  PRIMARY KEY (ChartId, PlotId),
  FOREIGN KEY (ChartId) REFERENCES Charts (ChartId) ON DELETE CASCADE
);

-- WorkUnitId is nullable (mirrors Artifacts), so it is kept outside the primary key.
CREATE TABLE ArtifactCharts (
  Xid BIGINT NOT NULL,
  WorkUnitId BIGINT,
  ArtifactId BIGINT NOT NULL,
  ArtifactChartId TEXT NOT NULL,
  ChartId TEXT NOT NULL,
  CreatedAt TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (Xid, ArtifactId, ArtifactChartId),
  FOREIGN KEY (Xid, ArtifactId) REFERENCES Artifacts (Xid, ArtifactId) ON DELETE CASCADE,
  CONSTRAINT FK_ArtifactChart_ChartId FOREIGN KEY (ChartId) REFERENCES Charts (ChartId)
);

CREATE INDEX ArtifactChartsByChartId ON ArtifactCharts (ChartId);

CREATE TABLE Components (
  ComponentId BIGINT NOT NULL,
  Name TEXT NOT NULL,
  ComponentClass TEXT,
  ConfigProto BYTEA NOT NULL,
  AccessPayload BYTEA,
  State BIGINT NOT NULL,
  Generation BIGINT NOT NULL,
  ObservedGeneration BIGINT,
  CreateTime TIMESTAMPTZ NOT NULL,
  UpdateTime TIMESTAMPTZ NOT NULL,
  -- NULL once the component is deleted (State 5), freeing the name for reuse.
  ActiveName TEXT GENERATED ALWAYS AS (CASE WHEN State != 5 THEN Name ELSE NULL END) STORED,
  -- Recorded owner: the declarative object (Kubernetes CR) that last wrote the component; all NULL
  -- for records never written by a sourced request.
  SourceUid TEXT,
  SourceGeneration BIGINT,
  SourceNamespace TEXT,
  SourceClusterUid TEXT,
  SourceEpoch BIGINT,
  SourceLastSeenTime TIMESTAMPTZ, -- Last request accepted from the owner; staleness observability only.
  PRIMARY KEY (ComponentId),
  CONSTRAINT CK_Components_State CHECK (State IN (1, 2, 3, 4, 5))
);

CREATE UNIQUE INDEX Idx_Components_ActiveName ON Components (ActiveName) WHERE ActiveName IS NOT NULL;
CREATE INDEX Idx_Components_Class ON Components (ComponentClass);

CREATE TABLE ComponentDependencies (
  ComponentId BIGINT NOT NULL,
  DependencyComponentId BIGINT NOT NULL,
  PRIMARY KEY (ComponentId, DependencyComponentId),
  FOREIGN KEY (ComponentId) REFERENCES Components (ComponentId) ON DELETE CASCADE,
  CONSTRAINT FK_Dependency FOREIGN KEY (DependencyComponentId) REFERENCES Components (ComponentId)
);

CREATE INDEX Idx_ComponentDependencies_Dependency ON ComponentDependencies (DependencyComponentId);

CREATE TABLE ExperimentComponentSnapshots (
  Xid BIGINT NOT NULL,
  ComponentId BIGINT NOT NULL,
  LockedGeneration BIGINT NOT NULL,
  ConfigProto BYTEA NOT NULL,
  AccessPayload BYTEA,
  PRIMARY KEY (Xid, ComponentId),
  FOREIGN KEY (Xid) REFERENCES Experiments (Xid) ON DELETE CASCADE,
  CONSTRAINT FK_Snapshots_Component FOREIGN KEY (ComponentId) REFERENCES Components (ComponentId)
);

CREATE INDEX Idx_Snapshots_Component ON ExperimentComponentSnapshots (ComponentId);

CREATE VIEW ExperimentConsistencyInfo AS
SELECT
  e.Xid,
  NOT EXISTS (
    SELECT 1
    FROM ExperimentComponentSnapshots s
    INNER JOIN Components c ON s.ComponentId = c.ComponentId
    WHERE s.Xid = e.Xid
      AND (c.State = 5 OR (c.ObservedGeneration IS NOT NULL AND s.LockedGeneration != c.ObservedGeneration))
  ) AS IsExperimentConsistent,
  EXISTS (
    SELECT 1
    FROM ExperimentComponentSnapshots s
    INNER JOIN Components c ON s.ComponentId = c.ComponentId
    WHERE s.Xid = e.Xid
      AND c.ObservedGeneration IS NULL
  ) AS HasPendingReconciliation
FROM Experiments e
WHERE e.ActiveState = 1;

-- Historical work unit state transitions for telemetry and queue velocity metrics.
CREATE TABLE WorkUnitStatusHistory (
  Xid BIGINT NOT NULL,
  WorkUnitId BIGINT NOT NULL,
  HistoryId BIGINT NOT NULL,
  ExecutionId BIGINT,
  QueueName TEXT, -- Should match the kueue ClusterQueue.
  PreviousState BIGINT, -- WorkUnitStatus.WorkUnitState
  NewState BIGINT NOT NULL, -- WorkUnitStatus.WorkUnitState
  ResourceType TEXT, -- Compute resource type requested (e.g. nvidia-tesla-h100, TPU, CPU).
  ResourceUnits BIGINT,
  CreateTime TIMESTAMPTZ NOT NULL,
  -- 30-day retention. UTC conversion keeps the expression IMMUTABLE.
  ExpirationTime TIMESTAMPTZ GENERATED ALWAYS AS (((CreateTime AT TIME ZONE 'UTC') + INTERVAL '30 days') AT TIME ZONE 'UTC') STORED,
  PRIMARY KEY (Xid, WorkUnitId, HistoryId)
);

-- Sliding-window queue velocity and turnover rate queries.
CREATE INDEX WorkUnitStatusHistoryByQueueAndCreateTime ON WorkUnitStatusHistory (QueueName, CreateTime DESC, NewState, PreviousState)
  INCLUDE (ResourceType, ResourceUnits, Xid, WorkUnitId, ExecutionId);

-- Singleton table persisting the global scheduling pause toggle and resumption metadata.
CREATE TABLE PauseSchedulingState (
  Id BIGINT NOT NULL, -- Singleton key. Always 1.
  SchedulingPaused BOOLEAN NOT NULL DEFAULT FALSE,
  ResumptionRunId TEXT, -- Last triggered resumption run. NULL while paused.
  ResumptionWorkerId TEXT, -- Worker currently holding the run, for fencing stale workers.
  UpdatedAt TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (Id),
  CONSTRAINT Check_Id_Is_One CHECK (Id = 1)
);

-- Tasks intercepted by the Actuator while scheduling is paused.
CREATE TABLE PausedTasks (
  Xid BIGINT NOT NULL,
  WorkUnitId BIGINT NOT NULL,
  TaskId TEXT NOT NULL,
  Target TEXT NOT NULL, -- "experiments/{xid}/workUnits/{wid}/executions/{execution_id}"
  ActionProto BYTEA NOT NULL, -- cloud.xmanager.v1.Action
  OperationName TEXT NOT NULL,
  TraceId TEXT NOT NULL,
  Priority BIGINT NOT NULL, -- Higher values are replayed first.
  CreateTime TIMESTAMPTZ NOT NULL,
  ProcessedTime TIMESTAMPTZ, -- NULL while pending.
  ResolutionStatus TEXT, -- e.g. replayed, skipped.
  ResumptionRunId TEXT,
  IsPending BOOLEAN GENERATED ALWAYS AS (CASE WHEN ProcessedTime IS NULL THEN TRUE ELSE NULL END) STORED,
  -- 7 days after resolution; NULL while pending. UTC conversion keeps the expression IMMUTABLE.
  ExpirationTime TIMESTAMPTZ GENERATED ALWAYS AS (((ProcessedTime AT TIME ZONE 'UTC') + INTERVAL '7 days') AT TIME ZONE 'UTC') STORED,
  PRIMARY KEY (Xid, WorkUnitId, TaskId),
  FOREIGN KEY (Xid, WorkUnitId) REFERENCES WorkUnits (Xid, WorkUnitId) ON DELETE CASCADE
);

-- Replay ordering index for pending paused tasks.
CREATE INDEX PausedTasksUnprocessed ON PausedTasks (Priority DESC, CreateTime)
  INCLUDE (ActionProto, Target, OperationName, TraceId, IsPending, Xid, WorkUnitId, TaskId) WHERE IsPending IS NOT NULL;
