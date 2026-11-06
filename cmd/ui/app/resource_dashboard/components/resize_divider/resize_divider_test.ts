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
          controls="left-pane"
          [containerElement]="container"
          [(splitRatio)]="splitRatio"
        />
      }
    </div>
  `,
})
class TestHostComponent {
  readonly splitRatio = signal<number>(50);
  readonly showDivider = signal<boolean>(true);
  readonly containerRef = viewChild.required<ElementRef<HTMLDivElement>>('container');
}

function stubContainerRect(host: TestHostComponent, left: number, width: number): jasmine.Spy {
  return spyOn(host.containerRef().nativeElement, 'getBoundingClientRect').and.returnValue({
    left,
    width,
  } as DOMRect);
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
    expect(attrs.ariaLabel).toBe('Resize queue list and jobs panes');
    expect(attrs.title).toBe('Drag to resize sections (double-click to reset)');
    expect(attrs.ariaValueMin).toBe('20');
    expect(attrs.ariaValueMax).toBe('80');
    expect(attrs.ariaValueNow).toBe('50');
    expect(attrs.ariaValueText).toBe('Queue list 50%');
    expect(attrs.ariaControls).toBe('left-pane');
    expect(await divider.isDragging()).toBeFalse();
  });

  it('should update splitRatio while dragging, clamp to [20, 80], and detach listeners on pointerup', async () => {
    const host = bootstrap(TestHostComponent);
    await flush();
    const rectSpy = stubContainerRect(host, 100, 1000);

    const divider = await getHarness(ResizeDividerHarness);
    await divider.startDrag();
    expect(await divider.isDragging()).toBeTrue();
    expect(await divider.isFocused()).toBeTrue();

    await divider.moveDragTo(450);
    expect(host.splitRatio()).toBe(35);
    expect((await divider.getAttributes()).ariaValueNow).toBe('35');

    await divider.moveDragTo(453.7);
    expect((await divider.getAttributes()).ariaValueNow).toBe('35');

    await divider.moveDragTo(120);
    expect(host.splitRatio()).toBe(20);

    await divider.moveDragTo(1050);
    await divider.endDrag();
    expect(host.splitRatio()).toBe(80);
    expect(await divider.isDragging()).toBeFalse();
    expect(rectSpy).toHaveBeenCalledTimes(1);

    await divider.moveDragTo(500);
    expect(host.splitRatio()).toBe(80);
  });

  it('should ignore non-primary pointer buttons', async () => {
    const host = bootstrap(TestHostComponent);
    await flush();
    stubContainerRect(host, 0, 1000);

    const divider = await getHarness(ResizeDividerHarness);
    await divider.startDrag(2);
    expect(await divider.isDragging()).toBeFalse();

    await divider.moveDragTo(300);
    expect(host.splitRatio()).toBe(50);
  });

  it('should stop dragging when the pointer moves with the primary button released', async () => {
    const host = bootstrap(TestHostComponent);
    await flush();
    stubContainerRect(host, 0, 1000);

    const divider = await getHarness(ResizeDividerHarness);
    await divider.startDrag();
    await divider.moveDragTo(300, 0);
    expect(await divider.isDragging()).toBeFalse();
    expect(host.splitRatio()).toBe(50);

    await divider.moveDragTo(400);
    expect(host.splitRatio()).toBe(50);
  });

  it('should end the drag on pointercancel', async () => {
    const host = bootstrap(TestHostComponent);
    await flush();
    stubContainerRect(host, 0, 1000);

    const divider = await getHarness(ResizeDividerHarness);
    await divider.startDrag();
    await divider.cancelDrag();
    expect(await divider.isDragging()).toBeFalse();

    await divider.moveDragTo(300);
    expect(host.splitRatio()).toBe(50);
  });

  it('should ignore pointers other than the one that started the drag', async () => {
    const host = bootstrap(TestHostComponent);
    await flush();
    stubContainerRect(host, 0, 1000);

    const divider = await getHarness(ResizeDividerHarness);
    await divider.startDrag();
    await divider.moveDragTo(300, 1, 7);
    await divider.endDrag(7);
    expect(host.splitRatio()).toBe(50);
    expect(await divider.isDragging()).toBeTrue();

    await divider.moveDragTo(300);
    expect(host.splitRatio()).toBe(30);
  });

  it('should reset on double-click and support ArrowLeft, ArrowRight, Enter, and Space keys', async () => {
    const host = bootstrap(TestHostComponent);
    host.splitRatio.set(70);
    await flush();

    const divider = await getHarness(ResizeDividerHarness);
    await divider.doubleClick();
    expect(host.splitRatio()).toBe(50);

    await divider.pressKey('ArrowLeft');
    expect(host.splitRatio()).toBe(45);

    await divider.pressKey('ArrowRight');
    await divider.pressKey('ArrowRight');
    expect(host.splitRatio()).toBe(55);
    expect((await divider.getAttributes()).ariaValueNow).toBe('55');

    await divider.pressKey('Tab');
    expect(host.splitRatio()).toBe(55);

    await divider.pressKey('Enter');
    expect(host.splitRatio()).toBe(50);

    host.splitRatio.set(30);
    await flush();
    await divider.pressKey(' ');
    expect(host.splitRatio()).toBe(50);
  });

  it('should clamp keyboard resizing at the [20, 80] bounds', async () => {
    const host = bootstrap(TestHostComponent);
    host.splitRatio.set(20);
    await flush();

    const divider = await getHarness(ResizeDividerHarness);
    await divider.pressKey('ArrowLeft');
    expect(host.splitRatio()).toBe(20);

    host.splitRatio.set(80);
    await flush();
    await divider.pressKey('ArrowRight');
    expect(host.splitRatio()).toBe(80);
  });

  it('should not start dragging when the container has zero width', async () => {
    const host = bootstrap(TestHostComponent);
    await flush();
    stubContainerRect(host, 0, 0);

    const divider = await getHarness(ResizeDividerHarness);
    await divider.startDrag();
    expect(await divider.isDragging()).toBeFalse();

    await divider.moveDragTo(300);
    expect(host.splitRatio()).toBe(50);
  });

  it('should detach exactly the drag listeners it attached when destroyed mid-drag', async () => {
    const host = bootstrap(TestHostComponent);
    await flush();
    stubContainerRect(host, 0, 1000);
    const addListenerSpy = spyOn(window, 'addEventListener').and.callThrough();
    const removeListenerSpy = spyOn(window, 'removeEventListener').and.callThrough();

    const divider = await getHarness(ResizeDividerHarness);
    await divider.startDrag();
    expect(await divider.isDragging()).toBeTrue();
    const dragListeners = addListenerSpy.calls
      .allArgs()
      .filter(([type]) => String(type).startsWith('pointer'));
    expect(dragListeners.length).toBe(3);

    host.showDivider.set(false);
    await flush();

    for (const [type, listener] of dragListeners) {
      expect(removeListenerSpy).toHaveBeenCalledWith(type, listener);
    }
  });

  it('should replace the active drag when a new pointer presses the divider', async () => {
    const host = bootstrap(TestHostComponent);
    await flush();
    stubContainerRect(host, 0, 1000);

    const divider = await getHarness(ResizeDividerHarness);
    await divider.startDrag(0, 1);
    await divider.startDrag(0, 2);

    await divider.moveDragTo(300, 1, 1);
    expect(host.splitRatio()).toBe(50);

    await divider.moveDragTo(400, 1, 2);
    expect(host.splitRatio()).toBe(40);
  });
});
