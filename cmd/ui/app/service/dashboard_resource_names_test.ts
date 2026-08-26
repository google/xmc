// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

import 'jasmine';

import { extractChartId, extractDashboardId } from './dashboard_resource_names';

describe('dashboard_resource_names', () => {
  describe('extractDashboardId', () => {
    it('should strip the dashboards collection prefix', () => {
      expect(extractDashboardId('dashboards/vcc_comparison')).toBe('vcc_comparison');
    });

    it('should pass through a bare ID supplied by a router parameter', () => {
      expect(extractDashboardId('my_dashboard')).toBe('my_dashboard');
    });

    it('should return an empty string for missing names', () => {
      expect(extractDashboardId('')).toBe('');
      expect(extractDashboardId(undefined)).toBe('');
    });
  });

  describe('extractChartId', () => {
    it('should extract the trailing ID from a work unit chart name', () => {
      expect(
        extractChartId('experiments/1234/workUnits/1/charts/f47ac10b-58cc-4372-a567-0e02b2c3d479'),
      ).toBe('f47ac10b-58cc-4372-a567-0e02b2c3d479');
    });

    it('should extract the trailing ID from a dashboard chart name', () => {
      expect(extractChartId('dashboards/vcc_comparison/charts/chart-uuid')).toBe('chart-uuid');
    });

    it('should extract the trailing ID from a bare chart name', () => {
      expect(extractChartId('charts/chart-uuid')).toBe('chart-uuid');
    });

    it('should pass through an unprefixed ID', () => {
      expect(extractChartId('chart-uuid')).toBe('chart-uuid');
    });

    it('should return an empty string for missing names', () => {
      expect(extractChartId('')).toBe('');
      expect(extractChartId(undefined)).toBe('');
    });
  });
});
