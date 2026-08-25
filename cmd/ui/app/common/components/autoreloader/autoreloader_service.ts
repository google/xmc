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
import { DOCUMENT } from '@angular/common';
import { inject, Injectable } from '@angular/core';
import { BehaviorSubject, combineLatest, EMPTY, fromEvent, Observable, timer } from 'rxjs';
import { distinctUntilChanged, map, pairwise, startWith, switchMap } from 'rxjs/operators';

import { AUTO_REFRESH_INTERVAL_MS } from 'xmanager_cloud/ui/app/service/constants';

/**
 * Control interface for an auto-reloading process.
 */
export interface ReloaderControl {
  /** Observable that emits when a reload should occur. */
  readonly reloader$: Observable<void>;
  /** Starts or resumes reloader emissions. */
  start: () => void;
  /** Stops reloader emissions. */
  stop: () => void;
}

/** Options for creating a reloader. */
export interface CreateReloaderOptions {
  /**
   * The refresh interval in milliseconds.
   * @default AUTO_REFRESH_INTERVAL_MS
   */
  refreshIntervalMs?: number;
  /**
   * If true, emits immediately when start() is called. If false, waits for
   * one interval.
   * @default true
   */
  emitOnStart?: boolean;
  /**
   * If true, emits immediately when page becomes visible. If false, waits for
   * one interval.
   * @default true
   */
  emitOnVisible?: boolean;
}

/**
 * Service for creating auto-reload controls.
 */
@Injectable({ providedIn: 'root' })
export class AutoReloadService {
  private readonly document: Document = inject(DOCUMENT);
  private readonly refreshIntervalMs = inject(AUTO_REFRESH_INTERVAL_MS);

  /**
   * Creates and returns a control object for a new auto-reload process.
   * @param options Options for creating the reloader.
   */
  createReloader(options: Partial<CreateReloaderOptions> = {}): ReloaderControl {
    const finalOptions = {
      refreshIntervalMs: options.refreshIntervalMs ?? this.refreshIntervalMs,
      emitOnStart: options.emitOnStart ?? true,
      emitOnVisible: options.emitOnVisible ?? true,
    };
    const isActive$ = new BehaviorSubject<boolean>(false);
    const visibility$ = this.getVisibilityObservable();

    // Emits {prev: boolean, curr: boolean} for visibility.
    const isVisibleWithPrevious$ = visibility$.pipe(
      startWith(!this.document.hidden),
      pairwise(),
      map(([prev, curr]) => ({ prev, curr })),
    );

    const reloader$ = combineLatest([isActive$, isVisibleWithPrevious$]).pipe(
      switchMap(([isActive, visible]) => {
        if (isActive && visible.curr) {
          const justBecameVisible = !visible.prev;
          const emitImmediately = justBecameVisible
            ? finalOptions.emitOnVisible
            : finalOptions.emitOnStart;
          const initialDelay = emitImmediately ? 0 : finalOptions.refreshIntervalMs;
          return timer(initialDelay, finalOptions.refreshIntervalMs);
        }
        return EMPTY;
      }),
      map(() => undefined),
    );

    return { reloader$, start: () => isActive$.next(true), stop: () => isActive$.next(false) };
  }

  private getVisibilityObservable(): Observable<boolean> {
    if (typeof this.document?.addEventListener !== 'function') {
      return new BehaviorSubject(true);
    }
    return fromEvent(this.document, 'visibilitychange').pipe(
      startWith(null),
      map(() => !this.document.hidden),
      distinctUntilChanged(),
    );
  }
}
