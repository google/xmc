import {Artifact} from 'xmanager_cloud/experiment_state_server/proto/artifact.jsonpb_decls';

/** Represents a collapsible group of artifacts of the same type. */
export interface ArtifactGroup {
  type: 'group';
  dataType: Artifact.DataType;
  artifacts: Artifact[];
  groupId: string;  // e.g. 'INPUT-MODEL_SNAPSHOTS'
  icon: string;
}

/** Represents a single artifact that is not part of a group. */
export interface ArtifactSingle {
  type: 'single';
  artifact: Artifact;
}

/** Union type for items in the artifact list. */
export type ArtifactListItem = ArtifactGroup|ArtifactSingle;
