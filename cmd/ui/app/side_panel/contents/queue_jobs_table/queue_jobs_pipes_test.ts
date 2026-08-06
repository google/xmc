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

import 'jasmine';

import { StatusOption } from 'xmanager_cloud/ui/app/common/components/status_chip/status_chip';

import { QueueJobPositionPipe, QueueJobStatusPipe } from './queue_jobs_pipes';

describe('QueueJobStatusPipe', () => {
  let pipe: QueueJobStatusPipe;

  beforeEach(() => {
    pipe = new QueueJobStatusPipe();
  });

  const testCases: Array<{
    input?: string;
    expected: StatusOption;
    description: string;
  }> = [
    {
      input: 'RUNNING',
      expected: StatusOption.RUNNING,
      description: 'uppercase RUNNING',
    },
    {
      input: 'running',
      expected: StatusOption.RUNNING,
      description: 'lowercase running',
    },
    {
      input: 'STARTING',
      expected: StatusOption.STARTING,
      description: 'STARTING',
    },
    {
      input: 'SCHEDULING',
      expected: StatusOption.SCHEDULING,
      description: 'SCHEDULING',
    },
    {
      input: 'INITIALIZING',
      expected: StatusOption.INITIALIZING,
      description: 'INITIALIZING',
    },
    {
      input: 'SUCCEEDED',
      expected: StatusOption.COMPLETED,
      description: 'SUCCEEDED',
    },
    {
      input: 'COMPLETED',
      expected: StatusOption.COMPLETED,
      description: 'COMPLETED',
    },
    {
      input: 'FAILED',
      expected: StatusOption.FAILED,
      description: 'FAILED',
    },
    {
      input: 'CANCELLED',
      expected: StatusOption.STOPPED,
      description: 'CANCELLED',
    },
    {
      input: 'STOPPED',
      expected: StatusOption.STOPPED,
      description: 'STOPPED',
    },
    {
      input: undefined,
      expected: StatusOption.PENDING,
      description: 'undefined status',
    },
    {
      input: '',
      expected: StatusOption.PENDING,
      description: 'empty status string',
    },
    {
      input: 'UNKNOWN_STATUS',
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
    input?: {
      status?: string;
      positionInQueue?: number | string | null;
      position?: number | string | null;
    } | null;
    expected: string;
    description: string;
  }> = [
    {
      input: { status: 'RUNNING', positionInQueue: 3 },
      expected: 'N/A - running',
      description: 'RUNNING job',
    },
    {
      input: { status: 'ACTIVE', positionInQueue: 1 },
      expected: 'N/A - running',
      description: 'ACTIVE job',
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
      expected: '0',
      description: 'SUCCEEDED job',
    },
    {
      input: { status: 'COMPLETED', positionInQueue: 0 },
      expected: '0',
      description: 'COMPLETED job',
    },
    {
      input: { status: 'SCHEDULING', positionInQueue: 5 },
      expected: '5',
      description: 'SCHEDULING job with positionInQueue 5',
    },
    {
      input: { status: 'PENDING', position: 0 },
      expected: '0',
      description: 'PENDING job with position 0',
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
      input: { status: 'PENDING', positionInQueue: null },
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
