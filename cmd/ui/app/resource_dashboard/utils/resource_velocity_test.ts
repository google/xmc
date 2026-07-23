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
} from './resource_velocity';

describe('Resource Velocity Utilities', () => {
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
    });
  });

  describe('getPendingJobsCount', () => {
    it('should return pendingJobsCount or pendingWorkloads', () => {
      expect(getPendingJobsCount({ pendingJobsCount: 5 })).toBe(5);
      expect(getPendingJobsCount({ pendingWorkloads: 3 })).toBe(3);
      expect(getPendingJobsCount({})).toBe(0);
    });
  });

  describe('calculateResourceVelocity', () => {
    it('should return custom resource velocity if present', () => {
      expect(calculateResourceVelocity({ resourceVelocity: '~16 GPUs/hr' }, '1h')).toBe(
        '~16 GPUs/hr',
      );
    });

    it('should calculate rate for GPU dominant resource regardless of pending jobs', () => {
      expect(
        calculateResourceVelocity(
          {
            dominantResource: 'nvidia.com/gpu',
            dominantResourceUnits: 8,
            pendingWorkloads: 5,
          },
          '1h',
        ),
      ).toBe('~8 GPUs/hr');
    });
  });

  describe('calculateJobVelocity', () => {
    it('should return custom job velocity if present', () => {
      expect(calculateJobVelocity({ jobVelocity: '4 jobs/hr' }, '1h')).toBe('4 jobs/hr');
    });

    it('should calculate job rate regardless of pending jobs', () => {
      expect(
        calculateJobVelocity(
          {
            admittedJobsCount: 24,
            pendingWorkloads: 3,
          },
          '24h',
        ),
      ).toBe('1 jobs/hr');
    });
  });

  describe('calculatePendingRequestsDisplay', () => {
    it('should return requested resources if present', () => {
      expect(calculatePendingRequestsDisplay({ requestedResources: '16 GPUs' })).toBe('16 GPUs');
    });

    it('should return 0 when requested resources is not present or 0', () => {
      expect(calculatePendingRequestsDisplay({})).toBe('0');
      expect(calculatePendingRequestsDisplay({ requestedResources: '0' })).toBe('0');
      expect(calculatePendingRequestsDisplay({ pendingJobsCount: 2 })).toBe('0');
    });
  });
});
