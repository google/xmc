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
import { JsonPipe, NgTemplateOutlet } from '@angular/common';
import { ChangeDetectionStrategy, Component, computed, input, Signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatDivider } from '@angular/material/divider';
import { MatExpansionModule } from '@angular/material/expansion';
import { MatIconModule } from '@angular/material/icon';
import { RouterLink } from '@angular/router';

import {
  getArtifactDisplayProperties,
  hasPayloadContent,
} from 'app/common/artifacts/artifact_utils';
import { PayloadType, TypedArtifact } from 'app/common/artifacts/interfaces';
import { SafeUrlPipe, SplitPipe } from 'app/common/pipes/pipes';
import { parseWorkUnitName } from 'app/common/resource_name_extractor';

import { Tag } from '../tag/tag';

const SHORT_NOTE_MAX_LENGTH = 40;
export const ADDITIONAL_INFO_COLLAPSE_THRESHOLD = 200;

/**
 * Component for displaying a single artifact card.
 */
@Component({
  selector: 'xmc-artifact-card',
  standalone: true,
  templateUrl: './artifact_card.ng.html',
  styleUrl: './artifact_card.scss',
  imports: [
    JsonPipe,
    MatButtonModule,
    MatCardModule,
    MatDivider,
    MatExpansionModule,
    MatIconModule,
    NgTemplateOutlet,
    RouterLink,
    SafeUrlPipe,
    SplitPipe,
    Tag,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class ArtifactCard {
  readonly artifact = input.required<TypedArtifact>();
  // Whether the artifact is on the experiment page. If so, we add a toggle to
  // show or hide work unit artifacts, and show the work unit badge on the
  // artifact card.
  readonly isOnExperimentPage = input<boolean>(false);

  readonly collapseThreshold = ADDITIONAL_INFO_COLLAPSE_THRESHOLD;
  protected readonly PayloadType = PayloadType;

  protected readonly displayProperties = computed(() =>
    getArtifactDisplayProperties(this.artifact()),
  );

  protected readonly tags = computed(() => this.artifact().artifact.tags ?? []);
  protected readonly notes = computed(() => this.artifact().artifact.notes ?? '');

  protected workUnitInfo: Signal<{ routerLink: string; badgeText: string } | null> = computed(
    () => {
      const art = this.artifact();
      if (this.isOnExperimentPage() && art.artifact.parent) {
        const ids = parseWorkUnitName(art.artifact.parent);
        if (ids) {
          return {
            routerLink: `/experiments/${ids.xid}/workUnits/${ids.wid}`,
            badgeText: `WU ${ids.wid}`,
          };
        }
      }
      return null;
    },
  );

  protected readonly isNoteLong = computed(() => {
    const notes = this.notes();
    return !!notes && notes.length > SHORT_NOTE_MAX_LENGTH;
  });

  protected readonly hasPayloadContent = computed(() => hasPayloadContent(this.artifact()));
}
