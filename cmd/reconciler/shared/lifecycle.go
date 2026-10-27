// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package shared

import (
	"context"
	"strings"

	essmsgpb "xmanager_cloud/api/protos/experiment"
	taskqueuepb "xmanager_cloud/api/protos/task_queue"
	storagepkg "xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/workunit"
)

// LifecycleNotifier publishes ArtifactLifecycleEvents.
type LifecycleNotifier interface {
	NotifyLifecycleEvent(ctx context.Context, event *taskqueuepb.ArtifactLifecycleEvent) error
}

// TransitionStorageUpdater defines the interface for updating work unit executions
// and retrieving the committed state transitions.
type TransitionStorageUpdater interface {
	StorageUpdater
	UpdateWorkUnitExecutionsWithTransitions(ctx context.Context, xid int64, updates []*storagepkg.WorkUnitExecutionUpdate) (map[int64]*essmsgpb.WorkUnitExecution, map[int64]workunit.StateTransition, error)
}

// MatchesNotifyWhen reports whether state matches the notifyWhen annotation value.
func MatchesNotifyWhen(state essmsgpb.WorkUnitStatus_WorkUnitState, notifyWhen string) bool {
	if notifyWhen == "" {
		return false
	}
	for _, token := range strings.FieldsFunc(notifyWhen, isLifecycleSeparator) {
		if strings.EqualFold(token, state.String()) {
			return true
		}
	}
	return false
}

func isLifecycleSeparator(r rune) bool {
	return r == '.' || r == ',' || r == ' '
}
