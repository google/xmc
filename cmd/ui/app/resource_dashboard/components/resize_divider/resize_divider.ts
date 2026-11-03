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

import { ChangeDetectionStrategy, Component, input, model, OnDestroy, signal } from '@angular/core';

export const DEFAULT_SPLIT_RATIO_PERCENT = 50;
const MIN_SPLIT_RATIO_PERCENT = 20;
const MAX_SPLIT_RATIO_PERCENT = 80;
const KEYBOARD_RESIZE_STEP_PERCENT = 5;
const PRIMARY_MOUSE_BUTTON = 0;
const PRIMARY_MOUSE_BUTTON_MASK = 1;

const KEYBOARD_STEP_BY_KEY: Readonly<Record<string, number>> = {
  ArrowLeft: -KEYBOARD_RESIZE_STEP_PERCENT,
  ArrowRight: KEYBOARD_RESIZE_STEP_PERCENT,
};

/**
 * Draggable and keyboard-accessible vertical split-pane divider for the Resource Dashboard.
 * The split resets to DEFAULT_SPLIT_RATIO_PERCENT and is clamped to [20%, 80%].
 */
@Component({
  selector: 'xmc-rod-resize-divider',
  standalone: true,
  templateUrl: './resize_divider.ng.html',
  styleUrl: './resize_divider.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class ResizeDividerComponent implements OnDestroy {
  /** Flex container whose full width maps to a 0–100% split ratio. */
  readonly containerElement = input.required<HTMLElement>();
  /** Width of the left pane as a percentage of the container width. */
  readonly splitRatio = model<number>(DEFAULT_SPLIT_RATIO_PERCENT);

  protected readonly isDragging = signal<boolean>(false);
  protected readonly minRatio = MIN_SPLIT_RATIO_PERCENT;
  protected readonly maxRatio = MAX_SPLIT_RATIO_PERCENT;

  private cleanupDragListeners: (() => void) | null = null;

  protected startResize(event: MouseEvent): void {
    if (event.button !== PRIMARY_MOUSE_BUTTON) {
      return;
    }
    // Container geometry is captured once per drag: dragging only changes child flex-basis.
    const { left, width } = this.containerElement().getBoundingClientRect();
    if (width <= 0) {
      return;
    }
    event.preventDefault();
    (event.currentTarget as HTMLElement).focus({ preventScroll: true });
    this.stopResize();
    this.isDragging.set(true);
    this.listenToDrag(left, width);
  }

  protected resetResize(): void {
    this.splitRatio.set(DEFAULT_SPLIT_RATIO_PERCENT);
  }

  protected onKeyDown(event: KeyboardEvent): void {
    const step = KEYBOARD_STEP_BY_KEY[event.key];
    if (step !== undefined) {
      event.preventDefault();
      this.splitRatio.update((ratio) => this.clampRatio(ratio + step));
      return;
    }
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      this.resetResize();
    }
  }

  ngOnDestroy(): void {
    this.stopResize();
  }

  private clampRatio(ratio: number): number {
    return Math.max(MIN_SPLIT_RATIO_PERCENT, Math.min(MAX_SPLIT_RATIO_PERCENT, ratio));
  }

  private listenToDrag(left: number, width: number): void {
    const onMouseMove = (moveEvent: MouseEvent) => {
      // A release outside the window (e.g. over a context menu) may never deliver mouseup.
      if ((moveEvent.buttons & PRIMARY_MOUSE_BUTTON_MASK) === 0) {
        this.stopResize();
        return;
      }
      this.splitRatio.set(this.clampRatio(((moveEvent.clientX - left) / width) * 100));
    };
    const onMouseUp = () => this.stopResize();

    window.addEventListener('mousemove', onMouseMove);
    window.addEventListener('mouseup', onMouseUp);
    this.cleanupDragListeners = () => {
      window.removeEventListener('mousemove', onMouseMove);
      window.removeEventListener('mouseup', onMouseUp);
    };
  }

  private stopResize(): void {
    this.cleanupDragListeners?.();
    this.cleanupDragListeners = null;
    this.isDragging.set(false);
  }
}
