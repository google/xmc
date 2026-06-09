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
import { HttpClientTestingModule, HttpTestingController } from '@angular/common/http/testing';
import { AuthService, UserResponse } from './auth.service';

function setup() {
  return cleanState(() => {
    setupModule({
      imports: [HttpClientTestingModule],
      providers: [AuthService],
    });
    return {
      service: get(AuthService),
      httpMock: get(HttpTestingController),
    };
  }, beforeEach);
}

describe('AuthService', () => {
  const state = setup();

  afterEach(() => {
    state.httpMock.verify();
  });

  it('should request user data and cache it', () => {
    const mockUser: UserResponse = {
      email: 'user@example.com',
      name: 'Test User',
      pictureUrl: 'pic.jpg',
      sessionExpiresAt: 12345,
      allowedDomains: ['example.com']
    };

    state.service.getUser().subscribe((user) => {
      expect(user).toEqual(mockUser);
    });

    const req = state.httpMock.expectOne('/api/user');
    expect(req.request.method).toBe('GET');
    expect(req.request.withCredentials).toBeTrue();
    req.flush(mockUser);

    state.service.getUser().subscribe((user) => {
      expect(user).toEqual(mockUser);
    });

    state.httpMock.expectNone('/api/user');
  });

  it('should catch error and return null on failed HTTP request', () => {
    state.service.getUser().subscribe((user) => {
      expect(user).toBeNull();
    });

    const req = state.httpMock.expectOne('/api/user');
    req.error(new ProgressEvent('error'));
  });

  it('should invalidate cache when clearCache is called', () => {
    const mockUser: UserResponse = {
      email: 'user@example.com',
      name: 'Test User',
      pictureUrl: 'pic.jpg',
      sessionExpiresAt: 12345,
      allowedDomains: ['example.com']
    };

    state.service.getUser().subscribe();
    let req = state.httpMock.expectOne('/api/user');
    req.flush(mockUser);

    state.service.clearCache();

    state.service.getUser().subscribe();
    req = state.httpMock.expectOne('/api/user');
    req.flush(mockUser);
  });
});
