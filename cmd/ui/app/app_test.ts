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

import {provideNoopAnimations} from '@angular/platform-browser/animations';
import {provideRouter} from '@angular/router';
import {of} from 'rxjs';

import {App} from './app';
import {AuthService} from './service/auth.service';

describe('App Component', () => {
  beforeEach(() => {
    const authServiceMock = jasmine.createSpyObj<AuthService>('AuthService', ['getUser']);
    authServiceMock.getUser.and.returnValue(of(null));

    setupModule({
      providers: [
        provideNoopAnimations(),
        provideRouter([]),
        {provide: AuthService, useValue: authServiceMock},
      ],
      imports: [
        App,
      ],
    });
  });

  it('should create the app', () => {
    const comp = bootstrap(App);
    expect(comp).toBeTruthy();
  });
});
