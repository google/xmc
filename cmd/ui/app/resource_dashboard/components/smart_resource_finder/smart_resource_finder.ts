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

import { ChangeDetectionStrategy, Component, input, output, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatIconModule } from '@angular/material/icon';

export interface ResourceRequirementQuery {
  amount: number;
  resourceTypes: string[];
}

@Component({
  selector: 'xmc-smart-resource-finder',
  standalone: true,
  templateUrl: './smart_resource_finder.ng.html',
  styleUrl: './smart_resource_finder.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [FormsModule, MatIconModule, MatButtonModule],
})
export class SmartResourceFinderComponent {
  readonly availableResourceTypes = input<string[]>([]);
  readonly findMatchingQueues = output<ResourceRequirementQuery>();

  protected readonly amount = signal<number>(64);
  protected readonly selectedResourceTypes = signal<string[]>(['']);

  protected addResourceTypeFilter() {
    this.selectedResourceTypes.update((types) => [...types, '']);
  }

  protected removeResourceTypeFilter(index: number) {
    if (this.selectedResourceTypes().length <= 1) return;
    this.selectedResourceTypes.update((types) => types.filter((_, i) => i !== index));
  }

  protected updateResourceType(index: number, val: string) {
    this.selectedResourceTypes.update((types) => {
      const copy = [...types];
      copy[index] = val;
      return copy;
    });
  }

  protected onFindMatchingQueues() {
    const amountVal = this.amount();
    if (amountVal === null || amountVal === undefined || isNaN(amountVal) || amountVal <= 0) {
      return;
    }
    const uniqueTypes = Array.from(
      new Set(this.selectedResourceTypes().filter((t) => t.trim() !== '')),
    );
    this.findMatchingQueues.emit({
      amount: amountVal,
      resourceTypes: uniqueTypes,
    });
  }
}
