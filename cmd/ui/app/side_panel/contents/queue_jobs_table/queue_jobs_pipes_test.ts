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

import { ResourceType } from 'app/common/components/resource_chip/resource_chip';
import { ResourceChipData } from 'app/common/components/resource_chip/resource_set_to_resource_chips';
import { StatusOption } from 'app/common/components/status_chip/status_chip';
import { DashboardJob, Resource } from 'app/protos/rod';

import {
  QueueJobPositionPipe,
  QueueJobResourcesPipe,
  QueueJobStatusPipe,
} from './queue_jobs_pipes';

describe('QueueJobStatusPipe', () => {
  let pipe: QueueJobStatusPipe;

  beforeEach(() => {
    pipe = new QueueJobStatusPipe();
  });

  const testCases: Array<{
    input?: DashboardJob | null;
    expected: StatusOption;
    description: string;
  }> = [
    {
      input: { status: 'RUNNING' },
      expected: StatusOption.RUNNING,
      description: 'uppercase RUNNING',
    },
    {
      input: { status: 'running' },
      expected: StatusOption.RUNNING,
      description: 'lowercase running',
    },
    {
      input: { status: 'STARTING' },
      expected: StatusOption.STARTING,
      description: 'STARTING',
    },
    {
      input: { status: 'SCHEDULING' },
      expected: StatusOption.SCHEDULING,
      description: 'SCHEDULING',
    },
    {
      input: { status: 'INITIALIZING' },
      expected: StatusOption.INITIALIZING,
      description: 'INITIALIZING',
    },
    {
      input: { status: 'SUCCEEDED' },
      expected: StatusOption.COMPLETED,
      description: 'SUCCEEDED',
    },
    {
      input: { status: 'COMPLETED' },
      expected: StatusOption.COMPLETED,
      description: 'COMPLETED',
    },
    {
      input: { status: 'FAILED' },
      expected: StatusOption.FAILED,
      description: 'FAILED',
    },
    {
      input: { status: 'CANCELLED' },
      expected: StatusOption.STOPPED,
      description: 'CANCELLED',
    },
    {
      input: { status: 'STOPPED' },
      expected: StatusOption.STOPPED,
      description: 'STOPPED',
    },
    {
      input: undefined,
      expected: StatusOption.PENDING,
      description: 'undefined job',
    },
    {
      input: { status: '' },
      expected: StatusOption.PENDING,
      description: 'empty status string',
    },
    {
      input: { status: 'UNKNOWN_STATUS' },
      expected: StatusOption.PENDING,
      description: 'unknown status',
    },
  ];

  for (const tc of testCases) {
    it(`should transform ${tc.description} to ${tc.expected}`, () => {
      expect(pipe.transform(tc.input)).toBe(tc.expected);
    });
  }
});

describe('QueueJobPositionPipe', () => {
  let pipe: QueueJobPositionPipe;

  beforeEach(() => {
    pipe = new QueueJobPositionPipe();
  });

  const testCases: Array<{
    input?: DashboardJob | null;
    expected: string;
    description: string;
  }> = [
    {
      input: { status: 'RUNNING', positionInQueue: 3 },
      expected: 'N/A - running',
      description: 'RUNNING job',
    },
    {
      input: { status: 'STARTING', positionInQueue: 5 },
      expected: 'N/A - running',
      description: 'STARTING job',
    },
    {
      input: { status: 'INITIALIZING', positionInQueue: 2 },
      expected: 'N/A - running',
      description: 'INITIALIZING job',
    },
    {
      input: { status: 'SUCCEEDED', positionInQueue: 0 },
      expected: '-',
      description: 'SUCCEEDED job',
    },
    {
      input: { status: 'COMPLETED', positionInQueue: 0 },
      expected: '-',
      description: 'COMPLETED job',
    },
    {
      input: { status: 'SCHEDULING', positionInQueue: 5 },
      expected: '5',
      description: 'SCHEDULING job with positionInQueue 5',
    },
    {
      input: { status: 'PENDING', positionInQueue: 0 },
      expected: '-',
      description: 'PENDING job with positionInQueue 0',
    },
    {
      input: { status: 'QUEUED', positionInQueue: 12 },
      expected: '12',
      description: 'QUEUED job with positionInQueue 12',
    },
    {
      input: { status: 'SCHEDULING', positionInQueue: -1 },
      expected: '-',
      description: 'negative position',
    },
    {
      input: { status: 'PENDING', positionInQueue: null as any },
      expected: '-',
      description: 'null position',
    },
    {
      input: { status: 'PENDING' },
      expected: '-',
      description: 'undefined position field',
    },
    { input: null, expected: '-', description: 'null job object' },
  ];

  for (const tc of testCases) {
    it(`should return "${tc.expected}" for ${tc.description}`, () => {
      expect(pipe.transform(tc.input)).toBe(tc.expected);
    });
  }
});

describe('QueueJobResourcesPipe', () => {
  let pipe: QueueJobResourcesPipe;

  beforeEach(() => {
    pipe = new QueueJobResourcesPipe();
  });

  it('should return empty array for null/undefined input', () => {
    expect(pipe.transform(null)).toEqual([]);
    expect(pipe.transform(undefined)).toEqual([]);
  });

  it('should return empty array when requestedResources is empty or missing', () => {
    expect(pipe.transform([])).toEqual([]);
  });

  it('should transform CPU resources correctly', () => {
    const singleCore = pipe.transform([{ resourceType: 'cpu', count: 1 }]);
    expect(singleCore).toEqual([{ resourceType: ResourceType.CPU, text: 'cpu: 1' }]);

    const multiCore = pipe.transform([{ resourceType: 'cpu', count: 3000000 }]);
    expect(multiCore).toEqual([{ resourceType: ResourceType.CPU, text: 'cpu: 3000000' }]);
  });

  it('should transform Memory resources correctly', () => {
    const gbMemory = pipe.transform([{ resourceType: 'memory', count: 17179869184 }]);
    expect(gbMemory).toEqual([{ resourceType: ResourceType.MEMORY, text: 'memory: 17179869184' }]);

    const pbMemory = pipe.transform([{ resourceType: 'memory', count: 3298886726347889 }]);
    expect(pbMemory).toEqual([
      { resourceType: ResourceType.MEMORY, text: 'memory: 3298886726347889' },
    ]);
  });

  it('should transform GPU, TPU, and custom resources correctly', () => {
    const h100Gpu = pipe.transform([{ resourceType: 'H100', count: 4 }]);
    expect(h100Gpu).toEqual([{ resourceType: ResourceType.GPU, text: '4 x H100' }]);

    const tpuV4 = pipe.transform([{ resourceType: 'TPU v4', count: 16 }]);
    expect(tpuV4).toEqual([{ resourceType: ResourceType.TPU, text: '16 x TPU v4' }]);

    const k8sGpu = pipe.transform([{ resourceType: 'nvidia.com/gpu', count: 4 }]);
    expect(k8sGpu).toEqual([{ resourceType: ResourceType.GPU, text: '4 x nvidia.com/gpu' }]);
  });

  it('should transform multiple resources from an array of Resource objects', () => {
    const arrayInput: Resource[] = [
      { resourceType: 'cpu', count: 3000000 },
      { resourceType: 'memory', count: 3298886726347889 },
      { resourceType: 'H100', count: 4 },
    ];
    const result = pipe.transform(arrayInput);
    expect(result).toEqual([
      { resourceType: ResourceType.CPU, text: 'cpu: 3000000' },
      { resourceType: ResourceType.MEMORY, text: 'memory: 3298886726347889' },
      { resourceType: ResourceType.GPU, text: '4 x H100' },
    ]);
  });
});
