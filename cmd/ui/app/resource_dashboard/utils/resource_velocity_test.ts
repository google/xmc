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

import {
  calculateJobVelocity,
  calculatePendingRequestsDisplay,
  calculateResourceVelocity,
  formatRate,
  getPendingJobsCount,
  getVelocityHours,
  mapPeriodToRangePreset,
  QueueSecondaryData,
} from './resource_velocity';

describe('Resource Velocity Utilities', () => {
  const mockSecondaryData: QueueSecondaryData = {
    queuePredictability: [
      {
        velocityRange: 'LAST_HOUR',
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
        velocityRange: 'LAST_DAY',
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

  describe('getVelocityHours', () => {
    it('should return hours for 1h, 24h, 7d periods', () => {
      expect(getVelocityHours('1h')).toBe(1);
      expect(getVelocityHours('1 hour')).toBe(1);
      expect(getVelocityHours('24h')).toBe(24);
      expect(getVelocityHours('1 day')).toBe(24);
      expect(getVelocityHours('7d')).toBe(168);
      expect(getVelocityHours('7 days')).toBe(168);
    });
  });

  describe('getPendingJobsCount', () => {
    it('should return pendingJobsCount fallback and calculated values', () => {
      expect(getPendingJobsCount({})).toBe(0);
      expect(getPendingJobsCount(mockSecondaryData)).toBe(5);
    });
  });

  describe('calculateResourceVelocity', () => {
    it('should return resource velocity string for matching timeframe', () => {
      expect(calculateResourceVelocity({}, '1h')).toBe('~0 GPUs/hr');
      expect(calculateResourceVelocity(mockSecondaryData, '1h')).toBe('~4.5 GPUs/hr');
      expect(calculateResourceVelocity(mockSecondaryData, '24h')).toBe('~2.1 GPUs/hr');
    });
  });

  describe('calculateJobVelocity', () => {
    it('should return job velocity string for matching timeframe', () => {
      expect(calculateJobVelocity({}, '1h')).toBe('0 jobs/hr');
      expect(calculateJobVelocity(mockSecondaryData, '1h')).toBe('12.5 jobs/hr');
      expect(calculateJobVelocity(mockSecondaryData, '24h')).toBe('8.2 jobs/hr');
    });
  });

  describe('calculatePendingRequestsDisplay', () => {
    it('should return pending resource requests display text', () => {
      expect(calculatePendingRequestsDisplay({})).toBe('0');
      expect(calculatePendingRequestsDisplay(mockSecondaryData)).toBe('16');
    });
  });
});
