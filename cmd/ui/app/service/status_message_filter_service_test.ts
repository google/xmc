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

import { ChangeDetectionStrategy, Component } from '@angular/core';
import { provideRouter, Router } from '@angular/router';

import { StructuredMessage } from 'xmanager_cloud/experiment_state_server/proto/structured_message.jsonpb_decls';

import { cleanState, get, setupModule } from '../testing/test';
import {
  ALL_SOURCES,
  formatSource,
  StatusMessageFilterService,
} from './status_message_filter_service';

@Component({
  standalone: true,
  template: '',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
class EmptyComp {}

describe('StatusMessageFilterService', () => {
  const state = cleanState(() => {
    setupModule({
      providers: [
        StatusMessageFilterService,
        provideRouter([
          { path: 'experiments', component: EmptyComp },
          { path: 'experiments/:id', component: EmptyComp },
        ]),
      ],
    });

    const storage = new Map<string, string>();
    spyOn(window.localStorage, 'getItem').and.callFake((key: string) => storage.get(key) || null);
    spyOn(window.localStorage, 'setItem').and.callFake((key: string, value: string) =>
      storage.set(key, value),
    );
    spyOn(window.localStorage, 'removeItem').and.callFake((key: string) => storage.delete(key));

    return {
      storage,
      router: get(Router),
    };
  }, beforeEach);

  afterEach(() => {
    state.storage.clear();
  });

  it('should be instantiated', async () => {
    await state.router.navigateByUrl('/experiments');
    expect(get(StatusMessageFilterService)).toBeTruthy();
  });

  describe('Initialization with list context', () => {
    beforeEach(async () => {
      await state.router.navigateByUrl('/experiments');
    });

    it('should load default filters if storage is empty', () => {
      const service = get(StatusMessageFilterService);
      expect(service.severitiesSubject.value).toEqual(
        new Set<StructuredMessage.Severity>(['WARNING', 'ERROR', 'FATAL']),
      );
      expect(service.sourcesSubject.value).toEqual(new Set<StructuredMessage.Source>(ALL_SOURCES));
    });

    it('should load filters from storage', () => {
      state.storage.set(
        'xmc-status-filter-severities-experiments-list',
        JSON.stringify(['INFO', 'ERROR']),
      );
      state.storage.set(
        'xmc-status-filter-sources-experiments-list',
        JSON.stringify(['KUBERNETES']),
      );
      const service = get(StatusMessageFilterService);
      expect(service.severitiesSubject.value).toEqual(
        new Set<StructuredMessage.Severity>(['INFO', 'ERROR']),
      );
      expect(service.sourcesSubject.value).toEqual(
        new Set<StructuredMessage.Source>(['KUBERNETES']),
      );
    });

    it('should load default filters if storage data is corrupted', () => {
      state.storage.set('xmc-status-filter-severities-experiments-list', 'corrupted-json');
      const service = get(StatusMessageFilterService);
      expect(service.severitiesSubject.value).toEqual(
        new Set<StructuredMessage.Severity>(['WARNING', 'ERROR', 'FATAL']),
      );
      expect(state.storage.has('xmc-status-filter-severities-experiments-list')).toBeFalse();
    });
  });

  describe('Initialization with detail context', () => {
    beforeEach(async () => {
      await state.router.navigateByUrl('/experiments/123');
    });

    it('should load default filters if storage is empty', () => {
      const service = get(StatusMessageFilterService);
      expect(service.severitiesSubject.value).toEqual(
        new Set<StructuredMessage.Severity>(['WARNING', 'ERROR', 'FATAL']),
      );
      expect(service.sourcesSubject.value).toEqual(new Set<StructuredMessage.Source>(ALL_SOURCES));
    });

    it('should load filters from storage', () => {
      state.storage.set(
        'xmc-status-filter-severities-experiments-detail',
        JSON.stringify(['FATAL']),
      );
      state.storage.set(
        'xmc-status-filter-sources-experiments-detail',
        JSON.stringify(['APPLICATION']),
      );
      const service = get(StatusMessageFilterService);
      expect(service.severitiesSubject.value).toEqual(
        new Set<StructuredMessage.Severity>(['FATAL']),
      );
      expect(service.sourcesSubject.value).toEqual(
        new Set<StructuredMessage.Source>(['APPLICATION']),
      );
    });
  });

  describe('Context Switching', () => {
    it('should switch to detail context and load detail filters', async () => {
      await state.router.navigateByUrl('/experiments');
      const service = get(StatusMessageFilterService);
      state.storage.set(
        'xmc-status-filter-severities-experiments-detail',
        JSON.stringify(['FATAL']),
      );
      state.storage.set(
        'xmc-status-filter-sources-experiments-detail',
        JSON.stringify(['APPLICATION']),
      );

      await state.router.navigateByUrl('/experiments/123');

      expect(service.severitiesSubject.value).toEqual(
        new Set<StructuredMessage.Severity>(['FATAL']),
      );
      expect(service.sourcesSubject.value).toEqual(
        new Set<StructuredMessage.Source>(['APPLICATION']),
      );
    });

    it('should switch to list context and load list filters', async () => {
      await state.router.navigateByUrl('/experiments/123');
      const service = get(StatusMessageFilterService);
      state.storage.set('xmc-status-filter-severities-experiments-list', JSON.stringify(['INFO']));
      state.storage.set(
        'xmc-status-filter-sources-experiments-list',
        JSON.stringify(['XMANAGER_CLIENT']),
      );

      await state.router.navigateByUrl('/experiments');

      expect(service.severitiesSubject.value).toEqual(
        new Set<StructuredMessage.Severity>(['INFO']),
      );
      expect(service.sourcesSubject.value).toEqual(
        new Set<StructuredMessage.Source>(['XMANAGER_CLIENT']),
      );
    });
  });

  describe('Update Filters', () => {
    it('should update severities and save to storage', async () => {
      await state.router.navigateByUrl('/experiments');
      const service = get(StatusMessageFilterService);
      const newSeverities = new Set<StructuredMessage.Severity>(['INFO']);
      service.updateSeverities(newSeverities);

      expect(service.severitiesSubject.value).toEqual(newSeverities);
      expect(state.storage.get('xmc-status-filter-severities-experiments-list')).toBe(
        JSON.stringify(['INFO']),
      );
    });

    it('should update sources and save to storage', async () => {
      await state.router.navigateByUrl('/experiments');
      const service = get(StatusMessageFilterService);
      const newSources = new Set<StructuredMessage.Source>(['USER_DEFINED']);
      service.updateSources(newSources);

      expect(service.sourcesSubject.value).toEqual(newSources);
      expect(state.storage.get('xmc-status-filter-sources-experiments-list')).toBe(
        JSON.stringify(['USER_DEFINED']),
      );
    });
  });

  describe('isMessageVisible', () => {
    const severities = new Set<StructuredMessage.Severity>(['INFO', 'WARNING']);
    const sources = new Set<StructuredMessage.Source>(['APPLICATION', 'KUBERNETES']);

    it('should return true if severity and source match', async () => {
      await state.router.navigateByUrl('/experiments');
      const service = get(StatusMessageFilterService);
      service.updateSeverities(severities);
      service.updateSources(sources);
      const message: StructuredMessage = {
        severity: 'INFO',
        source: 'APPLICATION',
      };
      expect(service.isMessageVisible(message)).toBeTrue();
    });

    it('should return false if severity does not match', async () => {
      await state.router.navigateByUrl('/experiments');
      const service = get(StatusMessageFilterService);
      service.updateSeverities(severities);
      service.updateSources(sources);
      const message: StructuredMessage = {
        severity: 'ERROR',
        source: 'APPLICATION',
      };
      expect(service.isMessageVisible(message)).toBeFalse();
    });

    it('should return false if source does not match', async () => {
      await state.router.navigateByUrl('/experiments');
      const service = get(StatusMessageFilterService);
      service.updateSeverities(severities);
      service.updateSources(sources);
      const message: StructuredMessage = {
        severity: 'INFO',
        source: 'XMANAGER_CLIENT',
      };
      expect(service.isMessageVisible(message)).toBeFalse();
    });

    it('should handle unspecified severity and source', async () => {
      await state.router.navigateByUrl('/experiments');
      const service = get(StatusMessageFilterService);
      service.updateSeverities(severities);
      service.updateSources(sources);
      const message: StructuredMessage = {};
      expect(service.isMessageVisible(message)).toBeFalse();

      const severitiesWithUnspecified = new Set<StructuredMessage.Severity>([
        'SEVERITY_UNSPECIFIED',
        'INFO',
      ]);
      const sourcesWithUnspecified = new Set<StructuredMessage.Source>([
        'SOURCE_UNSPECIFIED',
        'APPLICATION',
      ]);
      service.updateSeverities(severitiesWithUnspecified);
      service.updateSources(sourcesWithUnspecified);
      expect(service.isMessageVisible(message)).toBeTrue();
    });
  });

  describe('formatSource', () => {
    it('should format known sources', () => {
      expect(formatSource('XMANAGER_CLIENT')).toBe('XManager Client');
      expect(formatSource('XMANAGER_BACKEND')).toBe('XManager Backend');
      expect(formatSource('KUBERNETES')).toBe('Kubernetes');
      expect(formatSource('APPLICATION')).toBe('Application');
    });

    it('should handle unspecified and undefined sources', () => {
      expect(formatSource('SOURCE_UNSPECIFIED')).toBe('Unspecified');
      expect(formatSource(undefined)).toBe('Unspecified');
    });
  });
});
