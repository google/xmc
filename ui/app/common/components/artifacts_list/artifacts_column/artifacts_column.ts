import {ChangeDetectionStrategy, Component, input} from '@angular/core';
import {MatExpansionModule} from '@angular/material/expansion';
import {MatIconModule} from '@angular/material/icon';

import {formatArtifactDataType} from '../../../artifacts/artifact_utils';
import {ArtifactListItem} from '../../../artifacts/interfaces';
import {ArtifactCard} from '../../artifact_card/artifact_card';

/**
 * Component for displaying a column of artifacts within the same lifecycle phase.
 */
@Component({
  selector: 'xmc-artifacts-column',
  standalone: true,
  templateUrl: './artifacts_column.ng.html',
  styleUrl: './artifacts_column.css',
  imports: [
    MatIconModule,
    ArtifactCard,
    MatExpansionModule,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class ArtifactsColumn {
  readonly lifecyclePhase = input.required<string>();
  readonly items = input.required<readonly ArtifactListItem[]>();
  readonly isOnExperimentPage = input<boolean>(false);

  protected readonly formatDataType = formatArtifactDataType;
}
