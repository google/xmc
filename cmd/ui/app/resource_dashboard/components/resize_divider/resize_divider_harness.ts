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

import { ComponentHarness } from '@angular/cdk/testing';

const DEFAULT_POINTER_ID = 1;

/** Harness for interacting with <xmc-rod-resize-divider> in tests. */
export class ResizeDividerHarness extends ComponentHarness {
  static hostSelector = 'xmc-rod-resize-divider';

  private readonly separator = this.locatorFor('.rod-resize-divider');

  /** Gets accessibility and tooltip attributes of the vertical resize divider. */
  async getAttributes(): Promise<{
    role: string | null;
    ariaLabel: string | null;
    title: string | null;
    ariaValueMin: string | null;
    ariaValueMax: string | null;
    ariaValueNow: string | null;
    ariaValueText: string | null;
    ariaControls: string | null;
  }> {
    const el = await this.separator();
    return {
      role: await el.getAttribute('role'),
      ariaLabel: await el.getAttribute('aria-label'),
      title: await el.getAttribute('title'),
      ariaValueMin: await el.getAttribute('aria-valuemin'),
      ariaValueMax: await el.getAttribute('aria-valuemax'),
      ariaValueNow: await el.getAttribute('aria-valuenow'),
      ariaValueText: await el.getAttribute('aria-valuetext'),
      ariaControls: await el.getAttribute('aria-controls'),
    };
  }

  /** Returns true when the divider is actively dragging. */
  async isDragging(): Promise<boolean> {
    return (await this.separator()).hasClass('is-dragging');
  }

  /** Returns true when the divider has keyboard focus. */
  async isFocused(): Promise<boolean> {
    return (await this.separator()).isFocused();
  }

  /** Starts a pointer resize drag gesture on the divider. */
  async startDrag(button = 0, pointerId = DEFAULT_POINTER_ID): Promise<void> {
    await (await this.separator()).dispatchEvent('pointerdown', { button, pointerId });
  }

  /**
   * Moves a pointer during an active drag. The component listens on `window`, so the event is
   * dispatched there (TestBed environments only). `buttons` defaults to the primary button held.
   */
  async moveDragTo(clientX: number, buttons = 1, pointerId = DEFAULT_POINTER_ID): Promise<void> {
    await this.dispatchWindowPointerEvent('pointermove', { clientX, buttons, pointerId });
  }

  /** Releases the pointer to end an active drag. */
  async endDrag(pointerId = DEFAULT_POINTER_ID): Promise<void> {
    await this.dispatchWindowPointerEvent('pointerup', { pointerId });
  }

  /** Cancels the pointer (e.g. a browser gesture takeover) during an active drag. */
  async cancelDrag(pointerId = DEFAULT_POINTER_ID): Promise<void> {
    await this.dispatchWindowPointerEvent('pointercancel', { pointerId });
  }

  /** Double-clicks the divider to reset split widths to default. */
  async doubleClick(): Promise<void> {
    await (await this.separator()).dispatchEvent('dblclick');
  }

  /** Dispatches a keyboard event on the divider. */
  async pressKey(key: string): Promise<void> {
    await (await this.separator()).dispatchEvent('keydown', { key });
  }

  private async dispatchWindowPointerEvent(type: string, init: PointerEventInit): Promise<void> {
    window.dispatchEvent(new PointerEvent(type, { ...init, bubbles: true }));
    await this.forceStabilize();
  }
}
