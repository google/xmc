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

import { Component, ElementRef, signal, viewChild } from '@angular/core';

import { bootstrap, cleanState, flush, getHarness, setupModule } from 'app/testing/test';

import { ResizeDividerComponent } from './resize_divider';
import { ResizeDividerHarness } from './resize_divider_harness';

@Component({
  standalone: true,
  imports: [ResizeDividerComponent],
  template: `
    <div #container class="test-split-container">
      @if (showDivider()) {
        <xmc-rod-resize-divider
          [containerElement]="useContainer() ? container : null"
          [(splitRatio)]="splitRatio"
          [(isDragging)]="isDragging"
        />
      }
    </div>
  `,
})
class TestHostComponent {
  readonly splitRatio = signal<number>(50);
  readonly isDragging = signal<boolean>(false);
  readonly useContainer = signal<boolean>(true);
  readonly showDivider = signal<boolean>(true);
  readonly containerRef = viewChild.required<ElementRef<HTMLDivElement>>('container');
}

describe('ResizeDividerComponent', () => {
  cleanState(() => {
    setupModule({
      imports: [TestHostComponent],
    });
    return {};
  }, beforeEach);

  it('should render WAI-ARIA separator attributes and tooltip', async () => {
    bootstrap(TestHostComponent);
    await flush();

    const divider = await getHarness(ResizeDividerHarness);
    const attrs = await divider.getAttributes();

    expect(attrs.role).toBe('separator');
    expect(attrs.ariaLabel).toBe('Drag to resize sections (double-click to reset)');
    expect(attrs.title).toBe('Drag to resize sections (double-click to reset)');
    expect(attrs.ariaValueMin).toBe('20');
    expect(attrs.ariaValueMax).toBe('80');
    expect(attrs.ariaValueNow).toBe('50');
    expect(await divider.isDragging()).toBeFalse();
  });

  it('should update splitRatio while dragging, clamp to [20, 80], and detach listeners on mouseup', async () => {
    const host = bootstrap(TestHostComponent);
    await flush();

    const rectSpy = spyOn(
      host.containerRef().nativeElement,
      'getBoundingClientRect',
    ).and.returnValue({
      left: 100,
      width: 1000,
    } as DOMRect);

    const divider = await getHarness(ResizeDividerHarness);
    await divider.startDrag(600);
    await flush();
    expect(host.isDragging()).toBeTrue();
    expect(await divider.isDragging()).toBeTrue();

    window.dispatchEvent(new MouseEvent('mousemove', { clientX: 450, bubbles: true }));
    await flush();
    expect(host.splitRatio()).toBe(35);

    window.dispatchEvent(new MouseEvent('mousemove', { clientX: 120, bubbles: true }));
    await flush();
    expect(host.splitRatio()).toBe(20);

    window.dispatchEvent(new MouseEvent('mousemove', { clientX: 1050, bubbles: true }));
    window.dispatchEvent(new MouseEvent('mouseup', { bubbles: true }));
    await flush();
    expect(host.splitRatio()).toBe(80);
    expect(host.isDragging()).toBeFalse();
    expect(await divider.isDragging()).toBeFalse();
    expect(rectSpy).toHaveBeenCalledTimes(1);

    window.dispatchEvent(new MouseEvent('mousemove', { clientX: 500, bubbles: true }));
    await flush();
    expect(host.splitRatio()).toBe(80);
  });

  it('should reset on double-click and support ArrowLeft, ArrowRight, Enter, and Space keys', async () => {
    const host = bootstrap(TestHostComponent);
    host.splitRatio.set(70);
    await flush();

    const divider = await getHarness(ResizeDividerHarness);
    await divider.doubleClick();
    await flush();
    expect(host.splitRatio()).toBe(50);

    await divider.pressKey('ArrowLeft');
    await flush();
    expect(host.splitRatio()).toBe(45);

    await divider.pressKey('ArrowRight');
    await divider.pressKey('ArrowRight');
    await flush();
    expect(host.splitRatio()).toBe(55);

    await divider.pressKey('Enter');
    await flush();
    expect(host.splitRatio()).toBe(50);

    host.splitRatio.set(30);
    await flush();
    await divider.pressKey(' ');
    await flush();
    expect(host.splitRatio()).toBe(50);
  });

  it('should not start dragging when the container has zero width', async () => {
    const host = bootstrap(TestHostComponent);
    await flush();
    spyOn(host.containerRef().nativeElement, 'getBoundingClientRect').and.returnValue({
      left: 0,
      width: 0,
    } as DOMRect);

    const divider = await getHarness(ResizeDividerHarness);
    await divider.startDrag(500);
    await flush();
    expect(host.isDragging()).toBeFalse();
    expect(await divider.isDragging()).toBeFalse();

    window.dispatchEvent(new MouseEvent('mousemove', { clientX: 300, bubbles: true }));
    await flush();
    expect(host.splitRatio()).toBe(50);
  });

  it('should ignore drag when containerElement is null and clean up active drag on destroy', async () => {
    const host = bootstrap(TestHostComponent);
    host.useContainer.set(false);
    await flush();

    const divider = await getHarness(ResizeDividerHarness);
    await divider.startDrag(500);
    await flush();
    expect(host.isDragging()).toBeFalse();

    host.useContainer.set(true);
    await flush();
    spyOn(host.containerRef().nativeElement, 'getBoundingClientRect').and.returnValue({
      left: 0,
      width: 1000,
    } as DOMRect);

    await divider.startDrag(500);
    await flush();
    expect(host.isDragging()).toBeTrue();

    host.showDivider.set(false);
    await flush();
    expect(host.isDragging()).toBeFalse();

    window.dispatchEvent(new MouseEvent('mousemove', { clientX: 300, bubbles: true }));
    await flush();
    expect(host.splitRatio()).toBe(50);
  });
});
