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

import {
  QueueOverview,
  QueueResourceCapacity,
} from 'xmanager_cloud/api/protos/rod/api.jsonpb_decls';

import {
  CohortStateInfoPipe,
  FreeTotalDisplayPipe,
  MaxSchedulableDisplayPipe,
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

  describe('MaxSchedulableDisplayPipe', () => {
    let pipe: MaxSchedulableDisplayPipe;

    beforeEach(() => {
      pipe = new MaxSchedulableDisplayPipe();
    });

    it('should return "-" when capacity is undefined', () => {
      expect(pipe.transform(undefined)).toBe('-');
    });

    it('should return cluster quota / cohort quota when freeCapacityWithBorrowing is available', () => {
      const cap: QueueResourceCapacity = {
        resourceType: 'nvidia-tesla-h100',
        freeCapacity: 32,
        totalCapacity: 64,
        freeCapacityWithBorrowing: 24,
      };
      expect(pipe.transform(cap)).toBe('32 / 24');
    });

    it('should format cluster quota / cluster quota when freeCapacityWithBorrowing is not set', () => {
      const cap: QueueResourceCapacity = {
        resourceType: 'cpu',
        freeCapacity: 128,
        totalCapacity: 256,
      };
      expect(pipe.transform(cap)).toBe('128 / 128');
    });
  });

  describe('CohortStateInfoPipe & getCohortStateInfo', () => {
    let pipe: CohortStateInfoPipe;

    beforeEach(() => {
      pipe = new CohortStateInfoPipe();
    });

    it('should identify SQUEEZED state when freeCapacityWithBorrowing < freeCapacity', () => {
      const cap: QueueResourceCapacity = {
        resourceType: 'cpu',
        totalCapacity: 100,
        freeCapacity: 60,
        freeCapacityWithBorrowing: 30,
      };
      const info = pipe.transform(cap);
      expect(info.state).toBe('SQUEEZED');
      expect(info.delta).toBe(30);
      expect(info.badgeClass).toBe('squeezed');
      expect(info.label).toBe('Squeezed by cohort');
    });

    it('should identify BORROWING state when freeCapacityWithBorrowing > freeCapacity', () => {
      const cap: QueueResourceCapacity = {
        resourceType: 'cpu',
        totalCapacity: 100,
        freeCapacity: 60,
        freeCapacityWithBorrowing: 80,
      };
      const info = pipe.transform(cap);
      expect(info.state).toBe('BORROWING');
      expect(info.delta).toBe(20);
      expect(info.badgeClass).toBe('borrowing');
      expect(info.label).toBe('Borrowing available');
    });

    it('should identify BALANCED state when freeCapacityWithBorrowing == freeCapacity', () => {
      const cap: QueueResourceCapacity = {
        resourceType: 'cpu',
        totalCapacity: 100,
        freeCapacity: 60,
        freeCapacityWithBorrowing: 60,
      };
      const info = pipe.transform(cap);
      expect(info.state).toBe('BALANCED');
      expect(info.delta).toBe(0);
      expect(info.badgeClass).toBe('balanced');
      expect(info.label).toBe('Balanced');
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
