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

import { Component } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import {
  ActivatedRouteSnapshot,
  GuardResult,
  provideRouter,
  Router,
  RouterOutlet,
  RouterStateSnapshot,
  UrlTree,
} from '@angular/router';
import { firstValueFrom, Observable, of } from 'rxjs';

import { authGuard } from './auth.guard';
import { HomePage } from './homepage/home_page';
import { HomePageHarness } from './homepage/home_page_harness';
import { AuthService, UserResponse } from './service/auth.service';
import { bootstrap, cleanState, getHarness, setupModule } from './testing/test';

const DESTINATION = '/experiments/30054?filter=needs%20review&tag=a%2Bb#logs';
const USER: UserResponse = {
  email: 'user@example.com',
  name: 'User',
  pictureUrl: '',
  sessionExpiresAt: 0,
  allowedDomains: [],
};

@Component({ template: '', standalone: true })
class ProtectedPage {}

@Component({ template: '<router-outlet />', imports: [RouterOutlet], standalone: true })
class TestApp {}

function setup() {
  return cleanState(() => {
    const authService = jasmine.createSpyObj<AuthService>('AuthService', ['getUser']);
    authService.getUser.and.returnValue(of(null));
    setupModule({
      imports: [TestApp, HomePage, ProtectedPage],
      providers: [
        { provide: AuthService, useValue: authService },
        provideRouter([
          { path: '', component: HomePage, pathMatch: 'full' },
          { path: 'experiments/:id', component: ProtectedPage, canActivate: [authGuard] },
        ]),
      ],
    });
    return { authService, router: TestBed.inject(Router) };
  }, beforeEach);
}

function runGuard(): Promise<GuardResult> {
  const result = TestBed.runInInjectionContext(() =>
    authGuard(new ActivatedRouteSnapshot(), { url: DESTINATION } as RouterStateSnapshot),
  );
  return firstValueFrom(result as Observable<GuardResult>);
}

describe('authGuard', () => {
  const state = setup();

  it('allows authenticated users to open the requested page', async () => {
    state.authService.getUser.and.returnValue(of(USER));

    expect(await runGuard()).toBeTrue();
    bootstrap(TestApp);
    await state.router.navigateByUrl(DESTINATION);
    expect(state.router.url).toBe(DESTINATION);
  });

  for (const user of [null, { ...USER, email: '' }]) {
    it(`preserves the destination when the user is ${user ? 'missing an email' : 'signed out'}`, async () => {
      state.authService.getUser.and.returnValue(of(user));

      const result = await runGuard();

      expect(result instanceof UrlTree).toBeTrue();
      expect(state.router.serializeUrl(result as UrlTree)).toBe(
        `/?return_to=${encodeURIComponent(DESTINATION)}`,
      );
    });
  }

  it('passes the initial route through the homepage sign-in button', async () => {
    const openSpy = spyOn(window, 'open').and.stub();
    bootstrap(TestApp);

    await state.router.navigateByUrl(DESTINATION);
    const homePage = await getHarness(HomePageHarness);
    await homePage.signIn();

    expect(openSpy).toHaveBeenCalledWith(
      `/api/login?return_to=${encodeURIComponent(DESTINATION)}`,
      '_self',
      undefined,
    );
  });
});
