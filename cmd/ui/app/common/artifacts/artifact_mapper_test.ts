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
import { Artifact } from 'app/protos/experiment_state_server';

import {
  getArtifactDisplayProperties,
  groupAndSortArtifacts,
  hasPayloadContent,
  sortArtifactsByPriority,
  toTypedArtifact,
} from './artifact_mapper';
import { PayloadType, TypedArtifact } from './interfaces';

const BASE_ARTIFACT: Artifact = {
  name: 'experiments/1/artifacts/1',
  priority: 1,
  createTime: '2026-01-01T00:00:00Z',
};

function createArtifact(overrides: Partial<Artifact>): Artifact {
  return { ...BASE_ARTIFACT, ...overrides };
}

describe('artifact_mapper', () => {
  describe('toTypedArtifact', () => {
    it('maps managedXprof payload to MANAGED_XPROF typed artifact', () => {
      const managedXprof = { displayName: 'Profile 1', xprof: { xprofUrl: 'https://xprof.dev/1' } };
      const artifact = createArtifact({ payload: { managedXprof } });

      expect(toTypedArtifact(artifact)).toEqual({
        type: PayloadType.MANAGED_XPROF,
        artifact,
        payload: managedXprof,
      });
    });

    it('maps codeSource payload to CODE_SOURCE typed artifact', () => {
      const codeSource = { title: 'Repo', codeUrl: 'https://github.com/org/repo' };
      const artifact = createArtifact({ payload: { codeSource } });

      expect(toTypedArtifact(artifact)).toEqual({
        type: PayloadType.CODE_SOURCE,
        artifact,
        payload: codeSource,
      });
    });

    it('maps text payload to TEXT typed artifact', () => {
      const text = { title: 'Config', content: 'batch_size: 32' };
      const artifact = createArtifact({ payload: { text } });

      expect(toTypedArtifact(artifact)).toEqual({
        type: PayloadType.TEXT,
        artifact,
        payload: text,
      });
    });

    it('maps url payload to URL typed artifact', () => {
      const url = { displayName: 'Docs', url: 'https://docs.example.com', icon: 'menu_book' };
      const artifact = createArtifact({ payload: { url } });

      expect(toTypedArtifact(artifact)).toEqual({
        type: PayloadType.URL,
        artifact,
        payload: url,
      });
    });

    it('maps chart payload to CHART typed artifact', () => {
      const chart = { title: 'Loss Curve', metricNames: ['train/loss'] };
      const artifact = createArtifact({ payload: { chart } });

      expect(toTypedArtifact(artifact)).toEqual({
        type: PayloadType.CHART,
        artifact,
        payload: chart,
      });
    });

    it('maps missing or empty payload to UNKNOWN typed artifact', () => {
      const withoutPayload = createArtifact({});
      const emptyPayload = createArtifact({ payload: {} as Artifact['payload'] });

      expect(toTypedArtifact(withoutPayload)).toEqual({
        type: PayloadType.UNKNOWN,
        artifact: withoutPayload,
      });
      expect(toTypedArtifact(emptyPayload)).toEqual({
        type: PayloadType.UNKNOWN,
        artifact: emptyPayload,
      });
    });
  });

  describe('getArtifactDisplayProperties', () => {
    it('returns display properties for MANAGED_XPROF', () => {
      const populated = toTypedArtifact(
        createArtifact({
          payload: {
            managedXprof: { displayName: 'Step 100', xprof: { xprofUrl: 'https://xprof.dev/100' } },
          },
        }),
      );
      const empty = toTypedArtifact(createArtifact({ payload: { managedXprof: {} } }));

      expect(getArtifactDisplayProperties(populated)).toEqual({
        typeName: 'Managed Xprof',
        icon: 'query_stats',
        url: 'https://xprof.dev/100',
        displayName: 'Step 100',
      });
      expect(getArtifactDisplayProperties(empty)).toEqual({
        typeName: 'Managed Xprof',
        icon: 'query_stats',
        url: '',
        displayName: '',
      });
    });

    it('returns display properties for CODE_SOURCE', () => {
      const populated = toTypedArtifact(
        createArtifact({
          payload: { codeSource: { title: 'Main Branch', codeUrl: 'https://github.com/main' } },
        }),
      );
      const empty = toTypedArtifact(createArtifact({ payload: { codeSource: {} } }));

      expect(getArtifactDisplayProperties(populated)).toEqual({
        typeName: 'Code Source',
        icon: 'code',
        url: 'https://github.com/main',
        displayName: 'Main Branch',
      });
      expect(getArtifactDisplayProperties(empty)).toEqual({
        typeName: 'Code Source',
        icon: 'code',
        url: '',
        displayName: '',
      });
    });

    it('returns display properties for TEXT', () => {
      const populated = toTypedArtifact(
        createArtifact({ payload: { text: { title: 'Hyperparameters' } } }),
      );
      const empty = toTypedArtifact(createArtifact({ payload: { text: {} } }));

      expect(getArtifactDisplayProperties(populated)).toEqual({
        typeName: 'Text',
        icon: 'description',
        url: '',
        displayName: 'Hyperparameters',
      });
      expect(getArtifactDisplayProperties(empty)).toEqual({
        typeName: 'Text',
        icon: 'description',
        url: '',
        displayName: '',
      });
    });

    it('returns display properties and fallbacks for URL', () => {
      const custom = toTypedArtifact(
        createArtifact({
          payload: {
            url: { displayName: 'TensorBoard', url: 'https://tb.example.com', icon: 'analytics' },
          },
        }),
      );
      const urlOnly = toTypedArtifact(
        createArtifact({ payload: { url: { url: 'https://fallback.example.com' } } }),
      );
      const empty = toTypedArtifact(createArtifact({ payload: { url: {} } }));

      expect(getArtifactDisplayProperties(custom)).toEqual({
        typeName: 'URL',
        icon: 'analytics',
        url: 'https://tb.example.com',
        displayName: 'TensorBoard',
      });
      expect(getArtifactDisplayProperties(urlOnly)).toEqual({
        typeName: 'URL',
        icon: 'link',
        url: 'https://fallback.example.com',
        displayName: 'https://fallback.example.com',
      });
      expect(getArtifactDisplayProperties(empty)).toEqual({
        typeName: 'URL',
        icon: 'link',
        url: '',
        displayName: '',
      });
    });

    it('returns display properties and fallback title for CHART', () => {
      const titled = toTypedArtifact(
        createArtifact({ payload: { chart: { title: 'Eval Accuracy' } } }),
      );
      const untitled = toTypedArtifact(createArtifact({ payload: { chart: {} } }));

      expect(getArtifactDisplayProperties(titled)).toEqual({
        typeName: 'Chart',
        icon: 'bar_chart',
        url: '',
        displayName: 'Eval Accuracy',
      });
      expect(getArtifactDisplayProperties(untitled)).toEqual({
        typeName: 'Chart',
        icon: 'bar_chart',
        url: '',
        displayName: 'Dashboard Chart',
      });
    });

    it('returns default display properties for UNKNOWN', () => {
      const unknown = toTypedArtifact(createArtifact({}));

      expect(getArtifactDisplayProperties(unknown)).toEqual({
        typeName: 'Unknown',
        icon: 'link',
        url: '',
        displayName: '',
      });
    });
  });

  describe('hasPayloadContent', () => {
    it('returns true for known payload types and false for UNKNOWN', () => {
      const textArtifact = toTypedArtifact(
        createArtifact({ payload: { text: { title: 'Note' } } }),
      );
      const unknownArtifact = toTypedArtifact(createArtifact({}));

      expect(hasPayloadContent(textArtifact)).toBeTrue();
      expect(hasPayloadContent(unknownArtifact)).toBeFalse();
    });
  });

  describe('sortArtifactsByPriority', () => {
    it('sorts by ascending priority without mutating the input array', () => {
      const lowPriority = toTypedArtifact(
        createArtifact({ name: 'experiments/1/artifacts/low', priority: 10 }),
      );
      const highPriority = toTypedArtifact(
        createArtifact({ name: 'experiments/1/artifacts/high', priority: 1 }),
      );
      const unsetPriority = toTypedArtifact(
        createArtifact({ name: 'experiments/1/artifacts/unset', priority: undefined }),
      );
      const input = [unsetPriority, lowPriority, highPriority];

      const sorted = sortArtifactsByPriority(input);

      expect(sorted).toEqual([highPriority, lowPriority, unsetPriority]);
      expect(input).toEqual([unsetPriority, lowPriority, highPriority]);
    });

    it('breaks priority ties chronologically by createTime', () => {
      const later = toTypedArtifact(
        createArtifact({
          name: 'experiments/1/artifacts/later',
          priority: 2,
          createTime: '2026-01-02T00:00:00Z',
        }),
      );
      const earlier = toTypedArtifact(
        createArtifact({
          name: 'experiments/1/artifacts/earlier',
          priority: 2,
          createTime: '2026-01-01T00:00:00Z',
        }),
      );
      const missingTime = toTypedArtifact(
        createArtifact({
          name: 'experiments/1/artifacts/missing',
          priority: 2,
          createTime: undefined,
        }),
      );

      expect(sortArtifactsByPriority([later, earlier, missingTime])).toEqual([
        missingTime,
        earlier,
        later,
      ]);
    });
  });

  describe('groupAndSortArtifacts', () => {
    it('groups artifact types exceeding threshold and preserves priority ordering', () => {
      const url1 = toTypedArtifact(
        createArtifact({
          name: 'experiments/1/artifacts/url1',
          priority: 1,
          payload: { url: { url: 'https://example.com/1', icon: 'launch' } },
        }),
      );
      const text1 = toTypedArtifact(
        createArtifact({
          name: 'experiments/1/artifacts/text1',
          priority: 2,
          payload: { text: { title: 'Summary' } },
        }),
      );
      const url2 = toTypedArtifact(
        createArtifact({
          name: 'experiments/1/artifacts/url2',
          priority: 3,
          payload: { url: { url: 'https://example.com/2' } },
        }),
      );
      const items: TypedArtifact[] = [url2, text1, url1];

      const result = groupAndSortArtifacts(items, 'OUTPUT', 1);

      expect(result).toEqual([
        {
          type: 'group',
          payloadType: PayloadType.URL,
          artifacts: [url1, url2],
          groupId: 'OUTPUT-url',
          icon: 'launch',
        },
        {
          type: 'single',
          artifact: text1,
        },
      ]);
      expect(groupAndSortArtifacts([], 'OUTPUT', 1)).toEqual([]);
    });
  });
});
