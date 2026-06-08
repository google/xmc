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

import 'jasmine';

import { TestBed } from '@angular/core/testing';
import { Router } from '@angular/router';
import { of, throwError } from 'rxjs';
import { authGuard } from './auth.guard';
import { AuthService } from './service/auth.service';

describe('authGuard', () => {
  let authServiceSpy: jasmine.SpyObj<AuthService>;
  let routerSpy: jasmine.SpyObj<Router>;

  beforeEach(() => {
    authServiceSpy = jasmine.createSpyObj('AuthService', ['getUser']);
    routerSpy = jasmine.createSpyObj('Router', ['navigate']);

    TestBed.configureTestingModule({
      providers: [
        { provide: AuthService, useValue: authServiceSpy },
        { provide: Router, useValue: routerSpy }
      ]
    });
  });

  it('should return true if user is logged in', (done) => {
    authServiceSpy.getUser.and.returnValue(of({ email: 'user@example.com' } as any));

    const result = TestBed.runInInjectionContext(() => authGuard({} as any, {} as any));

    (result as any).subscribe((res: boolean) => {
      expect(res).toBeTrue();
      expect(routerSpy.navigate).not.toHaveBeenCalled();
      done();
    });
  });

  it('should redirect to / and return false if user is not logged in', (done) => {
    authServiceSpy.getUser.and.returnValue(of(null));

    const result = TestBed.runInInjectionContext(() => authGuard({} as any, {} as any));

    (result as any).subscribe((res: boolean) => {
      expect(res).toBeFalse();
      expect(routerSpy.navigate).toHaveBeenCalledWith(['/']);
      done();
    });
  });

  it('should redirect to / and return false if auth service throws an error', (done) => {
    authServiceSpy.getUser.and.returnValue(throwError(() => new Error('Auth error')));

    const result = TestBed.runInInjectionContext(() => authGuard({} as any, {} as any));

    (result as any).subscribe((res: boolean) => {
      expect(res).toBeFalse();
      expect(routerSpy.navigate).toHaveBeenCalledWith(['/']);
      done();
    });
  });
});
