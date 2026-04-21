import {ChangeDetectionStrategy, Component, input} from '@angular/core';
import {MatChipsModule} from '@angular/material/chips';

/** The resource type */
export enum ResourceType {
  CPU = 'CPU',
  MEMORY = 'MEMORY',
  GPU = 'GPU',
}

/** The resource chip component. */
@Component({
  selector: 'xmc-resource-chip',
  standalone: true,
  templateUrl: './resource_chip.ng.html',
  styleUrl: './resource_chip.css',
  imports: [MatChipsModule],
  changeDetection: ChangeDetectionStrategy.OnPush
})
export class ResourceChip {
  readonly resourceType = input.required<ResourceType>();
  readonly text = input.required<string>();

  readonly ResourceType = ResourceType;
}
