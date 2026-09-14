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

	"task/go/status"
)

var (
	// ErrTaskNotFound indicates the requested paused task does not exist.
	ErrTaskNotFound = fmt.Errorf("paused task not found: %w", status.ErrNotFound)
	// ErrTaskAlreadyExists indicates a task with the given key already exists.
	ErrTaskAlreadyExists = fmt.Errorf("paused task already exists: %w", status.ErrAlreadyExists)
)

// Repository defines operations for persisting and retrieving paused tasks.
type Repository interface {
	// Create persists a new paused task.
	Create(ctx context.Context, task *PausedTask) error

	// ListPending retrieves pending paused tasks ordered by Priority DESC, CreateTime ASC.
	// A limit <= 0 returns all pending tasks.
	ListPending(ctx context.Context, limit int) ([]*PausedTask, error)

	// MarkProcessed records resolution details for a batch of paused tasks.
	MarkProcessed(ctx context.Context, resolutions []TaskResolution) error
}
