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

import { ClusterQueueSecondaryMetrics } from 'xmanager_cloud/api/protos/rod/api.jsonpb_decls';

import {
  calculateJobVelocity,
  calculatePendingRequestsDisplay,
  calculateResourceVelocity,
  findQueuePredictability,
  formatRate,
  getPendingJobsCount,
} from './resource_velocity';

describe('Resource Velocity Utilities', () => {
  const mockSecondaryData: ClusterQueueSecondaryMetrics = {
    queuePredictability: [
      {
        velocityRange: 'RANGE_PRESET_LAST_HOUR',
        pendingJobs: 5,
        jobVelocity: 12.5,
        resourceVelocity: {
          resourceType: 'nvidia.com/gpu',
          velocity: 4.5,
        },
        pendingResourceRequests: {
          resourceType: 'nvidia.com/gpu',
          count: 16,
        },
      },
      {
        velocityRange: 'RANGE_PRESET_LAST_DAY',
        pendingJobs: 5,
        jobVelocity: 8.2,
        resourceVelocity: {
          resourceType: 'nvidia.com/gpu',
          velocity: 2.1,
        },
        pendingResourceRequests: {
          resourceType: 'nvidia.com/gpu',
          count: 16,
        },
      },
      {
        velocityRange: 'RANGE_PRESET_LAST_HOUR',
        pendingJobs: 2,
        jobVelocity: 25.0,
        resourceVelocity: {
          resourceType: 'cpu',
          velocity: 120,
        },
        pendingResourceRequests: {
          resourceType: 'cpu',
          count: 48,
        },
      },
    ],
  };

  describe('formatRate', () => {
    it('should format rate values correctly', () => {
      expect(formatRate(0)).toBe('0');
      expect(formatRate(5)).toBe('5');
      expect(formatRate(2.33333)).toBe('2.33');
      expect(formatRate(8.5)).toBe('8.5');
    });
  });

  describe('getPendingJobsCount', () => {
    it('should return pendingJobsCount fallback and calculated values', () => {
      expect(getPendingJobsCount({})).toBe(0);
      expect(getPendingJobsCount(mockSecondaryData)).toBe(5);
    });
  });

  describe('calculateResourceVelocity', () => {
    it('should return resource velocity string for matching timeframe and dominant resource', () => {
      expect(calculateResourceVelocity({}, '1h')).toBe('~0 units/hr');
      expect(calculateResourceVelocity(mockSecondaryData, '1h', 'nvidia.com/gpu')).toBe(
        '~4.5 nvidia.com/gpu/hr',
      );
      expect(calculateResourceVelocity(mockSecondaryData, '24h', 'nvidia.com/gpu')).toBe(
        '~2.1 nvidia.com/gpu/hr',
      );
      expect(calculateResourceVelocity(mockSecondaryData, '1h', 'cpu')).toBe('~120 cpu/hr');
    });
  });

  describe('calculateJobVelocity', () => {
    it('should return job velocity string for matching timeframe and dominant resource', () => {
      expect(calculateJobVelocity({}, '1h')).toBe('0 jobs/hr');
      expect(calculateJobVelocity(mockSecondaryData, '1h', 'nvidia.com/gpu')).toBe('12.5 jobs/hr');
      expect(calculateJobVelocity(mockSecondaryData, '24h', 'nvidia.com/gpu')).toBe('8.2 jobs/hr');
      expect(calculateJobVelocity(mockSecondaryData, '1h', 'cpu')).toBe('25 jobs/hr');
    });
  });

  describe('findQueuePredictability', () => {
    it('should return exact match for timeframe and dominant resource', () => {
      const match = findQueuePredictability(mockSecondaryData, '1h', 'nvidia.com/gpu');
      expect(match).toBeDefined();
      expect(match?.jobVelocity).toBe(12.5);
    });

    it('should return undefined when timeframe does not exist for dominant resource', () => {
      const match = findQueuePredictability(mockSecondaryData, '7d', 'nvidia.com/gpu');
      expect(match).toBeUndefined();
    });

    it('should fallback to first entry when dominantResource is null or empty', () => {
      const match = findQueuePredictability(mockSecondaryData, '7d');
      expect(match).toBeDefined();
      expect(match?.velocityRange).toBe('RANGE_PRESET_LAST_HOUR');
    });
  });

  describe('calculatePendingRequestsDisplay', () => {
    it('should return pending resource requests display text for dominant resource', () => {
      expect(calculatePendingRequestsDisplay({})).toBe('0');
      expect(calculatePendingRequestsDisplay(mockSecondaryData, 'nvidia.com/gpu')).toBe('16');
      expect(calculatePendingRequestsDisplay(mockSecondaryData, 'cpu')).toBe('48');
    });

    it('should find pending requests count even if only non-1h range preset exists', () => {
      const weeklyOnlyData: ClusterQueueSecondaryMetrics = {
        queuePredictability: [
          {
            velocityRange: 'RANGE_PRESET_LAST_WEEK',
            pendingResourceRequests: {
              resourceType: 'nvidia.com/gpu',
              count: 32,
            },
          },
        ],
      };
      expect(calculatePendingRequestsDisplay(weeklyOnlyData, 'nvidia.com/gpu')).toBe('32');
    });
  });
});
