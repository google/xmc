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

import { TestbedHarnessEnvironment } from '@angular/cdk/testing/testbed';
import { Component, signal } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';

import { ErrorBannerComponent } from './error_banner';
import { ErrorBannerHarness } from './error_banner_harness';

@Component({
  standalone: true,
  imports: [ErrorBannerComponent],
  template: `
    <xmc-error-banner
      [message]="message()"
      [showRetry]="showRetry()"
      [retryText]="retryText()"
      (retry)="onRetry()"
    />
  `,
})
class TestHostComponent {
  readonly message = signal<string>('An unexpected error occurred.');
  readonly showRetry = signal<boolean>(true);
  readonly retryText = signal<string>('Retry');
  retryClickCount = 0;

  onRetry(): void {
    this.retryClickCount++;
  }
}

describe('ErrorBannerComponent', () => {
  let fixture: ComponentFixture<TestHostComponent>;
  let hostInstance: TestHostComponent;
  let harness: ErrorBannerHarness;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [TestHostComponent, NoopAnimationsModule],
    }).compileComponents();

    fixture = TestBed.createComponent(TestHostComponent);
    fixture.detectChanges();
    hostInstance = fixture.componentInstance;
    const loader = TestbedHarnessEnvironment.loader(fixture);
    harness = await loader.getHarness(ErrorBannerHarness);
  });

  it('should render message text and retry button by default', async () => {
    expect(await harness.isVisible()).toBeTrue();
    expect(await harness.getMessage()).toContain('An unexpected error occurred.');
    expect(await harness.hasRetryButton()).toBeTrue();
    expect(await harness.getRetryButtonText()).toContain('Retry');
  });

  it('should render custom message text', async () => {
    hostInstance.message.set('Custom error message.');
    fixture.detectChanges();
    expect(await harness.getMessage()).toContain('Custom error message.');
  });

  it('should hide retry button when showRetry is false', async () => {
    hostInstance.showRetry.set(false);
    fixture.detectChanges();
    expect(await harness.isVisible()).toBeTrue();
    expect(await harness.hasRetryButton()).toBeFalse();
  });

  it('should render custom retry button text', async () => {
    hostInstance.retryText.set('Try again');
    fixture.detectChanges();
    expect(await harness.getRetryButtonText()).toContain('Try again');
  });

  it('should emit retry output when retry button is clicked', async () => {
    expect(hostInstance.retryClickCount).toBe(0);
    await harness.clickRetry();
    fixture.detectChanges();
    expect(hostInstance.retryClickCount).toBe(1);
  });
});
