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
import { ResourceSet } from 'app/protos/experiment_state_server';

import { ResourceType } from '../resource_chip';
import {
  ResourceChipData,
  resourceSetsToResourceChipsData,
} from './resource_set_to_resource_chips';

describe('resourceSetsToResourceChipsData', () => {
  it('should return an empty array for empty input', () => {
    expect(resourceSetsToResourceChipsData([])).toEqual([]);
  });

  it('should return an empty array if a ResourceSet has no relevant resources', () => {
    const resources: ResourceSet[] = [{}];
    expect(resourceSetsToResourceChipsData(resources)).toEqual([]);
  });

  it('should convert CPU resources correctly', () => {
    const resources: ResourceSet[] = [
      {
        cpuResources: { cpuMillicores: '1500', memoryBytes: '1073741824' },
      },
    ];
    const expected: ResourceChipData[] = [
      { resourceType: ResourceType.CPU, text: '1.5 cores' },
      { resourceType: ResourceType.MEMORY, text: '1 GB' },
    ];
    expect(resourceSetsToResourceChipsData(resources)).toEqual(expected);
  });

  it('should handle CPU resources with zero millicores', () => {
    const resources: ResourceSet[] = [
      {
        cpuResources: { cpuMillicores: '0', memoryBytes: '512' },
      },
    ];
    const expected: ResourceChipData[] = [
      { resourceType: ResourceType.CPU, text: '0 cores' },
      { resourceType: ResourceType.MEMORY, text: '512 B' },
    ];
    expect(resourceSetsToResourceChipsData(resources)).toEqual(expected);
  });

  it('should handle CPU resources with missing millicores', () => {
    const resources: ResourceSet[] = [
      {
        cpuResources: { memoryBytes: '256' },
      },
    ];
    const expected: ResourceChipData[] = [
      { resourceType: ResourceType.CPU, text: '0 cores' },
      { resourceType: ResourceType.MEMORY, text: '256 B' },
    ];
    expect(resourceSetsToResourceChipsData(resources)).toEqual(expected);
  });

  it('should handle CPU resources with missing or negative memoryBytes', () => {
    const resources: ResourceSet[] = [
      { cpuResources: { cpuMillicores: '1000' } },
      { cpuResources: { cpuMillicores: '1000', memoryBytes: '-1024' } },
    ];
    const expected: ResourceChipData[] = [
      { resourceType: ResourceType.CPU, text: '1 core' },
      { resourceType: ResourceType.MEMORY, text: '0 B' },
      { resourceType: ResourceType.CPU, text: '1 core' },
      { resourceType: ResourceType.MEMORY, text: '0 B' },
    ];
    expect(resourceSetsToResourceChipsData(resources)).toEqual(expected);
  });

  it('should cap memory unit at YB for extremely large memoryBytes', () => {
    const resources: ResourceSet[] = [
      {
        cpuResources: {
          cpuMillicores: '1000',
          memoryBytes: String(Math.pow(1024, 9)),
        },
      },
    ];
    const expected: ResourceChipData[] = [
      { resourceType: ResourceType.CPU, text: '1 core' },
      { resourceType: ResourceType.MEMORY, text: '1024 YB' },
    ];
    expect(resourceSetsToResourceChipsData(resources)).toEqual(expected);
  });

  it('should convert GPU resources correctly', () => {
    const resources: ResourceSet[] = [
      {
        gpuResources: { count: '2', type: 'NVIDIA_A100' },
      },
    ];
    const expected: ResourceChipData[] = [
      { resourceType: ResourceType.GPU, text: '2 x NVIDIA_A100' },
    ];
    expect(resourceSetsToResourceChipsData(resources)).toEqual(expected);
  });

  it('should convert TPU resources correctly', () => {
    const resources: ResourceSet[] = [
      {
        tpuResources: { topology: '2x2', type: 'V2' },
      },
    ];
    const expected: ResourceChipData[] = [{ resourceType: ResourceType.GPU, text: '2x2 x V2' }];
    expect(resourceSetsToResourceChipsData(resources)).toEqual(expected);
  });

  it('should handle mixed resources in a single ResourceSet', () => {
    const resources: ResourceSet[] = [
      {
        cpuResources: { cpuMillicores: '1000', memoryBytes: '2048' },
        gpuResources: { count: '1', type: 'NVIDIA_V100' },
      },
    ];
    const expected: ResourceChipData[] = [
      { resourceType: ResourceType.CPU, text: '1 core' },
      { resourceType: ResourceType.MEMORY, text: '2 KB' },
      { resourceType: ResourceType.GPU, text: '1 x NVIDIA_V100' },
    ];
    expect(resourceSetsToResourceChipsData(resources)).toEqual(expected);
  });

  it('should handle multiple ResourceSet objects and flatten the result', () => {
    const resources: ResourceSet[] = [
      { cpuResources: { cpuMillicores: '500', memoryBytes: '128' } },
      { gpuResources: { count: '4', type: 'NVIDIA_A100' } },
      { tpuResources: { topology: '4x4', type: 'V3' } },
      { cpuResources: { cpuMillicores: '2000', memoryBytes: '4096' } },
    ];
    const expected: ResourceChipData[] = [
      { resourceType: ResourceType.CPU, text: '0.5 cores' },
      { resourceType: ResourceType.MEMORY, text: '128 B' },
      { resourceType: ResourceType.GPU, text: '4 x NVIDIA_A100' },
      { resourceType: ResourceType.GPU, text: '4x4 x V3' },
      { resourceType: ResourceType.CPU, text: '2 cores' },
      { resourceType: ResourceType.MEMORY, text: '4 KB' },
    ];
    expect(resourceSetsToResourceChipsData(resources)).toEqual(expected);
  });
});
