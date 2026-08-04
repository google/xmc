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

import { QueueOverview, QueueResourceCapacity } from 'xmanager_cloud/api/protos/rod/api.jsonpb_decls';

import {
  FreeTotalDisplayPipe,
  MaxWorkloadDisplayPipe,
  PredictabilityMessagePipe,
  QueueStatusBadgePipe,
} from './resource_dashboard_pipes';

describe('Resource Dashboard Pipes', () => {
  describe('FreeTotalDisplayPipe', () => {
    let pipe: FreeTotalDisplayPipe;

    beforeEach(() => {
      pipe = new FreeTotalDisplayPipe();
    });

    it('should return "-" when capacity is undefined', () => {
      expect(pipe.transform(undefined)).toBe('-');
    });

    it('should format free and total capacity correctly', () => {
      const cap: QueueResourceCapacity = {
        resourceType: 'nvidia-tesla-h100',
        totalCapacity: 64,
        freeCapacity: 56,
      };
      expect(pipe.transform(cap)).toBe('56 / 64');
    });

    it('should handle missing free capacity gracefully', () => {
      const cap: Partial<QueueResourceCapacity> = { totalCapacity: 100 };
      expect(pipe.transform(cap as QueueResourceCapacity)).toBe('0 / 100');
    });
  });

  describe('MaxWorkloadDisplayPipe', () => {
    let pipe: MaxWorkloadDisplayPipe;

    beforeEach(() => {
      pipe = new MaxWorkloadDisplayPipe();
    });

    it('should return "-" when capacity is undefined', () => {
      expect(pipe.transform(undefined)).toBe('-');
    });

    it('should compute slot from freeCapacity and totalCapacity', () => {
      const cap: QueueResourceCapacity = {
        resourceType: 'nvidia-tesla-h100',
        freeCapacity: 32,
        totalCapacity: 64,
      };
      expect(pipe.transform(cap)).toBe('32 → 64');
    });
  });

  describe('PredictabilityMessagePipe', () => {
    let pipe: PredictabilityMessagePipe;

    beforeEach(() => {
      pipe = new PredictabilityMessagePipe();
    });

    it('should return null when queue is undefined or null', () => {
      expect(pipe.transform(undefined)).toBeNull();
      expect(pipe.transform(null)).toBeNull();
    });

    it('should return healthy message for valid queue', () => {
      const queue: QueueOverview = {
        queueName: 'gpu-cluster-queue',
      };
      expect(pipe.transform(queue)).toEqual({
        text: 'System is healthy. Job will start immediately.',
        isHealthy: true,
      });
    });
  });

  describe('QueueStatusBadgePipe', () => {
    let pipe: QueueStatusBadgePipe;

    beforeEach(() => {
      pipe = new QueueStatusBadgePipe();
    });

    it('should return null when queue is undefined or null', () => {
      expect(pipe.transform(undefined)).toBeNull();
      expect(pipe.transform(null)).toBeNull();
    });

    it('should return "Resources available" for valid queue', () => {
      const queue: QueueOverview = {
        queueName: 'gpu-cluster-queue',
      };
      expect(pipe.transform(queue)).toEqual({
        label: 'Resources available',
        isHealthy: true,
      });
    });
  });
});
