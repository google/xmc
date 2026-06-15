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
import { numBytesToString } from 'javascript/closure/format/format';
import { MessageFormat } from 'javascript/closure/i18n/messageformat';
import { declareIcuTemplate } from 'javascript/closure/i18n/messages';
import { NumberFormat } from 'javascript/closure/i18n/numberformat';
import { ResourceSet } from 'xmanager_cloud/experiment_state_server/proto/resources.jsonpb_decls';

import { ResourceType } from '../resource_chip';

/** The data to display a resource chip */
export interface ResourceChipData {
  readonly resourceType: ResourceType;
  readonly text: string;
}

const MSG_CORE_COUNT_ICU = declareIcuTemplate(
  `{NUM_CORES, plural,
    =1 {{FORMATTED_CORES} core}
    other {{FORMATTED_CORES} cores}}`,
  {
    description: 'Displays the count of cores, handling singular vs plural.',
  },
);

/**  Converts a list of ResourceSet to a list of ResourceChipData. */
export function resourceSetsToResourceChipsData(resources: ResourceSet[]): ResourceChipData[] {
  const coreCountFormatter = new NumberFormat(NumberFormat.Format.COMPACT_SHORT);
  coreCountFormatter.setMaximumFractionDigits(1);
  const coreCountMessage = new MessageFormat(MSG_CORE_COUNT_ICU);

  return resources
    .map((resource: ResourceSet) => {
      const resources: ResourceChipData[] = [];
      if (resource.cpuResources) {
        const numCores = resource.cpuResources.cpuMillicores
          ? Number(resource.cpuResources.cpuMillicores) / 1000
          : 0;

        resources.push({
          resourceType: ResourceType.CPU,
          text: coreCountMessage.format({
            NUM_CORES: numCores,
            FORMATTED_CORES: coreCountFormatter.format(numCores),
          }),
        });
        resources.push({
          resourceType: ResourceType.MEMORY,
          text: numBytesToString(Number(resource.cpuResources.memoryBytes), 2),
        });
      }

      if (resource.gpuResources) {
        resources.push({
          resourceType: ResourceType.GPU,
          text: `${resource.gpuResources.count} x ${resource.gpuResources.type}`,
        });
      }

      if (resource.tpuResources) {
        resources.push({
          resourceType: ResourceType.GPU,
          text: `${resource.tpuResources.topology} x ${resource.tpuResources.type}`,
        });
      }

      return resources;
    })
    .flat();
}
