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

import {
  expandResourceTypesWithParents,
  extractParentResourceType,
  matchesResourceFilter,
} from './resource_filter';

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
    expect(
      matchesResourceFilter('nvidia.com/gpu: nvidia-tesla-a100', 'nvidia.com/gpu:'),
    ).toBeTrue();
    expect(matchesResourceFilter('nvidia.com/gpu/sub-resource', 'nvidia.com/gpu/')).toBeTrue();
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

describe('extractParentResourceType', () => {
  it('should extract parent domain prefix from compound accelerator resources', () => {
    expect(extractParentResourceType('nvidia.com/gpu: nvidia-tesla-a100')).toBe('nvidia.com/gpu');
    expect(extractParentResourceType('nvidia.com/gpu: nvidia-l4')).toBe('nvidia.com/gpu');
    expect(extractParentResourceType('google.com/tpu: tpu-v5p')).toBe('google.com/tpu');
    expect(extractParentResourceType('custom.io/accelerator: model-x')).toBe(
      'custom.io/accelerator',
    );
  });

  it('should handle whitespace around compound components', () => {
    expect(extractParentResourceType('  nvidia.com/gpu : nvidia-tesla-a100  ')).toBe(
      'nvidia.com/gpu',
    );
  });

  it('should return null for non-compound resource types', () => {
    expect(extractParentResourceType('cpu')).toBeNull();
    expect(extractParentResourceType('memory')).toBeNull();
    expect(extractParentResourceType('nvidia.com/gpu')).toBeNull();
    expect(extractParentResourceType('google.com/tpu')).toBeNull();
  });

  it('should return null for empty, blank, or malformed strings', () => {
    expect(extractParentResourceType('')).toBeNull();
    expect(extractParentResourceType('   ')).toBeNull();
    expect(extractParentResourceType(':model')).toBeNull();
    expect(extractParentResourceType('nvidia.com/gpu:')).toBeNull();
    expect(extractParentResourceType(' : ')).toBeNull();
  });
});

describe('expandResourceTypesWithParents', () => {
  it('should synthesize parent options and sort parent before compound children', () => {
    const rawTypes = ['nvidia.com/gpu: nvidia-tesla-a100'];
    const expanded = expandResourceTypesWithParents(rawTypes);
    expect(expanded).toEqual(['nvidia.com/gpu', 'nvidia.com/gpu: nvidia-tesla-a100']);
  });

  it('should deduplicate synthesized parents across multiple flavors', () => {
    const rawTypes = [
      'nvidia.com/gpu: nvidia-tesla-a100',
      'nvidia.com/gpu: nvidia-l4',
      'nvidia.com/gpu: nvidia-h100',
    ];
    const expanded = expandResourceTypesWithParents(rawTypes);
    expect(expanded).toEqual([
      'nvidia.com/gpu',
      'nvidia.com/gpu: nvidia-h100',
      'nvidia.com/gpu: nvidia-l4',
      'nvidia.com/gpu: nvidia-tesla-a100',
    ]);
  });

  it('should preserve standard resources and sort all options deterministically', () => {
    const rawTypes = [
      'nvidia.com/gpu: nvidia-tesla-a100',
      'cpu',
      'google.com/tpu: tpu-v5p',
      'memory',
    ];
    const expanded = expandResourceTypesWithParents(rawTypes);
    expect(expanded).toEqual([
      'cpu',
      'google.com/tpu',
      'google.com/tpu: tpu-v5p',
      'memory',
      'nvidia.com/gpu',
      'nvidia.com/gpu: nvidia-tesla-a100',
    ]);
  });

  it('should not duplicate parents if generic resource is already reported by a queue', () => {
    const rawTypes = ['nvidia.com/gpu', 'nvidia.com/gpu: nvidia-tesla-a100', 'nvidia.com/gpu'];
    const expanded = expandResourceTypesWithParents(rawTypes);
    expect(expanded).toEqual(['nvidia.com/gpu', 'nvidia.com/gpu: nvidia-tesla-a100']);
  });

  it('should ignore empty and whitespace-only entries', () => {
    const rawTypes = ['', '  ', 'cpu'];
    const expanded = expandResourceTypesWithParents(rawTypes);
    expect(expanded).toEqual(['cpu']);
  });
});
