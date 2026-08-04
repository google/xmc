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

import { CommonModule } from '@angular/common';
import { ChangeDetectionStrategy, Component, input, model, output } from '@angular/core';

import { QueueCardComponent } from 'xmanager_cloud/ui/app/resource_dashboard/components/queue_card/queue_card';
import { QueueOverview } from 'xmanager_cloud/api/protos/rod/api.jsonpb_decls';

@Component({
  selector: 'xmc-rod-queue-list',
  standalone: true,
  templateUrl: './queue_list.ng.html',
  styleUrl: './queue_list.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [CommonModule, QueueCardComponent],
})
export class QueueListComponent {
  readonly queues = input<QueueOverview[]>([]);
  readonly selectedQueue = input<string>('');
  readonly isLoading = input<boolean>(false);
  readonly selectQueue = output<string>();

  readonly velocityPeriod = model<string>('1h');
  protected readonly velocityPeriods: readonly string[] = ['1h', '24h', '7d'];

  protected setVelocityPeriod(period: string) {
    this.velocityPeriod.set(period);
  }

  protected onQueueSelect(queueName: string) {
    this.selectQueue.emit(queueName);
  }
}
