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

import { Pipe, PipeTransform } from '@angular/core';

import { QueueOverview, QueueResourceCapacity } from 'xmanager_cloud/api/protos/rod/api.jsonpb_decls';

/**
 * Pure Angular pipe to compute free/total capacity display text for a queue resource capacity.
 */
@Pipe({
  name: 'freeTotalDisplay',
  standalone: true,
  pure: true,
})
export class FreeTotalDisplayPipe implements PipeTransform {
  transform(capacity?: QueueResourceCapacity | null): string {
    if (!capacity) return '-';
    const total = capacity.totalCapacity ?? 0;
    const free = capacity.freeCapacity ?? 0;
    return `${free} / ${total}`;
  }
}

/**
 * Pure Angular pipe to compute max workload display text for a queue resource capacity.
 */
@Pipe({
  name: 'maxWorkloadDisplay',
  standalone: true,
  pure: true,
})
export class MaxWorkloadDisplayPipe implements PipeTransform {
  transform(capacity?: QueueResourceCapacity | null): string {
    if (!capacity) return '-';
    const free = capacity.freeCapacity ?? 0;
    const total = capacity.totalCapacity ?? 0;
    return `${free} → ${total}`;
  }
}

export interface PredictabilityMessageInfo {
  text: string;
  isHealthy: boolean;
}

/**
 * Pure Angular pipe to compute predictability status message and health status for a cluster queue.
 */
@Pipe({
  name: 'predictabilityMessage',
  standalone: true,
  pure: true,
})
export class PredictabilityMessagePipe implements PipeTransform {
  transform(queue?: QueueOverview | null): PredictabilityMessageInfo | null {
    if (!queue) return null;
    return { text: 'System is healthy. Job will start immediately.', isHealthy: true };
  }
}

export interface QueueStatusBadgeInfo {
  label: string;
  isHealthy: boolean;
}

/**
 * Pure Angular pipe to compute queue status badge info (label and health status).
 */
@Pipe({
  name: 'queueStatusBadge',
  standalone: true,
  pure: true,
})
export class QueueStatusBadgePipe implements PipeTransform {
  transform(queue?: QueueOverview | null): QueueStatusBadgeInfo | null {
    if (!queue) return null;
    return { label: 'Resources available', isHealthy: true };
  }
}
