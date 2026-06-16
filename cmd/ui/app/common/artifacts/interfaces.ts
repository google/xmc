// Copyright 2025 Google LLC
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
import { Artifact } from 'xmanager_cloud/experiment_state_server/proto/artifact.jsonpb_decls';

/** Represents a collapsible group of artifacts of the same type. */
export interface ArtifactGroup {
  type: 'group';
  dataType: Artifact.DataType;
  artifacts: Artifact[];
  groupId: string; // e.g. 'INPUT-MODEL_SNAPSHOTS'
  icon: string;
}

/** Represents a single artifact that is not part of a group. */
export interface ArtifactSingle {
  type: 'single';
  artifact: Artifact;
}

/** Union type for items in the artifact list. */
export type ArtifactListItem = ArtifactGroup | ArtifactSingle;
