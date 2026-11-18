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
import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import { ActivatedRoute, Params, provideRouter } from '@angular/router';
import { BehaviorSubject } from 'rxjs';

import { bootstrap, bootstrapTemplate, cleanState, getHarness, setupModule } from '../testing/test';
import { HomePage } from './home_page';
import { HomePageHarness } from './home_page_harness';

describe('HomePage Component', () => {
  const state = cleanState(() => {
    const queryParams = new BehaviorSubject<Params>({});
    const mockRoute = new ActivatedRoute();
    mockRoute.queryParams = queryParams;

    setupModule({
      imports: [HomePage, NoopAnimationsModule],
      providers: [provideRouter([]), { provide: ActivatedRoute, useFactory: () => mockRoute }],
    });

    return {
      queryParams,
    };
  }, beforeEach);

  it('should be created', () => {
    const comp = bootstrap(HomePage);
    expect(comp).toBeTruthy();
  });

  it('should not display error message by default', () => {
    const comp = bootstrap(HomePage);
    expect(comp.errorMessage()).toBeNull();
  });

  it('should set error message for forbidden error', () => {
    state.queryParams.next({ error: 'forbidden', email: 'user@example.com' });
    const comp = bootstrap(HomePage);
    expect(comp.errorMessage()).toBe(
      'Your account (user@example.com) is not authorized. Please contact your administrator to request access.',
    );
  });

  it('should set error message for permission error', () => {
    state.queryParams.next({ error: 'permission_error' });
    const comp = bootstrap(HomePage);
    expect(comp.errorMessage()).toBe(
      'Failed to verify authorization permissions. Please contact your administrator.',
    );
  });

  it('should set generic error message for other errors', () => {
    state.queryParams.next({ error: 'other_error' });
    const comp = bootstrap(HomePage);
    expect(comp.errorMessage()).toBe('An authentication error occurred. Please try again.');
  });

  it('should set error message for forbidden error when email is missing', () => {
    state.queryParams.next({ error: 'forbidden' });
    const comp = bootstrap(HomePage);
    expect(comp.errorMessage()).toBe(
      'Your account () is not authorized. Please contact your administrator to request access.',
    );
  });

  for (const returnTo of [undefined, '']) {
    it(`opens the login endpoint when the destination is ${returnTo === undefined ? 'missing' : 'empty'}`, async () => {
      const openSpy = spyOn(window, 'open').and.stub();
      state.queryParams.next({ return_to: returnTo });
      bootstrapTemplate('<xmc-home-page />');

      const harness = await getHarness(HomePageHarness);
      await harness.signIn();

      expect(openSpy).toHaveBeenCalledWith('/api/login', '_self', undefined);
    });
  }

  it('passes the destination path, query, and fragment to the login endpoint', async () => {
    const openSpy = spyOn(window, 'open').and.stub();
    const returnTo = '/experiments/30054?filter=needs%20review&tag=a%2Bb#logs';
    state.queryParams.next({ return_to: returnTo });
    bootstrapTemplate('<xmc-home-page />');

    const harness = await getHarness(HomePageHarness);
    await harness.signIn();

    expect(openSpy).toHaveBeenCalledWith(
      `/api/login?return_to=${encodeURIComponent(returnTo)}`,
      '_self',
      undefined,
    );
  });
});
