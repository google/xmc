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

/** The resource type */
export enum ResourceType {
  CPU = 'CPU',
  MEMORY = 'MEMORY',
  GPU = 'GPU',
  TPU = 'TPU',
  UNKNOWN = 'UNKNOWN',
}

/** The resource chip component. */
@Component({
  selector: 'xmc-resource-chip',
  standalone: true,
  templateUrl: './resource_chip.ng.html',
  styleUrl: './resource_chip.scss',
  imports: [MatChipsModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class ResourceChip {
  readonly resourceType = input.required<ResourceType>();
  readonly text = input.required<string>();

  readonly ResourceType = ResourceType;

  protected readonly isGreyChip = computed(() => {
    const type = this.resourceType();
    return (
      type === ResourceType.CPU || type === ResourceType.MEMORY || type === ResourceType.UNKNOWN
    );
  });

  protected readonly isGreenChip = computed(() => {
    const type = this.resourceType();
    return type === ResourceType.GPU || type === ResourceType.TPU;
  });
}
