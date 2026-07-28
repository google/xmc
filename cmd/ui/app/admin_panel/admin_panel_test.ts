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

import { provideHttpClient } from '@angular/common/http';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';

import { HealthService } from '../service/health_service';
import { MockHealthService } from '../service/testing/mock_health_service';
import { bootstrap, cleanState, flush, getEls, setupModule } from '../testing/test';
import { AdminPanelPage } from './admin_panel';

describe('AdminPanelPage Component', () => {
  cleanState(() => {
    setupModule({
      imports: [AdminPanelPage, NoopAnimationsModule],
      providers: [provideHttpClient(), { provide: HealthService, useClass: MockHealthService }],
    });
  }, beforeEach);

  it('should be created', () => {
    const comp = bootstrap(AdminPanelPage);
    expect(comp).toBeTruthy();
  });

  it('should display health status, configs and user info', async () => {
    bootstrap(AdminPanelPage);
    await flush();

    const preElements = getEls('pre.raw-json');
    expect(preElements.length).toBe(3);

    expect(preElements[0].textContent).toContain('HEALTH_STATE_HEALTHY');
    expect(preElements[0].textContent).toContain('mock-app-version-1.0.0');
    expect(preElements[1].textContent).toContain('Health Service Config');
    expect(preElements[1].textContent).toContain('mock-project');
    expect(preElements[2].textContent).toContain('mock-user@google.com');
    expect(preElements[2].textContent).toContain('PRIVILEGE_ACCESS_ADMIN_PANEL');
  });
});
