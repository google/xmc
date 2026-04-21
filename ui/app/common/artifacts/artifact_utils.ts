import {Artifact} from 'xmanager_cloud/experiment_state_server/proto/artifact.jsonpb_decls';

import {ArtifactListItem} from './interfaces';

/** Returns the icon for an artifact. */
export function getArtifactIcon(artifact: Artifact): string {
  if (artifact.url?.icon) {
    return artifact.url.icon;
  }

  switch (artifact.dataType) {
    case 'URL':
      return 'link';
    case 'FILE':
      return 'insert_drive_file';
    case 'DIRECTORY':
      return 'folder';
    case 'MODEL_SNAPSHOTS':
      return 'save';
    case 'INFERENCE_MODEL':
      return 'hub';
    case 'ML_DATASET':
      return 'dataset';
    case 'EMBEDDING':
      return 'data_array';
    case 'VIDEO':
      return 'videocam';
    case 'IMAGE':
      return 'image';
    case 'AUDIO':
      return 'audiotrack';
    case 'HTML_REPORT':
      return 'html';
    case 'TEXT':
      return 'description';
    case 'POINT_CLOUD':
      return 'grain';
    case 'MESH':
      return 'view_in_ar';
    case 'DATA_SCHEMA':
      return 'schema';
    case 'DATA_STATISTICS':
      return 'monitoring';
    case 'CONTAINER_IMAGE':
      return 'developer_board';
    case 'CODE_SOURCE':
      return 'code';
    case 'CONFIGURATION':
      return 'settings';
    case 'EVENT_LOGS':
      return 'receipt_long';
    case 'METRICS':
      return 'analytics';
    case 'TEST_RESULTS':
      return 'checklist';
    case 'PROMPT_TEMPLATE':
      return 'article';
    case 'VECTOR_INDEX':
      return 'search';
    case 'TOKENIZER':
      return 'token';
    case 'PIPELINE_GRAPH':
      return 'account_tree';
    case 'DATA_TYPE_UNSPECIFIED':
    default:
      return 'link';
  }
}

/** Formats artifact data type for display. */
export function formatArtifactDataType(
    dataType: Artifact.DataType|undefined,
    ): string {
  if (!dataType) {
    return 'Unspecified';
  }
  switch (dataType) {
    case 'DATA_TYPE_UNSPECIFIED':
      return 'Unspecified';
    case 'URL':
      return 'URL';
    case 'ML_DATASET':
      return 'ML Dataset';
    case 'HTML_REPORT':
      return 'HTML Report';
    default:
      return dataType.split('_')
          .map(
              (word) =>
                  word.charAt(0).toUpperCase() + word.slice(1).toLowerCase(),
              )
          .join(' ');
  }
}

/** Groups artifacts by type if they exceed threshold, and sorts by priority. */
export function groupAndSortArtifacts(
    artifacts: Artifact[],
    phase: Artifact.LifecyclePhase|'OUTPUT'|'INPUT'|'EXECUTION',
    groupingThreshold: number): ArtifactListItem[] {
  // 1. Sort by priority
  const sorted = [...artifacts].sort(
      (a, b) => (a.url?.priority ?? Number.MAX_SAFE_INTEGER) -
          (b.url?.priority ?? Number.MAX_SAFE_INTEGER));

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
      result.push({type: 'single', artifact});
    }
  }
  return result;
}
