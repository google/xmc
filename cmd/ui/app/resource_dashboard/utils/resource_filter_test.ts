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

import { matchesResourceFilter } from './resource_filter';

describe('matchesResourceFilter', () => {
  it('should return false for empty inputs', () => {
    expect(matchesResourceFilter('', 'nvidia.com/gpu')).toBeFalse();
    expect(matchesResourceFilter('nvidia.com/gpu', '')).toBeFalse();
    expect(matchesResourceFilter('', '')).toBeFalse();
  });

  it('should match exact identical strings', () => {
    expect(matchesResourceFilter('cpu', 'cpu')).toBeTrue();
    expect(matchesResourceFilter('CPU', 'cpu')).toBeTrue();
    expect(matchesResourceFilter('memory', 'memory')).toBeTrue();
    expect(matchesResourceFilter('nvidia.com/gpu', 'nvidia.com/gpu')).toBeTrue();
    expect(
      matchesResourceFilter(
        'nvidia.com/gpu: nvidia-tesla-a100',
        'nvidia.com/gpu: nvidia-tesla-a100',
      ),
    ).toBeTrue();
  });

  it('should match specific compound resources when generic filter is selected', () => {
    expect(matchesResourceFilter('nvidia.com/gpu: nvidia-tesla-a100', 'nvidia.com/gpu')).toBeTrue();
    expect(matchesResourceFilter('nvidia.com/gpu: nvidia-l4', 'nvidia.com/gpu')).toBeTrue();
    expect(matchesResourceFilter('nvidia.com/gpu: nvidia-tesla-a100', 'gpu')).toBeTrue();
    expect(matchesResourceFilter('google.com/tpu: tpu-v5p', 'google.com/tpu')).toBeTrue();
    expect(matchesResourceFilter('google.com/tpu: tpu-v5p', 'tpu')).toBeTrue();
  });

  it('should NOT match generic queue when specific model filter is selected', () => {
    expect(
      matchesResourceFilter('nvidia.com/gpu', 'nvidia.com/gpu: nvidia-tesla-a100'),
    ).toBeFalse();
    expect(matchesResourceFilter('nvidia.com/gpu', 'nvidia.com/gpu: nvidia-l4')).toBeFalse();
    expect(matchesResourceFilter('google.com/tpu', 'google.com/tpu: tpu-v5p')).toBeFalse();
  });

  it('should NOT match different specific accelerators', () => {
    expect(
      matchesResourceFilter('nvidia.com/gpu: nvidia-l4', 'nvidia.com/gpu: nvidia-tesla-a100'),
    ).toBeFalse();
    expect(
      matchesResourceFilter('nvidia.com/gpu: nvidia-tesla-a100', 'nvidia.com/gpu: nvidia-l4'),
    ).toBeFalse();
  });

  it('should match when substring search term is used without domain prefix', () => {
    expect(matchesResourceFilter('nvidia.com/gpu: nvidia-tesla-a100', 'a100')).toBeTrue();
    expect(matchesResourceFilter('nvidia.com/gpu: nvidia-tesla-a100', 'tesla')).toBeTrue();
    expect(matchesResourceFilter('nvidia.com/gpu: nvidia-l4', 'l4')).toBeTrue();
  });
});
