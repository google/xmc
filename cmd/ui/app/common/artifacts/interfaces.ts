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
import {
  Artifact,
  CodeSourceArtifact,
  ManagedXprofArtifact,
  TextArtifact,
  UrlArtifact,
  TestArtifact,
} from 'xmanager_cloud/experiment_state_server/proto/artifact.jsonpb_decls';

export enum PayloadType {
  MANAGED_XPROF = 'managedXprof',
  CODE_SOURCE = 'codeSource',
  TEXT = 'text',
  URL = 'url',
  TEST_ARTIFACT = 'testArtifact',
  UNKNOWN = 'unknown',
}

export interface BaseTypedArtifact {
  artifact: Artifact;
}

export interface ManagedXprofTypedArtifact extends BaseTypedArtifact {
  type: PayloadType.MANAGED_XPROF;
  payload: ManagedXprofArtifact;
}

export interface CodeSourceTypedArtifact extends BaseTypedArtifact {
  type: PayloadType.CODE_SOURCE;
  payload: CodeSourceArtifact;
}

export interface TextTypedArtifact extends BaseTypedArtifact {
  type: PayloadType.TEXT;
  payload: TextArtifact;
}

export interface UrlTypedArtifact extends BaseTypedArtifact {
  type: PayloadType.URL;
  payload: UrlArtifact;
}

export interface TestArtifactTypedArtifact extends BaseTypedArtifact {
  type: PayloadType.TEST_ARTIFACT;
  payload: TestArtifact;
}

export interface UnknownTypedArtifact extends BaseTypedArtifact {
  type: PayloadType.UNKNOWN;
}

export type TypedArtifact =
  | ManagedXprofTypedArtifact
  | CodeSourceTypedArtifact
  | TextTypedArtifact
  | UrlTypedArtifact
  | TestArtifactTypedArtifact
  | UnknownTypedArtifact;

/** Represents a collapsible group of artifacts of the same payload type. */
export interface ArtifactGroup {
  type: 'group';
  payloadType: PayloadType;
  artifacts: TypedArtifact[];
  groupId: string;
  icon: string;
}

/** Represents a single artifact that is not part of a group. */
export interface ArtifactSingle {
  type: 'single';
  artifact: TypedArtifact;
}

/** Union type for items in the artifact list. */
export type ArtifactListItem = ArtifactGroup | ArtifactSingle;
