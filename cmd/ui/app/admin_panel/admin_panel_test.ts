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

import { NoopAnimationsModule } from '@angular/platform-browser/animations';

import { bootstrap, cleanState, setupModule } from '../testing/test';
import { AdminPanelPage } from './admin_panel';

describe('AdminPanelPage Component', () => {
  cleanState(() => {
    setupModule({
      imports: [AdminPanelPage, NoopAnimationsModule],
    });
  }, beforeEach);

  it('should be created', () => {
    const comp = bootstrap(AdminPanelPage);
    expect(comp).toBeTruthy();
  });
});
