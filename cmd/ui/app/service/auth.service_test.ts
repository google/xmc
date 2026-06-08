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

describe('AuthService', () => {
  let service: AuthService;
  let httpMock: HttpTestingController;

  beforeEach(() => {
    TestBed.configureTestingModule({
      imports: [HttpClientTestingModule],
      providers: [AuthService]
    });
    service = TestBed.inject(AuthService);
    httpMock = TestBed.inject(HttpTestingController);
  });

  afterEach(() => {
    httpMock.verify();
  });

  it('should request user data and cache it', () => {
    const mockUser: UserResponse = {
      email: 'user@example.com',
      name: 'Test User',
      pictureUrl: 'pic.jpg',
      sessionExpiresAt: 12345,
      allowedDomains: ['example.com']
    };

    service.getUser().subscribe((user) => {
      expect(user).toEqual(mockUser);
    });

    const req = httpMock.expectOne('/api/user');
    expect(req.request.method).toBe('GET');
    expect(req.request.withCredentials).toBeTrue();
    req.flush(mockUser);

    service.getUser().subscribe((user) => {
      expect(user).toEqual(mockUser);
    });

    httpMock.expectNone('/api/user');
  });

  it('should catch error and return null on failed HTTP request', () => {
    service.getUser().subscribe((user) => {
      expect(user).toBeNull();
    });

    const req = httpMock.expectOne('/api/user');
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

    service.getUser().subscribe();
    let req = httpMock.expectOne('/api/user');
    req.flush(mockUser);

    service.clearCache();

    service.getUser().subscribe();
    req = httpMock.expectOne('/api/user');
    req.flush(mockUser);
  });
});
