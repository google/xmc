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

import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import { provideRouter, Router } from '@angular/router';

import { EmptyComponent } from 'xmanager_cloud/ui/app/common/testing_utils/empty_component';
import { bootstrap, get, getEl, setupModule, trigger } from 'xmanager_cloud/ui/app/testing/test';

import { Header } from './header';

describe('Header Component', () => {
  let router: Router;

  beforeEach(() => {
    setupModule({
      imports: [Header, NoopAnimationsModule],
      providers: [provideRouter([{ path: 'experiments', component: EmptyComponent }])],
    });

    router = get(Router);
    spyOn(router, 'navigate');
  });

  it('should be created', () => {
    const comp = bootstrap(Header);

    expect(comp).toBeTruthy();
    expect(getEl('.logo-container').innerText).toBe('XManager');
  });

  it('should navigate to experiments page when search button is clicked', async () => {
    bootstrap(Header);

    const input = getEl<HTMLInputElement>('input');
    input.value = 'xid:123';
    await trigger(input, 'input');
    await trigger(input, 'keyup', { key: 'Enter' });

    expect(router.navigate).toHaveBeenCalledWith(['/experiments'], {
      queryParams: { filter: 'xid:123' },
    });
  });
});
