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

import { cleanState, setupModule } from './testing/test';
import 'jasmine';

import { TestBed } from '@angular/core/testing';
import { Router, UrlTree } from '@angular/router';
import { of } from 'rxjs';
import { guestGuard } from './guest.guard';
import { AuthService } from './service/auth.service';

function setup() {
  return cleanState(() => {
    const authServiceSpy = jasmine.createSpyObj('AuthService', ['getUser']);
    const routerSpy = jasmine.createSpyObj('Router', ['parseUrl']);

    setupModule({
      providers: [
        { provide: AuthService, useValue: authServiceSpy },
        { provide: Router, useValue: routerSpy },
      ],
    });
    return {
      authServiceSpy,
      routerSpy,
    };
  }, beforeEach);
}

describe('guestGuard', () => {
  const state = setup();

  it('should redirect to /experiments if user is logged in', (done) => {
    state.authServiceSpy.getUser.and.returnValue(of({ email: 'user@example.com' } as any));
    const mockUrlTree = {} as UrlTree;
    state.routerSpy.parseUrl.and.returnValue(mockUrlTree);

    const result = TestBed.runInInjectionContext(() => guestGuard({} as any, {} as any));

    (result as any).subscribe((res: boolean | UrlTree) => {
      expect(res).toBe(mockUrlTree);
      expect(state.routerSpy.parseUrl).toHaveBeenCalledWith('/experiments');
      done();
    });
  });

  it('should return true if user is not logged in', (done) => {
    state.authServiceSpy.getUser.and.returnValue(of(null));

    const result = TestBed.runInInjectionContext(() => guestGuard({} as any, {} as any));

    (result as any).subscribe((res: boolean | UrlTree) => {
      expect(res).toBeTrue();
      expect(state.routerSpy.parseUrl).not.toHaveBeenCalled();
      done();
    });
  });
});
