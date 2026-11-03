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

import { ChangeDetectionStrategy, Component, input, model, OnDestroy } from '@angular/core';

export const DEFAULT_SPLIT_RATIO_PERCENT = 50;
const MIN_SPLIT_RATIO_PERCENT = 20;
const MAX_SPLIT_RATIO_PERCENT = 80;
const KEYBOARD_RESIZE_STEP_PERCENT = 5;

const KEYBOARD_STEP_BY_KEY: Readonly<Record<string, number>> = {
  ArrowLeft: -KEYBOARD_RESIZE_STEP_PERCENT,
  ArrowRight: KEYBOARD_RESIZE_STEP_PERCENT,
};

/** Draggable and keyboard-accessible vertical split-pane divider for the Resource Dashboard. */
@Component({
  selector: 'xmc-rod-resize-divider',
  standalone: true,
  templateUrl: './resize_divider.ng.html',
  styleUrl: './resize_divider.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class ResizeDividerComponent implements OnDestroy {
  readonly containerElement = input<HTMLElement | null>(null);
  readonly minRatio = input<number>(MIN_SPLIT_RATIO_PERCENT);
  readonly maxRatio = input<number>(MAX_SPLIT_RATIO_PERCENT);
  readonly ariaLabel = input<string>('Drag to resize sections (double-click to reset)');

  readonly splitRatio = model<number>(DEFAULT_SPLIT_RATIO_PERCENT);
  readonly isDragging = model<boolean>(false);

  private cleanupDragListeners: (() => void) | null = null;

  protected startResize(event: MouseEvent): void {
    const container = this.containerElement();
    if (!container) {
      return;
    }
    event.preventDefault();
    this.stopResize();
    this.isDragging.set(true);

    const onMouseMove = (moveEvent: MouseEvent) => {
      const { left, width } = container.getBoundingClientRect();
      if (width <= 0) {
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

  protected resetResize(): void {
    this.splitRatio.set(this.clampRatio(DEFAULT_SPLIT_RATIO_PERCENT));
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
    return Math.max(this.minRatio(), Math.min(this.maxRatio(), ratio));
  }

  private stopResize(): void {
    this.cleanupDragListeners?.();
    this.cleanupDragListeners = null;
    this.isDragging.set(false);
  }
}
