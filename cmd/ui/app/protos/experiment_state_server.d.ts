// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

export interface Experiment {
  id?: string;
  name?: string;
  [key: string]: any;
}

export interface WorkUnit {
  id?: string;
  [key: string]: any;
}

export interface ListExperimentsRequest {
  [key: string]: any;
}

export interface ListExperimentsResponse {
  experiments?: any[];
  [key: string]: any;
}

export interface ListStatusMessagesRequest {
  [key: string]: any;
}

export interface ListStatusMessagesResponse {
  statusMessages?: any[];
  [key: string]: any;
}

export interface ListArtifactsRequest {
  [key: string]: any;
}

export interface ListArtifactsResponse {
  artifacts?: any[];
  [key: string]: any;
}

export interface ListWorkUnitsRequest {
  [key: string]: any;
}

export interface ListWorkUnitsResponse {
  workUnits?: any[];
  [key: string]: any;
}

export interface GetWorkUnitRequest {
  [key: string]: any;
}

export interface UpdateExperimentRequest {
  [key: string]: any;
}

export namespace WorkUnitStatus {
  export type WorkUnitState = string;
}

export interface WorkUnitStatus {
  [key: string]: any;
}

export namespace ExperimentStatus {
  export interface StateCount {
    [key: string]: any;
  }
  export type ExperimentState = string;
}

export interface ExperimentStatus {
  [key: string]: any;
}

export interface ReplicatedJobStatus {
  [key: string]: any;
}

export type Priority = string;

export namespace StructuredMessage {
  export type Severity = 'WARNING' | 'ERROR' | 'FATAL' | 'INFO' | 'DEBUG' | string;
  export type Source =
    | 'SOURCE_UNSPECIFIED'
    | 'XMANAGER_CLIENT'
    | 'XMANAGER_BACKEND'
    | 'ADAPTER'
    | 'KUBERNETES'
    | 'SCHEDULER'
    | 'APPLICATION'
    | 'USER_DEFINED'
    | string;
}

export interface StructuredMessage {
  [key: string]: any;
}

export interface StatusMessage {
  [key: string]: any;
}

export interface QueueInfo {
  [key: string]: any;
}

export namespace Artifact {
  export type DataType = string;
  export type LifecyclePhase = string;
}

export interface Artifact {
  [key: string]: any;
}

export interface ChartArtifact {
  [key: string]: any;
}

export interface CodeSourceArtifact {
  [key: string]: any;
}

export interface ManagedXprofArtifact {
  [key: string]: any;
}

export interface TextArtifact {
  [key: string]: any;
}

export interface UrlArtifact {
  [key: string]: any;
}

export interface ResourceSet {
  [key: string]: any;
}

export interface Url {
  priority?: number;
  processedUrl?: string;
  icon?: any;
  [key: string]: any;
}
