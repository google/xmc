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
import { ChangeDetectionStrategy, Component, computed, input } from '@angular/core';
import { MatChipsModule } from '@angular/material/chips';
import { RouterLink } from '@angular/router';

/**
 * Display a tag in a chip.
 */
@Component({
  selector: 'xmc-tag',
  standalone: true,
  templateUrl: './tag.ng.html',
  styleUrl: './tag.css',
  imports: [MatChipsModule, RouterLink],
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class Tag {
  readonly text = input.required<string>();
  readonly disableLinks = input(false);
  readonly encodedTag = computed(() =>
    this.text().trim().replace(/\\/g, '\\\\').replace(/"/g, '\\"'),
  );
}
