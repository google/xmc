import {ChangeDetectionStrategy, Component, computed, EventEmitter, input, Output} from '@angular/core';
import {MatFormFieldModule} from '@angular/material/form-field';
import {MatSelectModule} from '@angular/material/select';
import {StructuredMessage} from 'xmanager_cloud/experiment_state_server/proto/structured_message.jsonpb_decls';
import {ALL_SEVERITIES, ALL_SOURCES, formatSource} from 'xmanager_cloud/ui/app/service/status_message_filter_service';

/**
 * Component for filter and sort options on the status messages history list.
 */
@Component({
  selector: 'xmc-status-messages-list-filter',
  standalone: true,
  templateUrl: './status_messages_list_filter.ng.html',
  styleUrl: './status_messages_list_filter.css',
  imports: [
    MatFormFieldModule,
    MatSelectModule,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class StatusMessagesListFilter {
  readonly severityFilter = input<readonly string[]>([]);
  readonly sourceFilter = input<readonly string[]>([]);
  readonly sortOrder = input<string>('DESC');

  @Output() readonly severityFilterChange = new EventEmitter<string[]>();
  @Output() readonly sourceFilterChange = new EventEmitter<string[]>();
  @Output() readonly sortOrderChange = new EventEmitter<string>();

  protected readonly severityOptions = ALL_SEVERITIES.map(
      sev => ({value: sev, label: this.formatSeverity(sev)}));
  protected readonly sourceOptions =
      ALL_SOURCES.map(src => ({value: src, label: formatSource(src)}));

  protected readonly selectedSeveritiesLabel = computed(
      () => this.getSelectionLabel(
          this.severityFilter(), this.severityOptions.length,
          s => this.formatSeverity(s)));

  protected readonly selectedSourcesLabel = computed(
      () => this.getSelectionLabel(
          this.sourceFilter(), this.sourceOptions.length,
          s => formatSource(s as StructuredMessage.Source)));

  private formatSeverity(sev: string): string {
    return sev.charAt(0).toUpperCase() + sev.slice(1).toLowerCase();
  }

  private getSelectionLabel(
      selected: readonly string[], allCount: number,
      formatFn: (s: string) => string): string {
    if (selected.length === allCount) return 'All';
    return selected.map(formatFn).join(', ');
  }
}
