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

declare module 'xmanager_cloud/experiment_state_server/proto/*' {
  export type StatusMessage = any;
  export const StatusMessage: any;

  export type ListStatusMessagesResponse = any;
  export const ListStatusMessagesResponse: any;

  export type ListExperimentsResponse = any;
  export const ListExperimentsResponse: any;

  export type Experiment = any;
  export const Experiment: any;

  export type WorkUnit = any;
  export const WorkUnit: any;

  export type Artifact = any;
  export const Artifact: any;

  export type Priority = any;
  export const Priority: any;

  export type ExperimentStatus = any;
  export const ExperimentStatus: any;

  export type WorkUnitStatus = any;
  export const WorkUnitStatus: any;

  export type StructuredMessage = any;
  export const StructuredMessage: any;

  export type QueueInfo = any;
  export const QueueInfo: any;

  export type ListArtifactsResponse = any;
  export const ListArtifactsResponse: any;

  export type ListArtifactsRequest = any;
  export const ListArtifactsRequest: any;

  export type ListExperimentsRequest = any;
  export const ListExperimentsRequest: any;

  export type UpdateExperimentRequest = any;
  export const UpdateExperimentRequest: any;

  export type ReplicatedJobStatus = any;
  export const ReplicatedJobStatus: any;

  export type ResourceSet = any;
  export const ResourceSet: any;

  export type Url = any;
  export const Url: any;

  const _default: any;
  export default _default;
}

declare module 'longrunning/*' {
  export type Operation = any;
  export const Operation: any;
  const _default: any;
  export default _default;
}
