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
import { ChangeDetectionStrategy, Component, input } from '@angular/core';
import { MatExpansionModule } from '@angular/material/expansion';
import { MatIconModule } from '@angular/material/icon';

import { getArtifactDisplayProperties } from 'xmanager_cloud/ui/app/common/artifacts/artifact_utils';
import { ArtifactListItem, PayloadType } from 'xmanager_cloud/ui/app/common/artifacts/interfaces';
import { ArtifactCard } from 'xmanager_cloud/ui/app/common/components/artifact_card/artifact_card';

/**
 * Component for displaying a column of artifacts within the same lifecycle phase.
 */
@Component({
  selector: 'xmc-artifacts-column',
  standalone: true,
  templateUrl: './artifacts_column.ng.html',
  styleUrl: './artifacts_column.css',
  imports: [MatIconModule, ArtifactCard, MatExpansionModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class ArtifactsColumn {
  readonly lifecyclePhase = input.required<string>();
  readonly items = input.required<readonly ArtifactListItem[]>();
  readonly isOnExperimentPage = input<boolean>(false);

  protected readonly getArtifactDisplayProperties = getArtifactDisplayProperties;
  protected readonly PayloadType = PayloadType;
}
