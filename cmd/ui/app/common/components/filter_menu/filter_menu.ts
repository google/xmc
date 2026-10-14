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
import { ChangeDetectionStrategy, Component, computed, input, output } from '@angular/core';
import { MatBadgeModule } from '@angular/material/badge';
import { MatButtonModule } from '@angular/material/button';
import { MatIconModule } from '@angular/material/icon';
import { MatMenuModule } from '@angular/material/menu';
import { MatTooltipModule } from '@angular/material/tooltip';

/**
 * Filter icon button that opens a menu with the projected filter sections.
 *
 * It only provides the trigger, its badge and the menu panel; each projected section owns its own
 * filtering. Sections that apply their selection in one step are driven from the parent template
 * through `opened` and `closed`.
 */
@Component({
  selector: 'xmc-filter-menu',
  standalone: true,
  templateUrl: './filter_menu.ng.html',
  styleUrl: './filter_menu.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [MatBadgeModule, MatButtonModule, MatIconModule, MatMenuModule, MatTooltipModule],
})
export class FilterMenu {
  /** Accessible name and tooltip of the trigger. */
  readonly label = input.required<string>();
  /** Number of applied filters shown on the badge; the badge is hidden at 0. */
  readonly activeFilterCount = input(0);

  /** Emits when the menu opens. */
  readonly opened = output<void>();
  /** Emits when the menu closes. */
  readonly closed = output<void>();

  /** Screen-reader text for the badge, which MatBadge otherwise hides from assistive tech. */
  protected readonly badgeDescription = computed(() => {
    const count = this.activeFilterCount();
    return count === 1 ? '1 filter applied' : `${count} filters applied`;
  });
}
