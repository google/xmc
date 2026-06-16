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
import { Component, signal } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { TestbedHarnessEnvironment } from '@angular/cdk/testing/testbed';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import 'jasmine';

import { LoadingBar } from './loading_bar';
import { LoadingBarHarness } from './testing/loading_bar_harness';

@Component({
  template: '<xmc-loading-bar [isRefreshing]="isRefreshing()" />',
  imports: [LoadingBar],
  standalone: true,
})
class TestHostComponent {
  isRefreshing = signal(false);
}

describe('LoadingBar Component', () => {
  let fixture: any;
  let hostInstance: TestHostComponent;
  let loadingBarHarness: LoadingBarHarness;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [TestHostComponent, NoopAnimationsModule],
    }).compileComponents();

    fixture = TestBed.createComponent(TestHostComponent);
    fixture.detectChanges();
    hostInstance = fixture.componentInstance;
    const loader = TestbedHarnessEnvironment.loader(fixture);
    loadingBarHarness = await loader.getHarness(LoadingBarHarness);
  });

  it('should be created and display in buffer mode', async () => {
    expect(loadingBarHarness).toBeDefined();
    expect(await loadingBarHarness.getMode()).toBe('buffer');
  });

  it('should not be in refreshing mode by default', async () => {
    expect(await loadingBarHarness.isRefreshing()).toBeFalse();
  });

  it('should not be in refreshing mode when isRefreshing is false', async () => {
    hostInstance.isRefreshing.set(false);
    fixture.detectChanges();
    expect(await loadingBarHarness.isRefreshing()).toBeFalse();
  });

  it('should be in refreshing mode when isRefreshing is true', async () => {
    hostInstance.isRefreshing.set(true);
    fixture.detectChanges();
    expect(await loadingBarHarness.isRefreshing()).toBeTrue();
  });
});
