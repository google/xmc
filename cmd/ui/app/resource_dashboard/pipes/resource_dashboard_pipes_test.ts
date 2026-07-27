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

import { ClusterQueue, ResourceFlavorMetric } from 'xmanager_cloud/ui/app/service/resource_types';

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

    it('should return "-" when flavor is undefined', () => {
      expect(pipe.transform(undefined)).toBe('-');
    });

    it('should format free and total capacity correctly', () => {
      const flavor: ResourceFlavorMetric = {
        name: 'h100',
        resourceName: 'nvidia.com/gpu',
        nominalQuota: '64',
        inUse: '8',
        reserved: '0',
        freeCapacity: '56',
        largestSlot: '56 → 64',
      };
      expect(pipe.transform(flavor)).toBe('56 / 64');
    });

    it('should handle missing free capacity gracefully', () => {
      const flavor: Partial<ResourceFlavorMetric> = { nominalQuota: '100' };
      expect(pipe.transform(flavor as ResourceFlavorMetric)).toBe('0 / 100');
    });
  });

  describe('MaxWorkloadDisplayPipe', () => {
    let pipe: MaxWorkloadDisplayPipe;

    beforeEach(() => {
      pipe = new MaxWorkloadDisplayPipe();
    });

    it('should return "-" when flavor is undefined', () => {
      expect(pipe.transform(undefined)).toBe('-');
    });

    it('should return largestSlot if valid', () => {
      const flavor: ResourceFlavorMetric = {
        name: 'h100',
        resourceName: 'nvidia.com/gpu',
        nominalQuota: '64',
        inUse: '8',
        reserved: '0',
        freeCapacity: '56',
        largestSlot: '56 → 64',
      };
      expect(pipe.transform(flavor)).toBe('56 → 64');
    });

    it('should compute slot from freeCapacity and nominalQuota if largestSlot is missing', () => {
      const flavor: Partial<ResourceFlavorMetric> = {
        freeCapacity: '32',
        nominalQuota: '64',
      };
      expect(pipe.transform(flavor as ResourceFlavorMetric)).toBe('32 → 64');
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

    it('should return healthy message when status is Active and no pending workloads', () => {
      const queue: Partial<ClusterQueue> = {
        status: 'Active',
        pendingWorkloads: 0,
      };
      expect(pipe.transform(queue as ClusterQueue)).toEqual({
        text: 'System is healthy. Job will start immediately.',
        isHealthy: true,
      });
    });

    it('should return stuck traffic message when pending workloads exist', () => {
      const queue: Partial<ClusterQueue> = {
        status: 'Active',
        pendingWorkloads: 5,
      };
      expect(pipe.transform(queue as ClusterQueue)).toEqual({
        text: 'Traffic stuck. Use another queue if available.',
        isHealthy: false,
      });
    });

    it('should return stuck traffic message when status is not Active', () => {
      const queue: Partial<ClusterQueue> = {
        status: 'Inactive',
        pendingWorkloads: 0,
      };
      expect(pipe.transform(queue as ClusterQueue)).toEqual({
        text: 'Traffic stuck. Use another queue if available.',
        isHealthy: false,
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

    it('should return "Resources available" when active and no pending workloads', () => {
      const queue: Partial<ClusterQueue> = {
        status: 'Active',
        pendingWorkloads: 0,
      };
      expect(pipe.transform(queue as ClusterQueue)).toEqual({
        label: 'Resources available',
        isHealthy: true,
      });
    });

    it('should return "Preemption risk" when queue has pending workloads', () => {
      const queue: Partial<ClusterQueue> = {
        status: 'Active',
        pendingWorkloads: 2,
      };
      expect(pipe.transform(queue as ClusterQueue)).toEqual({
        label: 'Preemption risk',
        isHealthy: false,
      });
    });
  });
});
