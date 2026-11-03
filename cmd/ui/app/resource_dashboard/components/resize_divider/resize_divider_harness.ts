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
  }> {
    const el = await this.separator();
    return {
      role: await el.getAttribute('role'),
      ariaLabel: await el.getAttribute('aria-label'),
      title: await el.getAttribute('title'),
      ariaValueMin: await el.getAttribute('aria-valuemin'),
      ariaValueMax: await el.getAttribute('aria-valuemax'),
      ariaValueNow: await el.getAttribute('aria-valuenow'),
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
  async startDrag(clientX = 500, button = 0): Promise<void> {
    await (await this.separator()).dispatchEvent('mousedown', { clientX, button });
  }

  /**
   * Moves the pointer during an active drag. The component listens on `window`, so the event is
   * dispatched there. `buttons` defaults to the primary button being held.
   */
  async moveDragTo(clientX: number, buttons = 1): Promise<void> {
    window.dispatchEvent(new MouseEvent('mousemove', { clientX, buttons, bubbles: true }));
    await this.forceStabilize();
  }

  /** Releases the pointer to end an active drag. */
  async endDrag(): Promise<void> {
    window.dispatchEvent(new MouseEvent('mouseup', { bubbles: true }));
    await this.forceStabilize();
  }

  /** Double-clicks the divider to reset split widths to default. */
  async doubleClick(): Promise<void> {
    await (await this.separator()).dispatchEvent('dblclick');
  }

  /** Dispatches a keyboard event on the divider. */
  async pressKey(key: string): Promise<void> {
    await (await this.separator()).dispatchEvent('keydown', { key });
  }
}
