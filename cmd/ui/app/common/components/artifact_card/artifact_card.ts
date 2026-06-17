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
import { NgTemplateOutlet } from '@angular/common';
import { ChangeDetectionStrategy, Component, computed, input, Signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatDivider } from '@angular/material/divider';
import { MatExpansionModule } from '@angular/material/expansion';
import { MatIconModule } from '@angular/material/icon';
import { RouterLink } from '@angular/router';

import { Artifact } from 'xmanager_cloud/experiment_state_server/proto/artifact.jsonpb_decls';
import {
  formatArtifactDataType,
  getArtifactIcon,
} from 'xmanager_cloud/ui/app/common/artifacts/artifact_utils';
import { parseWorkUnitName } from 'xmanager_cloud/ui/app/common/resource_name_extractor';
import { trySanitizeUrl, windowOpen } from 'xmanager_cloud/ui/app/safevalues_helper';

import { Tag } from '../tag/tag';

const SHORT_NOTE_MAX_LENGTH = 40;
const ADDITIONAL_INFO_COLLAPSE_THRESHOLD = 200;

/**
 * Component for displaying a single artifact card.
 */
@Component({
  selector: 'xmc-artifact-card',
  standalone: true,
  templateUrl: './artifact_card.ng.html',
  styleUrl: './artifact_card.css',
  imports: [
    MatButtonModule,
    MatCardModule,
    MatDivider,
    MatExpansionModule,
    MatIconModule,
    NgTemplateOutlet,
    RouterLink,
    Tag,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class ArtifactCard {
  readonly artifact = input.required<Artifact>();
  // Whether the artifact is on the experiment page. If so, we add a toggle to
  // show or hide work unit artifacts, and show the work unit badge on the
  // artifact card.
  readonly isOnExperimentPage = input<boolean>(false);

  protected workUnitInfo: Signal<{ routerLink: string; badgeText: string } | null> = computed(
    () => {
      const artifact = this.artifact();
      if (this.isOnExperimentPage() && artifact.parent) {
        const ids = parseWorkUnitName(artifact.parent);
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
    const notes = this.artifact().notes;
    return !!notes && notes.length > SHORT_NOTE_MAX_LENGTH;
  });

  protected readonly additionalInfoContent = computed(() => {
    const info = this.artifact().additionalInfo;
    if (!info) return null;
    return info.codeBlock ?? info.text ?? null;
  });

  protected readonly codeLines = computed(() => {
    const info = this.artifact().additionalInfo;
    if (!info?.codeBlock) return [];
    return info.codeBlock.split('\n');
  });

  protected readonly isAdditionalInfoLong = computed(() => {
    const content = this.additionalInfoContent();
    return !!content && content.length > ADDITIONAL_INFO_COLLAPSE_THRESHOLD;
  });

  protected getIcon(artifact: Artifact): string {
    return getArtifactIcon(artifact);
  }

  protected formatDataType(dataType: Artifact.DataType | undefined): string {
    return formatArtifactDataType(dataType);
  }

  protected goToUrl(url: string | undefined) {
    if (url) {
      const sanitizedUrl = trySanitizeUrl(url);
      if (sanitizedUrl) {
        windowOpen(window, sanitizedUrl, '_blank');
      }
    }
  }
}
