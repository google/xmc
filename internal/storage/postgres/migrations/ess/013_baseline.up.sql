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

CREATE OR REPLACE FUNCTION immutable_array_to_string(arr TEXT[], sep TEXT) RETURNS TEXT
  LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT AS $$ SELECT array_to_string(arr, sep); $$;

CREATE TABLE Experiments (
  Xid BIGINT NOT NULL,
  Title TEXT NOT NULL,
  Title_Tokens TSVECTOR GENERATED ALWAYS AS (to_tsvector('simple'::regconfig, coalesce(Title, ''))) STORED,
  Creator TEXT NOT NULL,
  Creator_Tokens TSVECTOR GENERATED ALWAYS AS (to_tsvector('simple'::regconfig, coalesce(Creator, ''))) STORED,
  Priority BIGINT NOT NULL,
  Urls BYTEA[] NOT NULL,
  Tags TEXT[] NOT NULL,
  Tags_Tokens TSVECTOR GENERATED ALWAYS AS (to_tsvector('simple'::regconfig, coalesce(immutable_array_to_string(Tags, ' '), ''))) STORED,
  Notes TEXT NOT NULL,
  Resources BYTEA[] NOT NULL,
  Status BYTEA NOT NULL,
  ActiveState BIGINT NOT NULL,
  LaunchState BIGINT NOT NULL,
  CreateTime TIMESTAMPTZ NOT NULL,
  UpdateTime TIMESTAMPTZ NOT NULL,
  CompleteTime TIMESTAMPTZ,
  PRIMARY KEY (Xid)
);

CREATE INDEX ExperimentsByCreator ON Experiments (Creator);
CREATE INDEX ExperimentsSearchIndex ON Experiments USING GIN (Title_Tokens, Tags_Tokens, Creator_Tokens);

CREATE TABLE Artifacts (
  Xid BIGINT NOT NULL,
  WorkUnitId BIGINT,
  ArtifactId BIGINT NOT NULL,
  Url TEXT NOT NULL,
  Url_Tokens TSVECTOR GENERATED ALWAYS AS (to_tsvector('simple'::regconfig, coalesce(Url, ''))) STORED,
  Icon TEXT NOT NULL,
  DisplayName TEXT NOT NULL,
  DisplayName_Tokens TSVECTOR GENERATED ALWAYS AS (to_tsvector('simple'::regconfig, coalesce(DisplayName, ''))) STORED,
  Priority BIGINT NOT NULL,
  Tags TEXT[] NOT NULL,
  Tags_Tokens TSVECTOR GENERATED ALWAYS AS (to_tsvector('simple'::regconfig, coalesce(immutable_array_to_string(Tags, ' '), ''))) STORED,
  Notes TEXT NOT NULL,
  Notes_Tokens TSVECTOR GENERATED ALWAYS AS (to_tsvector('simple'::regconfig, coalesce(Notes, ''))) STORED,
  LifecyclePhase BIGINT NOT NULL,
  DataType BIGINT NOT NULL,
  MimeType TEXT NOT NULL,
  MimeType_Tokens TSVECTOR GENERATED ALWAYS AS (to_tsvector('simple'::regconfig, coalesce(MimeType, ''))) STORED,
  AdditionalInfo BYTEA,
  Payload BYTEA,
  PayloadType TEXT NOT NULL DEFAULT '',
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

CREATE TABLE WorkUnits (
  Xid BIGINT NOT NULL,
  WorkUnitId BIGINT NOT NULL,
  WorkUnitType BIGINT NOT NULL,
  Priority BIGINT NOT NULL,
  Urls BYTEA[] NOT NULL,
  Tags TEXT[] NOT NULL,
  Notes TEXT NOT NULL,
  Executable BYTEA NOT NULL,
  ExecutionCreateTime TIMESTAMPTZ NOT NULL,
  ExecutionUpdateTime TIMESTAMPTZ NOT NULL,
  ExecutionCompleteTime TIMESTAMPTZ,
  ExecutionId BIGINT NOT NULL,
  ExecutionMetadata BYTEA NOT NULL,
  WorkUnitStatus BYTEA NOT NULL,
  QueueName TEXT NOT NULL,
  ExecutionState BIGINT NOT NULL,
  ExecutionStatusMessageSeverity BIGINT,
  PositionInQueue BIGINT NOT NULL DEFAULT 9223372036854775807,
  IsPendingOrRunningJob BOOLEAN GENERATED ALWAYS AS (CASE WHEN ExecutionState IN (1, 2, 3, 4) THEN TRUE ELSE NULL END) STORED,
  SortPhase BIGINT GENERATED ALWAYS AS (CASE WHEN ExecutionState IN (1, 3, 4) THEN 1 WHEN ExecutionState IN (2) THEN 0 ELSE -1 END) STORED,
  CreateTime TIMESTAMPTZ NOT NULL,
  UpdateTime TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (Xid, WorkUnitId),
  FOREIGN KEY (Xid) REFERENCES Experiments (Xid) ON DELETE CASCADE
);

CREATE INDEX WorkUnitsByQueueName ON WorkUnits (QueueName);
CREATE INDEX WorkUnitsByQueueAndPosition ON WorkUnits (QueueName, SortPhase DESC, PositionInQueue ASC, CreateTime ASC, WorkUnitId ASC)
  INCLUDE (IsPendingOrRunningJob, Xid) WHERE IsPendingOrRunningJob IS NOT NULL;

CREATE TABLE Operations (
  Xid BIGINT NOT NULL,
  WorkUnitId BIGINT NOT NULL,
  OperationId TEXT NOT NULL DEFAULT (gen_random_uuid()::text),
  Done BOOLEAN NOT NULL,
  Status BYTEA,
  Response BYTEA,
  OperationMetadata BYTEA NOT NULL,
  CreateTime TIMESTAMPTZ NOT NULL,
  UpdateTime TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (Xid, WorkUnitId, OperationId),
  FOREIGN KEY (Xid, WorkUnitId) REFERENCES WorkUnits (Xid, WorkUnitId) ON DELETE CASCADE
);

CREATE TABLE WorkUnitExecutions (
  Xid BIGINT NOT NULL,
  WorkUnitId BIGINT NOT NULL,
  ExecutionId BIGINT NOT NULL,
  ExecutionMetadata BYTEA NOT NULL,
  WorkUnitStatus BYTEA NOT NULL,
  CreateTime TIMESTAMPTZ NOT NULL,
  UpdateTime TIMESTAMPTZ NOT NULL,
  CompleteTime TIMESTAMPTZ,
  PRIMARY KEY (Xid, WorkUnitId, ExecutionId),
  FOREIGN KEY (Xid, WorkUnitId) REFERENCES WorkUnits (Xid, WorkUnitId) ON DELETE CASCADE
);

CREATE TABLE StatusMessages (
  Xid BIGINT NOT NULL,
  WorkUnitId BIGINT,
  MessageId BIGINT NOT NULL,
  ExecutionId BIGINT,
  Severity BIGINT NOT NULL,
  Source BIGINT NOT NULL,
  UserFacingText TEXT NOT NULL,
  UserFacingUrl BYTEA,
  CreateTime TIMESTAMPTZ NOT NULL,
  PreviousMessage BYTEA,
  AuxiliaryInfo BYTEA,
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
  ActiveName TEXT GENERATED ALWAYS AS (CASE WHEN State != 5 THEN Name ELSE NULL END) STORED,
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

CREATE TABLE WorkUnitStatusHistory (
  Xid BIGINT NOT NULL,
  WorkUnitId BIGINT NOT NULL,
  HistoryId BIGINT NOT NULL,
  ExecutionId BIGINT,
  QueueName TEXT,
  PreviousState BIGINT,
  NewState BIGINT NOT NULL,
  ResourceType TEXT,
  ResourceUnits BIGINT,
  CreateTime TIMESTAMPTZ NOT NULL,
  ExpirationTime TIMESTAMPTZ GENERATED ALWAYS AS (((CreateTime AT TIME ZONE 'UTC') + INTERVAL '30 days') AT TIME ZONE 'UTC') STORED,
  PRIMARY KEY (Xid, WorkUnitId, HistoryId)
);

CREATE INDEX WorkUnitStatusHistoryByQueueAndCreateTime ON WorkUnitStatusHistory (QueueName, CreateTime DESC, NewState, PreviousState)
  INCLUDE (ResourceType, ResourceUnits, Xid, WorkUnitId, ExecutionId);

CREATE TABLE PauseSchedulingState (
  Id BIGINT NOT NULL,
  SchedulingPaused BOOLEAN NOT NULL DEFAULT FALSE,
  ResumptionRunId TEXT,
  ResumptionWorkerId TEXT,
  UpdatedAt TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (Id),
  CONSTRAINT Check_Id_Is_One CHECK (Id = 1)
);

CREATE TABLE PausedTasks (
  Xid BIGINT NOT NULL,
  WorkUnitId BIGINT NOT NULL,
  TaskId TEXT NOT NULL,
  Target TEXT NOT NULL,
  ActionProto BYTEA NOT NULL,
  OperationName TEXT NOT NULL,
  TraceId TEXT NOT NULL,
  Priority BIGINT NOT NULL,
  CreateTime TIMESTAMPTZ NOT NULL,
  ProcessedTime TIMESTAMPTZ,
  ResolutionStatus TEXT,
  ResumptionRunId TEXT,
  IsPending BOOLEAN GENERATED ALWAYS AS (CASE WHEN ProcessedTime IS NULL THEN TRUE ELSE NULL END) STORED,
  ExpirationTime TIMESTAMPTZ GENERATED ALWAYS AS (((ProcessedTime AT TIME ZONE 'UTC') + INTERVAL '7 days') AT TIME ZONE 'UTC') STORED,
  PRIMARY KEY (Xid, WorkUnitId, TaskId),
  FOREIGN KEY (Xid, WorkUnitId) REFERENCES WorkUnits (Xid, WorkUnitId) ON DELETE CASCADE
);

CREATE INDEX PausedTasksUnprocessed ON PausedTasks (Priority DESC, CreateTime)
  INCLUDE (ActionProto, Target, OperationName, TraceId, IsPending, Xid, WorkUnitId, TaskId) WHERE IsPending IS NOT NULL;
