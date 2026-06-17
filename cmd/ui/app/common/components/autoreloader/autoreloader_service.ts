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
import { inject, Injectable, InjectionToken } from '@angular/core';
import { BehaviorSubject, combineLatest, EMPTY, Observable, timer } from 'rxjs';
import { distinctUntilChanged, map, mapTo, pairwise, startWith, switchMap } from 'rxjs/operators';

import { AUTO_REFRESH_INTERVAL_MS } from 'xmanager_cloud/ui/app/service/constants';

/** Injection token for the window object. */
export const WINDOW = new InjectionToken<Window>('WindowToken', {
  providedIn: 'root',
  factory: () => window,
});

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
  private readonly window: Window = inject(WINDOW);
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
      switchMap(([isActive, visible]) =>
        isActive && visible.curr
          ? timer(
              this.getInitialDelay(
                visible,
                finalOptions.emitOnStart,
                finalOptions.emitOnVisible,
                finalOptions.refreshIntervalMs,
              ),
              finalOptions.refreshIntervalMs,
            )
          : EMPTY,
      ),
      mapTo(undefined),
    );

    return { reloader$, start: () => isActive$.next(true), stop: () => isActive$.next(false) };
  }

  private getInitialDelay(
    visible: { prev: boolean; curr: boolean },
    emitOnStart: boolean,
    emitOnVisible: boolean,
    refreshIntervalMs: number
  ): number {
    const justBecameVisible = !visible.prev;
    const emitImmediately = justBecameVisible ? emitOnVisible : emitOnStart;
    return emitImmediately ? 0 : refreshIntervalMs;
  }

  private getVisibilityObservable(): Observable<boolean> {
    return new Observable<boolean>((subscriber) => {
      if (typeof this.window.addEventListener !== 'function') {
        subscriber.next(true); // If there's no real DOM, assume the page is always visible.
        return;
      }
      const handler = () => {
        subscriber.next(!this.document.hidden);
      };
      this.document.addEventListener('visibilitychange', handler);
      handler(); // Initial state

      return () => {
        this.document.removeEventListener('visibilitychange', handler);
      };
    }).pipe(distinctUntilChanged());
  }
}
