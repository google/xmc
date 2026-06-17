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

import { ArtifactListItem } from './interfaces';

const ARTIFACT_ICON_MAP: Record<string, string> = {
  'URL': 'link',
  'FILE': 'insert_drive_file',
  'DIRECTORY': 'folder',
  'MODEL_SNAPSHOTS': 'save',
  'INFERENCE_MODEL': 'hub',
  'ML_DATASET': 'dataset',
  'EMBEDDING': 'data_array',
  'VIDEO': 'videocam',
  'IMAGE': 'image',
  'AUDIO': 'audiotrack',
  'HTML_REPORT': 'html',
  'TEXT': 'description',
  'POINT_CLOUD': 'grain',
  'MESH': 'view_in_ar',
  'DATA_SCHEMA': 'schema',
  'DATA_STATISTICS': 'monitoring',
  'CONTAINER_IMAGE': 'developer_board',
  'CODE_SOURCE': 'code',
  'CONFIGURATION': 'settings',
  'EVENT_LOGS': 'receipt_long',
  'METRICS': 'analytics',
  'TEST_RESULTS': 'checklist',
  'PROMPT_TEMPLATE': 'article',
  'VECTOR_INDEX': 'search',
  'TOKENIZER': 'token',
  'PIPELINE_GRAPH': 'account_tree',
};

/** Returns the icon for an artifact. */
export function getArtifactIcon(artifact: Artifact): string {
  if (artifact.url?.icon) {
    return artifact.url.icon;
  }
  return ARTIFACT_ICON_MAP[artifact.dataType || ''] ?? 'link';
}

const SPECIAL_DATA_TYPE_LABELS: Record<string, string> = {
  'DATA_TYPE_UNSPECIFIED': 'Unspecified',
  'URL': 'URL',
  'ML_DATASET': 'ML Dataset',
  'HTML_REPORT': 'HTML Report',
};

/** Formats artifact data type for display. */
export function formatArtifactDataType(dataType: Artifact.DataType | undefined): string {
  if (!dataType) {
    return 'Unspecified';
  }
  const specialLabel = SPECIAL_DATA_TYPE_LABELS[dataType];
  if (specialLabel) {
    return specialLabel;
  }
  return dataType
    .split('_')
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1).toLowerCase())
    .join(' ');
}

/** Groups artifacts by type if they exceed threshold, and sorts by priority. */
export function groupAndSortArtifacts(
  artifacts: Artifact[],
  phase: Artifact.LifecyclePhase | 'OUTPUT' | 'INPUT' | 'EXECUTION',
  groupingThreshold: number,
): ArtifactListItem[] {
  // 1. Sort by priority
  const sorted = [...artifacts].sort(
    (a, b) =>
      (a.url?.priority ?? Number.MAX_SAFE_INTEGER) - (b.url?.priority ?? Number.MAX_SAFE_INTEGER),
  );

  // 2. Group artifacts by type
  const artifactsByType = new Map<Artifact.DataType, Artifact[]>();
  for (const artifact of sorted) {
    const type = artifact.dataType || 'DATA_TYPE_UNSPECIFIED';
    if (!artifactsByType.has(type)) {
      artifactsByType.set(type, []);
    }
    artifactsByType.get(type)!.push(artifact);
  }

  // 3. Build result list with groups or singles
  const result: ArtifactListItem[] = [];
  const groupedTypesProcessed = new Set<Artifact.DataType>();
  for (const artifact of sorted) {
    const type = artifact.dataType || 'DATA_TYPE_UNSPECIFIED';
    const group = artifactsByType.get(type)!;
    if (group.length > groupingThreshold) {
      if (!groupedTypesProcessed.has(type)) {
        result.push({
          type: 'group',
          dataType: type,
          artifacts: group,
          groupId: `${phase}-${type}`,
          icon: getArtifactIcon(group[0]),
        });
        groupedTypesProcessed.add(type);
      }
    } else {
      result.push({ type: 'single', artifact });
    }
  }
  return result;
}
