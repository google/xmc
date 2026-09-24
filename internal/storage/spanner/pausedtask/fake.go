// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package pausedtask

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"golang/protobuf/v2/proto/proto"
	"golang/uuid/uuid"
	"task/go/status"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	actuatortaskpb "xmanager_cloud/task_queue/proto/task_go_proto"
)

var _ Repository = (*Fake)(nil)

// Fake is an in-memory test double for Repository.
type Fake struct {
	mu    sync.RWMutex
	tasks []*PausedTask
}

// NewFake creates a new in-memory Fake.
func NewFake() *Fake {
	return &Fake{}
}

func cloneTask(task *PausedTask) *PausedTask {
	cp := *task
	if task.Action != nil {
		cp.Action = proto.Clone(task.Action).(*actuatortaskpb.Action)
	}
	if task.WorkUnit != nil {
		cp.WorkUnit = proto.Clone(task.WorkUnit).(*essmsgpb.WorkUnit)
	}
	if task.ProcessedTime != nil {
		pt := *task.ProcessedTime
		cp.ProcessedTime = &pt
	}
	return &cp
}

func (f *Fake) findTask(xid, wid int64, taskID string) *PausedTask {
	for _, task := range f.tasks {
		if task.XID == xid && task.WID == wid && task.TaskID == taskID {
			return task
		}
	}
	return nil
}

// Create stores a new paused task in memory.
func (f *Fake) Create(ctx context.Context, task *PausedTask) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if task == nil {
		return fmt.Errorf("%w: task cannot be nil", status.ErrInvalidArgument)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if task.TaskID == "" {
		task.TaskID = uuid.New()
	}

	if f.findTask(task.XID, task.WID, task.TaskID) != nil {
		return fmt.Errorf("%w: (xid=%d, wid=%d, task_id=%s)", ErrTaskAlreadyExists, task.XID, task.WID, task.TaskID)
	}

	stored := cloneTask(task)
	if stored.CreateTime.IsZero() {
		stored.CreateTime = time.Now()
	}

	f.tasks = append(f.tasks, stored)
	return nil
}

// List returns all paused tasks from memory in insertion order (test helper).
func (f *Fake) List(ctx context.Context) ([]*PausedTask, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	if len(f.tasks) == 0 {
		return nil, nil
	}

	result := make([]*PausedTask, len(f.tasks))
	for i, task := range f.tasks {
		result[i] = cloneTask(task)
	}
	return result, nil
}

// ListPending returns pending paused tasks ordered by Priority DESC, CreateTime ASC.
func (f *Fake) ListPending(ctx context.Context, limit int) ([]*PausedTask, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	var pending []*PausedTask
	for _, task := range f.tasks {
		if task.ProcessedTime == nil {
			pending = append(pending, cloneTask(task))
		}
	}
	if len(pending) == 0 {
		return nil, nil
	}

	sort.SliceStable(pending, func(i, j int) bool {
		if pending[i].Priority != pending[j].Priority {
			return pending[i].Priority > pending[j].Priority
		}
		return pending[i].CreateTime.Before(pending[j].CreateTime)
	})

	if limit > 0 && len(pending) > limit {
		pending = pending[:limit]
	}
	return pending, nil
}

// MarkProcessed updates resolution fields for the specified tasks.
func (f *Fake) MarkProcessed(ctx context.Context, resolutions []TaskResolution) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	matched := make([]*PausedTask, len(resolutions))
	for i, res := range resolutions {
		task := f.findTask(res.XID, res.WID, res.TaskID)
		if task == nil {
			return fmt.Errorf("%w: (xid=%d, wid=%d, task_id=%s)", ErrTaskNotFound, res.XID, res.WID, res.TaskID)
		}
		matched[i] = task
	}

	now := time.Now()
	for i, res := range resolutions {
		task := matched[i]
		t := res.ProcessedTime
		if t.IsZero() {
			t = now
		}
		task.ProcessedTime = &t
		task.ResolutionStatus = res.ResolutionStatus
		task.ResumptionRunID = res.ResumptionRunID
	}
	return nil
}

// Reset clears all tasks from memory (test helper).
func (f *Fake) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tasks = nil
}
