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

import { QueueOverview, QueueResourceCapacity } from 'app/protos/rod';

import {
  COHORT_STATE_BALANCED,
  COHORT_STATE_BORROWING,
  COHORT_STATE_SQUEEZED,
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

    it('should return freeCapacityWithBorrowing when available', () => {
      const cap: QueueResourceCapacity = {
        resourceType: 'nvidia-tesla-h100',
        freeCapacity: 32,
        totalCapacity: 64,
        freeCapacityWithBorrowing: 24,
      };
      expect(pipe.transform(cap)).toBe('24');
    });

    it('should fall back to freeCapacity when freeCapacityWithBorrowing is not set', () => {
      const cap: QueueResourceCapacity = {
        resourceType: 'cpu',
        freeCapacity: 128,
        totalCapacity: 256,
      };
      expect(pipe.transform(cap)).toBe('128');
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
      expect(pipe.transform(cap)).toEqual({
        state: COHORT_STATE_SQUEEZED,
        delta: 30,
        badgeClass: 'squeezed',
        label: 'Squeezed by cohort',
        icon: 'warning',
        tooltip:
          'Shared cohort pool depleted: Immediate headroom (30) is lower than nominal free quota (60) due to cohort-wide workload demand.',
      });
    });

    it('should identify BORROWING state when freeCapacityWithBorrowing > freeCapacity', () => {
      const cap: QueueResourceCapacity = {
        resourceType: 'cpu',
        totalCapacity: 100,
        freeCapacity: 60,
        freeCapacityWithBorrowing: 80,
      };
      expect(pipe.transform(cap)).toEqual({
        state: COHORT_STATE_BORROWING,
        delta: 20,
        badgeClass: 'borrowing',
        label: 'Borrowing available',
        icon: 'bolt',
        tooltip: 'Can borrow up to +20 beyond nominal quota from cohort.',
      });
    });

    it('should identify BALANCED state when freeCapacityWithBorrowing == freeCapacity', () => {
      const cap: QueueResourceCapacity = {
        resourceType: 'cpu',
        totalCapacity: 100,
        freeCapacity: 60,
        freeCapacityWithBorrowing: 60,
      };
      expect(pipe.transform(cap)).toEqual({
        state: COHORT_STATE_BALANCED,
        delta: 0,
        badgeClass: 'balanced',
        label: 'Balanced',
        icon: 'check_circle',
      });
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
