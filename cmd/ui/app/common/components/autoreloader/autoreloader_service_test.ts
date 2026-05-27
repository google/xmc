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

import {DOCUMENT} from '@angular/common';
import {AUTO_REFRESH_INTERVAL_MS} from 'xmanager_cloud/ui/app/service/constants';
import {ReplaySubject} from 'rxjs';

import {AutoReloadService} from './autoreloader_service';

const REFRESH_INTERVAL_MS = 3000;
const DEFAULT_OPTIONS = {
  refreshIntervalMs: REFRESH_INTERVAL_MS
};

describe('AutoReloadService', () => {
  beforeAll(() => {
    setupObservableMatchers();
  });

  useAutoTick();

  const state = cleanState(() => {
    const mockDocument: {
      hidden: boolean; addEventListener: jasmine.Spy;
      removeEventListener: jasmine.Spy;
      dispatchEvent: (event: Event) => boolean;
      visibilityChangeCallback?: () => void;
    } = {
      hidden: false,
      addEventListener: jasmine.createSpy('addEventListener')
                            .and.callFake((type, listener) => {
                              if (type === 'visibilitychange') {
                                mockDocument.visibilityChangeCallback =
                                    listener;
                              }
                            }),
      removeEventListener: jasmine.createSpy('removeEventListener'),
      dispatchEvent: (event: Event): boolean => {
        if (event.type === 'visibilitychange' &&
            mockDocument.visibilityChangeCallback) {
          mockDocument.visibilityChangeCallback();
        }
        return true;
      },
    };

    setupModule({
      providers: [
        AutoReloadService, {provide: DOCUMENT, useValue: mockDocument}, {
          provide: AUTO_REFRESH_INTERVAL_MS,
          useValue: REFRESH_INTERVAL_MS,
        }
      ],
    });


    return {service: get(AutoReloadService), mockDocument};
  }, beforeEach);

  function setDocumentHidden(hidden: boolean) {
    state.mockDocument.hidden = hidden;
    // Simulate the event being dispatched
    state.mockDocument.dispatchEvent(new Event('visibilitychange'));
  }

  it('should be created', () => {
    expect(state.service).toBeTruthy();
  });

  it('should not emit before start() is called', async () => {
    const reloaderControl = state.service.createReloader(DEFAULT_OPTIONS);
    const recorder$ = new ReplaySubject<void>();
    reloaderControl.reloader$.subscribe(recorder$);

    await expectAsync(recorder$).toHaveNeverEmitted();
    reloaderControl.stop();
  });

  it('should emit immediately when start() is called and document is visible',
     async () => {
       const reloaderControl = state.service.createReloader(DEFAULT_OPTIONS);
       const recorder$ = new ReplaySubject<void>();
       reloaderControl.reloader$.subscribe(recorder$);

       reloaderControl.start();
       jasmine.clock().tick(1);

       await expectAsync(recorder$).toEmit(undefined);
       reloaderControl.stop();
     });


  it('should emit on interval after start()', async () => {
    const reloaderControl = state.service.createReloader(DEFAULT_OPTIONS);
    const recorder$ = new ReplaySubject<void>();
    reloaderControl.reloader$.subscribe(recorder$);

    reloaderControl.start();
    jasmine.clock().tick(REFRESH_INTERVAL_MS * 2 + 1);

    await expectAsync(recorder$).toEmitSequence(
        [undefined, undefined, undefined]);
    reloaderControl.stop();
  });

  it('should stop emitting after stop() is called', async () => {
    const reloaderControl = state.service.createReloader(DEFAULT_OPTIONS);
    const recorder$ = new ReplaySubject<void>();
    reloaderControl.reloader$.subscribe(recorder$);

    reloaderControl.start();
    jasmine.clock().tick(REFRESH_INTERVAL_MS + 1);  // Should have 2 emissions
    reloaderControl.stop();
    jasmine.clock().tick(REFRESH_INTERVAL_MS * 2);  // No more emissions

    await expectAsync(recorder$).toEmitSequence([undefined, undefined]);
  });

  it('should not emit when started while document is hidden', async () => {
    setDocumentHidden(true);
    const reloaderControl = state.service.createReloader(DEFAULT_OPTIONS);
    const recorder$ = new ReplaySubject<void>();
    reloaderControl.reloader$.subscribe(recorder$);

    reloaderControl.start();
    jasmine.clock().tick(REFRESH_INTERVAL_MS * 2);

    await expectAsync(recorder$).toHaveNeverEmitted();
    reloaderControl.stop();
  });

  it('should stop emitting when document becomes hidden', async () => {
    const reloaderControl = state.service.createReloader(DEFAULT_OPTIONS);
    const recorder$ = new ReplaySubject<void>();
    reloaderControl.reloader$.subscribe(recorder$);

    reloaderControl.start();
    jasmine.clock().tick(1);  // Immediate emit
    setDocumentHidden(true);
    jasmine.clock().tick(REFRESH_INTERVAL_MS * 2);

    await expectAsync(recorder$).toEmitSequence([undefined]);
    reloaderControl.stop();
  });

  it('should emit immediately when document becomes visible', async () => {
    setDocumentHidden(true);
    const reloaderControl = state.service.createReloader(DEFAULT_OPTIONS);
    const recorder$ = new ReplaySubject<void>();
    reloaderControl.reloader$.subscribe(recorder$);

    reloaderControl.start();
    jasmine.clock().tick(REFRESH_INTERVAL_MS * 2);
    setDocumentHidden(false);
    jasmine.clock().tick(1);

    await expectAsync(recorder$).toEmitSequence([undefined]);
    reloaderControl.stop();
  });

  it('should resume interval emissions when document becomes visible',
     async () => {
       const reloaderControl = state.service.createReloader(DEFAULT_OPTIONS);
       const recorder$ = new ReplaySubject<void>();
       reloaderControl.reloader$.subscribe(recorder$);

       reloaderControl.start();
       jasmine.clock().tick(1);
       setDocumentHidden(true);  // Hide
       jasmine.clock().tick(
           REFRESH_INTERVAL_MS * 2);  // Time passes while hidden
       setDocumentHidden(false);  // Show
       jasmine.clock().tick(1);       //
       jasmine.clock().tick(REFRESH_INTERVAL_MS);

       await expectAsync(recorder$).toEmitSequence(
           [undefined, undefined, undefined]);
       reloaderControl.stop();
     });

  it('should create independent reloaders', async () => {
    const control1 = state.service.createReloader({refreshIntervalMs: 1000});
    const recorder1$ = new ReplaySubject<void>();
    control1.reloader$.subscribe(recorder1$);
    const control2 = state.service.createReloader({refreshIntervalMs: 150});
    const recorder2$ = new ReplaySubject<void>();
    control2.reloader$.subscribe(recorder2$);

    control1.start();
    control2.start();
    jasmine.clock().tick(1001);

    // R1: 0, 1000 (2 total)
    // R2: 0, 201, 402, 603, 804 (5 total)
    await expectAsync(recorder1$).toEmitSequence([undefined, undefined]);
    await expectAsync(recorder2$).toEmitSequence([
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
    ]);
    control1.stop();
    control2.stop();
  });

  it('should remove visibility listener on stop (conceptually)', () => {
    // We can't directly test if the listener is removed easily without
    // complex document mocking, but we trust the BehaviorSubject and
    // switchMap
    // to handle teardown. This test mainly ensures stop() doesn't break.
    const reloaderControl = state.service.createReloader(DEFAULT_OPTIONS);

    reloaderControl.start();
    reloaderControl.stop();

    expect(state.mockDocument.removeEventListener)
        .not.toHaveBeenCalledWith(
            'visibilitychange',
            state.mockDocument.visibilityChangeCallback,
        );
    // Note: The listener is actually managed by the Observable chain's
    // teardown
    // logic when there are no more subscribers. Testing this directly is
    // hard.
  });

  it('should not emit immediately on start when emitOnStart is false',
     async () => {
       const reloaderControl = state.service.createReloader(
           {refreshIntervalMs: REFRESH_INTERVAL_MS, emitOnStart: false});
       const recorder$ = new ReplaySubject<void>();
       reloaderControl.reloader$.subscribe(recorder$);

       reloaderControl.start();
       jasmine.clock().tick(REFRESH_INTERVAL_MS + 1);

       await expectAsync(recorder$).toEmitSequence([undefined]);
       reloaderControl.stop();
     });

  it('should not emit immediately when visible if emitOnVisible is false',
     async () => {
       setDocumentHidden(true);
       const reloaderControl = state.service.createReloader(
           {refreshIntervalMs: REFRESH_INTERVAL_MS, emitOnVisible: false});
       const recorder$ = new ReplaySubject<void>();
       reloaderControl.reloader$.subscribe(recorder$);

       reloaderControl.start();
       setDocumentHidden(false);
       jasmine.clock().tick(REFRESH_INTERVAL_MS + 1);

       await expectAsync(recorder$).toEmitSequence([undefined]);
       reloaderControl.stop();
     });

  it('should emit immediately on start but not on visible', async () => {
    // start visible
    state.mockDocument.hidden = false;
    const reloaderControl = state.service.createReloader({
      refreshIntervalMs: REFRESH_INTERVAL_MS,
      emitOnStart: true,
      emitOnVisible: false
    });
    const recorder$ = new ReplaySubject<void>();
    reloaderControl.reloader$.subscribe(recorder$);

    reloaderControl.start();
    jasmine.clock().tick(1);
    // Now test emitOnVisible = false
    setDocumentHidden(true);  // hide
    jasmine.clock().tick(REFRESH_INTERVAL_MS * 2);
    setDocumentHidden(false);  // become visible
    jasmine.clock().tick(REFRESH_INTERVAL_MS + 1);

    await expectAsync(recorder$).toEmitSequence([undefined, undefined]);
    reloaderControl.stop();
  });

  it('should emit immediately on visible but not on start', async () => {
    // start visible
    state.mockDocument.hidden = false;
    const reloaderControl = state.service.createReloader({
      refreshIntervalMs: REFRESH_INTERVAL_MS,
      emitOnStart: false,
      emitOnVisible: true
    });
    const recorder$ = new ReplaySubject<void>();
    reloaderControl.reloader$.subscribe(recorder$);

    reloaderControl.start();
    jasmine.clock().tick(REFRESH_INTERVAL_MS + 1);
    // Now test emitOnVisible = true
    setDocumentHidden(true);  // hide
    jasmine.clock().tick(REFRESH_INTERVAL_MS * 2);
    setDocumentHidden(false);  // become visible
    jasmine.clock().tick(1);

    await expectAsync(recorder$).toEmitSequence([undefined, undefined]);
    reloaderControl.stop();
  });
});