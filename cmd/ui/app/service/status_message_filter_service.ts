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
import { inject, Injectable } from '@angular/core';
import { NavigationEnd, Router } from '@angular/router';
import { BehaviorSubject, Observable } from 'rxjs';
import { filter } from 'rxjs/operators';

import { StructuredMessage } from 'xmanager_cloud/experiment_state_server/proto/structured_message.jsonpb_decls';

const SEVERITIES_STORAGE_KEY_PREFIX = 'xmc-status-filter-severities';
const SOURCES_STORAGE_KEY_PREFIX = 'xmc-status-filter-sources';

/** All possible severity values excluding unspecified. */
export const ALL_SEVERITIES: StructuredMessage.Severity[] = [
  'DEBUG',
  'INFO',
  'WARNING',
  'ERROR',
  'FATAL',
];
/** All possible source values excluding unspecified. */
export const ALL_SOURCES: StructuredMessage.Source[] = [
  'XMANAGER_CLIENT',
  'XMANAGER_BACKEND',
  'ADAPTER',
  'KUBERNETES',
  'SCHEDULER',
  'APPLICATION',
  'USER_DEFINED',
];

/** Default severities to show. */
const DEFAULT_SEVERITIES = new Set<StructuredMessage.Severity>(['WARNING', 'ERROR', 'FATAL']);
/** Default sources to show. */
const DEFAULT_SOURCES = new Set<StructuredMessage.Source>(ALL_SOURCES);

/**
 * Service for managing and persisting status message filter preferences.
 */
@Injectable({ providedIn: 'root' })
export class StatusMessageFilterService {
  private readonly router = inject(Router);
  private context?: string;

  readonly severitiesSubject = new BehaviorSubject<Set<StructuredMessage.Severity>>(new Set());
  readonly sourcesSubject = new BehaviorSubject<Set<StructuredMessage.Source>>(new Set());

  readonly severities$: Observable<Set<StructuredMessage.Severity>> =
    this.severitiesSubject.asObservable();
  readonly sources$: Observable<Set<StructuredMessage.Source>> = this.sourcesSubject.asObservable();

  constructor() {
    this.router.events
      .pipe(filter((event): event is NavigationEnd => event instanceof NavigationEnd))
      .subscribe((event: NavigationEnd) => {
        this.updateContextAndLoadFilters(event.urlAfterRedirects);
      });
    // Initialize with current URL on service creation.
    this.updateContextAndLoadFilters(this.router.url);
  }

  /** Updates and persists the selected severities for the current context. */
  updateSeverities(severities: Set<StructuredMessage.Severity>): void {
    this.severitiesSubject.next(severities);
    this.saveToStorage(this.getStorageKey(SEVERITIES_STORAGE_KEY_PREFIX), severities);
  }

  /** Updates and persists the selected sources for the current context. */
  updateSources(sources: Set<StructuredMessage.Source>): void {
    this.sourcesSubject.next(sources);
    this.saveToStorage(this.getStorageKey(SOURCES_STORAGE_KEY_PREFIX), sources);
  }

  /**
   * Returns the current set of severities to display for the current context.
   */
  messageSeveritiesToDisplay(): Set<StructuredMessage.Severity> {
    return this.severitiesSubject.getValue();
  }

  /**
   * Returns the current set of sources to display for the current context.
   */
  messageSourcesToDisplay(): Set<StructuredMessage.Source> {
    return this.sourcesSubject.getValue();
  }

  /** Checks if a message should be visible based on current filter settings. */
  isMessageVisible(message: StructuredMessage): boolean {
    const enabledSeverities = this.messageSeveritiesToDisplay();
    const enabledSources = this.messageSourcesToDisplay();
    const severity = message.severity ?? 'SEVERITY_UNSPECIFIED';
    const source = message.source ?? 'SOURCE_UNSPECIFIED';
    return enabledSeverities.has(severity) && enabledSources.has(source);
  }

  private getContextFromUrl(url: string): string {
    const path = url.split('?')[0].split('#')[0];
    const segments = path.split('/').filter((s) => s.length > 0);
    const isDetail = segments.some((segment) => /^\d+$/.test(segment));
    const resource = segments.length > 0 ? segments[0] : 'default';
    return `${resource}-${isDetail ? 'detail' : 'list'}`;
  }

  private updateContextAndLoadFilters(url: string): void {
    const newContext = this.getContextFromUrl(url);
    if (this.context !== newContext) {
      this.context = newContext;
      const storedSeverities = this.loadFromStorage<StructuredMessage.Severity>(
        this.getStorageKey(SEVERITIES_STORAGE_KEY_PREFIX),
      );
      const storedSources = this.loadFromStorage<StructuredMessage.Source>(
        this.getStorageKey(SOURCES_STORAGE_KEY_PREFIX),
      );
      this.severitiesSubject.next(storedSeverities ?? DEFAULT_SEVERITIES);
      this.sourcesSubject.next(storedSources ?? DEFAULT_SOURCES);
    }
  }

  private getStorageKey(prefix: string): string {
    return `${prefix}-${this.context}`;
  }

  private loadFromStorage<T>(key: string): Set<T> | null {
    const item = window.localStorage.getItem(key);
    if (!item) {
      return null;
    }
    try {
      const parsed = JSON.parse(item) as T[];
      return new Set(parsed);
    } catch {
      window.localStorage.removeItem(key);
      return null;
    }
  }

  private saveToStorage<T>(key: string, value: Set<T>): void {
    window.localStorage.setItem(key, JSON.stringify(Array.from(value)));
  }
}

/** Formats a status message source into a user-readable string. */
export function formatSource(source: StructuredMessage.Source | undefined): string {
  if (!source) {
    return 'Unspecified';
  }
  switch (source) {
    case 'SOURCE_UNSPECIFIED':
      return 'Unspecified';
    case 'XMANAGER_CLIENT':
      return 'XManager Client';
    case 'XMANAGER_BACKEND':
      return 'XManager Backend';
    case 'ADAPTER':
      return 'Adapter';
    case 'KUBERNETES':
      return 'Kubernetes';
    case 'SCHEDULER':
      return 'Scheduler';
    case 'APPLICATION':
      return 'Application';
    case 'USER_DEFINED':
      return 'User Defined';
    default:
      return 'Unknown';
  }
}
