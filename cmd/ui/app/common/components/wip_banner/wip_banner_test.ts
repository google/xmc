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

import { TestbedHarnessEnvironment } from '@angular/cdk/testing/testbed';
import { Component, signal } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';

import { WipBannerComponent } from './wip_banner';
import { WipBannerHarness } from './wip_banner_harness';

@Component({
  standalone: true,
  imports: [WipBannerComponent],
  template: `<xmc-wip-banner [text]="customText()" />`,
})
class TestHostComponent {
  readonly customText = signal<string>('Work in Progress');
}

describe('WipBannerComponent', () => {
  let fixture: ComponentFixture<TestHostComponent>;
  let hostInstance: TestHostComponent;
  let harness: WipBannerHarness;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [TestHostComponent, NoopAnimationsModule],
    }).compileComponents();

    fixture = TestBed.createComponent(TestHostComponent);
    fixture.detectChanges();
    hostInstance = fixture.componentInstance;
    const loader = TestbedHarnessEnvironment.loader(fixture);
    harness = await loader.getHarness(WipBannerHarness);
  });

  it('should render default Work in Progress banner text when input is omitted', async () => {
    const defaultFixture = TestBed.createComponent(WipBannerComponent);
    defaultFixture.detectChanges();
    const loader = TestbedHarnessEnvironment.loader(defaultFixture);
    const defaultHarness = await loader.getHarness(WipBannerHarness);
    expect(await defaultHarness.getText()).toBe('Work in Progress');
  });

  it('should render updated banner text when input changes', async () => {
    hostInstance.customText.set('Feature Coming Soon');
    fixture.detectChanges();
    expect(await harness.getText()).toBe('Feature Coming Soon');
  });
});
