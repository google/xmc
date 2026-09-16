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

package resumption

import (
	"context"

	"xmanager_cloud/logging/logging"
)

// Worker defines the contract for executing paused tasks resumption.
type Worker interface {
	Resume(ctx context.Context, req Request) error
}

// StubWorker is a placeholder resumption worker that logs received trigger requests.
type StubWorker struct{}

// NewStubWorker creates a new StubWorker.
func NewStubWorker() *StubWorker {
	return &StubWorker{}
}

// Resume logs the resumption request and returns nil.
func (w *StubWorker) Resume(ctx context.Context, req Request) error {
	logging.Info(ctx, "Stub resumption worker received resume request",
		"resumption_run_id", req.ResumptionRunID,
		"batch_size", req.BatchSize,
		"max_backlog_threshold", req.MaxBacklogThreshold,
	)
	return nil
}
