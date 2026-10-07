// Copyright 2025 Google LLC
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
  extractChartId,
  extractDashboardId,
  formatWorkUnitParent,
  parseArtifactName,
  parseExperimentName,
  parseWorkUnitName,
} from './resource_name_extractor';

describe('ResourceNames', () => {
  describe('parseExperimentName', () => {
    it('should parse valid experiment names', () => {
      expect(parseExperimentName('experiments/123')).toEqual('123');
    });

    it('should return null for invalid names', () => {
      expect(parseExperimentName('experiments/123/workUnits/1')).toBeNull();
      expect(parseExperimentName('invalid/123')).toBeNull();
      expect(parseExperimentName('experiments/')).toBeNull();
    });
  });

  describe('parseWorkUnitName', () => {
    it('should parse valid work unit names', () => {
      expect(parseWorkUnitName('experiments/123/workUnits/1')).toEqual({
        xid: '123',
        wid: '1',
      });
    });

    it('should return null for invalid names', () => {
      expect(parseWorkUnitName('experiments/123')).toBeNull();
      expect(parseWorkUnitName('invalid/123/workUnits/1')).toBeNull();
      expect(parseWorkUnitName('experiments/123/workUnits/')).toBeNull();
    });
  });

  describe('parseArtifactName', () => {
    it('should parse valid artifact names', () => {
      expect(parseArtifactName('artifacts/1')).toEqual('1');
    });

    it('should return null for invalid names', () => {
      expect(parseArtifactName('experiments/123/artifacts/1')).toBeNull();
      expect(parseArtifactName('experiments/123/workUnits/1/artifacts/1')).toBeNull();
      expect(parseArtifactName('experiments/123')).toBeNull();
      expect(parseArtifactName('experiments/123/workUnits/1')).toBeNull();
      expect(parseArtifactName('experiments/123/artifacts/1/invalid')).toBeNull();
    });
  });

  describe('extractChartId', () => {
    it('should extract the ID from every parent a chart can be addressed under', () => {
      expect(
        extractChartId('experiments/1234/workUnits/1/charts/f47ac10b-58cc-4372-a567-0e02b2c3d479'),
      ).toBe('f47ac10b-58cc-4372-a567-0e02b2c3d479');
      expect(extractChartId('dashboards/vcc_comparison/charts/chart-uuid')).toBe('chart-uuid');
      expect(extractChartId('charts/chart-uuid')).toBe('chart-uuid');
    });

    it('should pass a bare ID through unchanged', () => {
      expect(extractChartId('chart-uuid')).toBe('chart-uuid');
    });

    it('should not mistake another resource name for a chart', () => {
      expect(extractChartId('experiments/123/workUnits/1')).toBe('');
      expect(extractChartId('dashboards/vcc_comparison')).toBe('');
      expect(extractChartId('charts/')).toBe('');
    });

    it('should return an empty string for missing input', () => {
      expect(extractChartId('')).toBe('');
      expect(extractChartId(undefined)).toBe('');
    });
  });

  describe('extractDashboardId', () => {
    it('should extract the ID from a dashboard resource name', () => {
      expect(extractDashboardId('dashboards/vcc_comparison')).toBe('vcc_comparison');
    });

    it('should pass a bare ID through unchanged', () => {
      expect(extractDashboardId('my_dashboard')).toBe('my_dashboard');
    });

    it('should not mistake another resource name for a dashboard', () => {
      expect(extractDashboardId('dashboards/vcc_comparison/charts/chart-uuid')).toBe('');
      expect(extractDashboardId('experiments/123/workUnits/1')).toBe('');
    });

    it('should return an empty string for missing input', () => {
      expect(extractDashboardId('')).toBe('');
      expect(extractDashboardId(undefined)).toBe('');
    });
  });

  describe('formatWorkUnitParent', () => {
    it('should build a work unit parent that parseWorkUnitName round-trips', () => {
      expect(formatWorkUnitParent(1234, 2)).toBe('experiments/1234/workUnits/2');
      expect(parseWorkUnitName(formatWorkUnitParent(1234, 2))).toEqual({
        xid: '1234',
        wid: '2',
      });
    });

    it('should default a missing work unit ID to 1', () => {
      expect(formatWorkUnitParent(1234)).toBe('experiments/1234/workUnits/1');
      expect(formatWorkUnitParent(1234, undefined)).toBe('experiments/1234/workUnits/1');
    });
  });
});
