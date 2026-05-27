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
import 'jasmine';

import {parseArtifactName, parseExperimentName, parseWorkUnitName, } from './resource_name_extractor';

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
      expect(parseArtifactName('experiments/123/workUnits/1/artifacts/1'))
          .toBeNull();
      expect(parseArtifactName('experiments/123')).toBeNull();
      expect(parseArtifactName('experiments/123/workUnits/1')).toBeNull();
      expect(parseArtifactName('experiments/123/artifacts/1/invalid'))
          .toBeNull();
    });
  });
});
