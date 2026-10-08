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
import { getHarness } from '@angular/cdk/testing/catalyst/async';
import { MatSelectHarness } from '@angular/material/select/testing';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';

import { bootstrapTemplate, getDebugEl, setupModule } from 'xmanager_cloud/ui/app/testing/test';

import { PageSizeSelector } from './page_size_selector';

describe('PageSizeSelector Component', () => {
  beforeEach(() => {
    setupModule({
      imports: [PageSizeSelector, NoopAnimationsModule],
    });
  });

  it('should be created', () => {
    bootstrapTemplate('<xmc-page-size-selector [pageSize]="10" [pageSizeOptions]="[10, 20]" />');
    expect(getDebugEl(PageSizeSelector)).toBeTruthy();
  });

  it('should display the initial page size', async () => {
    bootstrapTemplate('<xmc-page-size-selector [pageSize]="10" [pageSizeOptions]="[10, 20]" />');
    const select = await getHarness(MatSelectHarness);
    expect(await select.getValueText()).toBe('10');
  });

  it('should display page size options', async () => {
    bootstrapTemplate(
      '<xmc-page-size-selector [pageSize]="10" [pageSizeOptions]="[10, 20, 50]" />',
    );
    const select = await getHarness(MatSelectHarness);
    await select.open();
    const options = await select.getOptions();
    expect(options.length).toBe(3);
    expect(await options[0].getText()).toBe('10');
    expect(await options[1].getText()).toBe('20');
    expect(await options[2].getText()).toBe('50');
  });

  it('should emit pageSizeChange when selection changes', async () => {
    const outputSpy = jasmine.createSpy('outputSpy');
    bootstrapTemplate(
      '<xmc-page-size-selector [pageSize]="10" [pageSizeOptions]="[10, 20]" (pageSizeChange)="outputSpy($event)" />',
      { outputSpy },
    );
    const select = await getHarness(MatSelectHarness);
    await select.open();
    await select.clickOptions({ text: '20' });
    expect(outputSpy).toHaveBeenCalledWith(20);
  });
});
