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

import { ArtifactListItem, PayloadType, TypedArtifact } from './interfaces';

export interface ArtifactDisplayProperties {
  typeName: string;
  icon: string;
  url: string;
  displayName: string;
}

/**
 * Converts a raw Artifact to a TypedArtifact directly.
 *
 * Note: The `complexity` lint rule is disabled here because TypeScript requires
 * explicit type narrowing (via repetitive `in` operator checks) to properly
 * discriminate the union fields mapped from an unguaranteed JSON payload.
 * Extracting this into nested validations hides the type narrows from the parent context.
 */
// eslint-disable-next-line complexity
export function toTypedArtifact(artifact: Artifact): TypedArtifact {
  const payload = artifact.payload;
  if (payload) {
    if ('managedXprof' in payload && payload.managedXprof) {
      return { type: PayloadType.MANAGED_XPROF, artifact, payload: payload.managedXprof };
    }
    if ('codeSource' in payload && payload.codeSource) {
      return { type: PayloadType.CODE_SOURCE, artifact, payload: payload.codeSource };
    }
    if ('text' in payload && payload.text) {
      return { type: PayloadType.TEXT, artifact, payload: payload.text };
    }
    if ('url' in payload && payload.url) {
      return { type: PayloadType.URL, artifact, payload: payload.url };
    }
    if ('chart' in payload && payload.chart) {
      return { type: PayloadType.CHART, artifact, payload: payload.chart };
    }
  }
  return { type: PayloadType.UNKNOWN, artifact };
}

/**
 * Returns UI display properties (icon, url, type name, display name)
 * for the given typed artifact.
 *
 * Note: `complexity` and `max-lines-per-function` lint rules are disabled here.
 * Abstracting this discrete mapping logic into lookup maps or separate smaller
 * functions would force the usage of non-specific types (like `any` or `Record<string, unknown>`),
 * permanently destroying TypeScript's ability to strictly infer and validate
 * the underlying payload structures via discriminated unions on the `switch` statement.
 */
// eslint-disable-next-line complexity, max-lines-per-function
export function getArtifactDisplayProperties(
  typedArtifact: TypedArtifact,
): ArtifactDisplayProperties {
  switch (typedArtifact.type) {
    case PayloadType.MANAGED_XPROF:
      return {
        typeName: 'Managed Xprof',
        icon: 'query_stats',
        url: typedArtifact.payload.xprof?.xprofUrl ?? '',
        displayName: typedArtifact.payload.displayName || '',
      };
    case PayloadType.CODE_SOURCE:
      return {
        typeName: 'Code Source',
        icon: 'code',
        url: typedArtifact.payload.codeUrl ?? '',
        displayName: typedArtifact.payload.title || '',
      };
    case PayloadType.TEXT:
      return {
        typeName: 'Text',
        icon: 'description',
        url: '',
        displayName: typedArtifact.payload.title || '',
      };
    case PayloadType.URL:
      return {
        typeName: 'URL',
        icon: typedArtifact.payload.icon || 'link',
        url: typedArtifact.payload.url ?? '',
        displayName: typedArtifact.payload.displayName || typedArtifact.payload.url || '',
      };
    case PayloadType.CHART:
      return {
        typeName: 'Chart',
        icon: 'bar_chart',
        url: '',
        displayName: typedArtifact.payload.title || 'Dashboard Chart',
      };
    case PayloadType.UNKNOWN:
    default:
      return {
        typeName: 'Unknown',
        icon: 'link',
        url: '',
        displayName: '',
      };
  }
}

/** Returns whether the typed artifact has payload content to display. */
export function hasPayloadContent(typedArtifact: TypedArtifact): boolean {
  return typedArtifact.type !== PayloadType.UNKNOWN;
}

/** Sorts artifacts by priority, then creation time. */
function sortArtifactsByPriority(typedArtifacts: TypedArtifact[]): TypedArtifact[] {
  return [...typedArtifacts].sort((a, b) => {
    const priorityA = a.artifact.priority ?? Number.MAX_SAFE_INTEGER;
    const priorityB = b.artifact.priority ?? Number.MAX_SAFE_INTEGER;
    if (priorityA !== priorityB) {
      return priorityA - priorityB;
    }

    // Tie-breaker: sort chronologically by creation time
    const timeA = a.artifact.createTime ? new Date(a.artifact.createTime).getTime() : 0;
    const timeB = b.artifact.createTime ? new Date(b.artifact.createTime).getTime() : 0;
    return timeA - timeB;
  });
}

function groupArtifactsByType(typedArtifacts: TypedArtifact[]): Map<PayloadType, TypedArtifact[]> {
  const artifactsByType = new Map<PayloadType, TypedArtifact[]>();
  for (const item of typedArtifacts) {
    const type = item.type;
    if (!artifactsByType.has(type)) {
      artifactsByType.set(type, []);
    }
    artifactsByType.get(type)!.push(item);
  }
  return artifactsByType;
}

function buildArtifactListItems(
  sortedArtifacts: TypedArtifact[],
  artifactsByType: Map<PayloadType, TypedArtifact[]>,
  phase: Artifact.LifecyclePhase | 'OUTPUT' | 'INPUT' | 'EXECUTION',
  groupingThreshold: number,
): ArtifactListItem[] {
  const result: ArtifactListItem[] = [];
  const groupedTypesProcessed = new Set<PayloadType>();
  for (const item of sortedArtifacts) {
    const type = item.type;
    const group = artifactsByType.get(type)!;
    if (group.length > groupingThreshold) {
      if (!groupedTypesProcessed.has(type)) {
        result.push({
          type: 'group',
          payloadType: type,
          artifacts: group,
          groupId: `${phase}-${type}`,
          icon: getArtifactDisplayProperties(group[0]).icon,
        });
        groupedTypesProcessed.add(type);
      }
    } else {
      result.push({ type: 'single', artifact: item });
    }
  }
  return result;
}

/** Groups artifacts by type if they exceed threshold, and sorts by priority. */
export function groupAndSortArtifacts(
  artifacts: TypedArtifact[],
  phase: Artifact.LifecyclePhase | 'OUTPUT' | 'INPUT' | 'EXECUTION',
  groupingThreshold: number,
): ArtifactListItem[] {
  const sorted = sortArtifactsByPriority(artifacts);
  const artifactsByType = groupArtifactsByType(sorted);
  return buildArtifactListItems(sorted, artifactsByType, phase, groupingThreshold);
}
