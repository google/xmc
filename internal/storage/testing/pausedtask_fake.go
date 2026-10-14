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

package storagetest

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"google.golang.org/protobuf/proto"

	essmsgpb "xmanager_cloud/api/protos/experiment"
	actuatortaskpb "xmanager_cloud/api/protos/task_queue"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
)

var _ storage.PausedTaskRepository = (*FakePausedTaskRepository)(nil)
var _ storage.PausedTaskTxRepository = (*FakePausedTaskRepository)(nil)

// FakePausedTaskRepository is an in-memory test double for storage.PausedTaskRepository.
type FakePausedTaskRepository struct {
	mu    sync.RWMutex
	tasks []*storage.PausedTask
}

// NewFakePausedTaskRepository creates a new in-memory FakePausedTaskRepository.
func NewFakePausedTaskRepository() *FakePausedTaskRepository {
	return &FakePausedTaskRepository{}
}

func cloneTask(task *storage.PausedTask) *storage.PausedTask {
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

func (f *FakePausedTaskRepository) findTask(xid, wid int64, taskID string) *storage.PausedTask {
	for _, task := range f.tasks {
		if task.XID == xid && task.WID == wid && task.TaskID == taskID {
			return task
		}
	}
	return nil
}

// Create stores a new paused task in memory.
func (f *FakePausedTaskRepository) Create(ctx context.Context, task *storage.PausedTask) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if task == nil {
		return fmt.Errorf("%w: task cannot be nil", grpcerr.ErrInvalidArgument)
	}
	if task.Action == nil {
		return fmt.Errorf("%w: task action cannot be nil", grpcerr.ErrInvalidArgument)
	}
	if task.TaskID == "" {
		return fmt.Errorf("%w: task ID cannot be empty", grpcerr.ErrInvalidArgument)
	}
	if task.CreateTime.IsZero() {
		return fmt.Errorf("%w: create time cannot be zero", grpcerr.ErrInvalidArgument)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if f.findTask(task.XID, task.WID, task.TaskID) != nil {
		return fmt.Errorf("%w: (xid=%d, wid=%d, task_id=%s)", storage.ErrPausedTaskAlreadyExists, task.XID, task.WID, task.TaskID)
	}

	f.tasks = append(f.tasks, cloneTask(task))
	return nil
}

// List returns all paused tasks from memory in insertion order (test helper).
func (f *FakePausedTaskRepository) List(ctx context.Context) ([]*storage.PausedTask, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	if len(f.tasks) == 0 {
		return nil, nil
	}

	result := make([]*storage.PausedTask, len(f.tasks))
	for i, task := range f.tasks {
		result[i] = cloneTask(task)
	}
	return result, nil
}

// ListPending returns pending paused tasks ordered by Priority DESC, CreateTime ASC.
// Because this fake is not backed by a WorkUnit store, tests populate task.WorkUnit on Create
// to represent the current state of the parent WorkUnit returned by ListPending.
func (f *FakePausedTaskRepository) ListPending(ctx context.Context, limit int) ([]*storage.PausedTask, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	var pending []*storage.PausedTask
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
func (f *FakePausedTaskRepository) MarkProcessed(ctx context.Context, resolutions []storage.PausedTaskResolution) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, res := range resolutions {
		if res.ProcessedTime.IsZero() {
			return fmt.Errorf("%w: processed time cannot be zero", grpcerr.ErrInvalidArgument)
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	matched := make([]*storage.PausedTask, len(resolutions))
	for i, res := range resolutions {
		task := f.findTask(res.XID, res.WID, res.TaskID)
		if task == nil {
			return fmt.Errorf("%w: (xid=%d, wid=%d, task_id=%s)", storage.ErrPausedTaskNotFound, res.XID, res.WID, res.TaskID)
		}
		matched[i] = task
	}

	for i, res := range resolutions {
		task := matched[i]
		t := res.ProcessedTime
		task.ProcessedTime = &t
		task.ResolutionStatus = res.ResolutionStatus
		task.ResumptionRunID = res.ResumptionRunID
	}
	return nil
}

// Reset clears all tasks from memory (test helper).
func (f *FakePausedTaskRepository) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tasks = nil
}
