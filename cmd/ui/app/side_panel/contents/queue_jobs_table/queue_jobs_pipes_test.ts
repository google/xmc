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

import { DashboardJob } from 'xmanager_cloud/api/protos/rod/api.jsonpb_decls';
import { StatusOption } from 'xmanager_cloud/ui/app/common/components/status_chip/status_chip';

import { QueueJobPositionPipe, QueueJobStatusPipe } from './queue_jobs_pipes';

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
