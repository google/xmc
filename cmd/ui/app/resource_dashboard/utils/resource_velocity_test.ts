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

import { QueueSecondaryData } from 'xmanager_cloud/api/protos/rod/api.jsonpb_decls';

import {
  calculateJobVelocity,
  calculatePendingRequestsDisplay,
  calculateResourceVelocity,
  formatRate,
  getPendingJobsCount,
  getVelocityHours,
  mapPeriodToRangePreset,
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
          velocity: 8.4,
        },
        pendingResourceRequests: {
          resourceType: 'nvidia.com/gpu',
          count: 32,
        },
      },
    ],
  };

  it('formatRate should format integer rates correctly', () => {
    expect(formatRate(0)).toBe('0');
    expect(formatRate(10)).toBe('10');
  });

  it('formatRate should format decimal rates with up to 2 decimal places', () => {
    expect(formatRate(12.5)).toBe('12.5');
    expect(formatRate(8.4321)).toBe('8.43');
  });

  it('getVelocityHours should convert period strings to correct hours', () => {
    expect(getVelocityHours('1h')).toBe(1);
    expect(getVelocityHours('1 hour')).toBe(1);
    expect(getVelocityHours('24h')).toBe(24);
    expect(getVelocityHours('1 day')).toBe(24);
    expect(getVelocityHours('7d')).toBe(168);
    expect(getVelocityHours('7 days')).toBe(168);
    expect(getVelocityHours('unknown')).toBe(168);
  });

  it('mapPeriodToRangePreset should return correct RangePreset enum values', () => {
    expect(mapPeriodToRangePreset('1h')).toBe('LAST_HOUR');
    expect(mapPeriodToRangePreset('24h')).toBe('LAST_DAY');
    expect(mapPeriodToRangePreset('7d')).toBe('LAST_WEEK');
    expect(mapPeriodToRangePreset('invalid')).toBe('LAST_WEEK');
  });

  it('getPendingJobsCount should return pending jobs count', () => {
    expect(getPendingJobsCount(mockSecondaryData)).toBe(5);
  });

  it('calculateResourceVelocity should format resource velocity display string', () => {
    expect(calculateResourceVelocity(mockSecondaryData, '1h')).toBe('~8.4 GPUs/hr');
  });

  it('calculateJobVelocity should format job velocity display string', () => {
    expect(calculateJobVelocity(mockSecondaryData, '1h')).toBe('12.5 jobs/hr');
  });

  it('calculatePendingRequestsDisplay should format pending requests count string', () => {
    expect(calculatePendingRequestsDisplay(mockSecondaryData)).toBe('32');
  });
});
