import {ChangeDetectionStrategy, Component, computed, input} from '@angular/core';
import {MatChipsModule} from '@angular/material/chips';
import {RouterLink} from '@angular/router';

/**
 * Display a tag in a chip.
 */
@Component({
  selector: 'xmc-tag',
  standalone: true,
  templateUrl: './tag.ng.html',
  styleUrl: './tag.css',
  imports: [MatChipsModule, RouterLink],
  changeDetection: ChangeDetectionStrategy.OnPush
})
export class Tag {
  readonly text = input.required<string>();
  readonly disableLinks = input(false);
  readonly encodedTag = computed(() => this.text().trim().replace(/\\/g, '\\\\').replace(/"/g, '\\"'));
}
