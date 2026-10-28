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
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	actuatortaskpb "xmanager_cloud/api/protos/task_queue"
	"xmanager_cloud/internal/storage"
	storagetest "xmanager_cloud/internal/storage/testing"
	"xmanager_cloud/internal/task_queue_redis/redisclient"
)

const recomputedNotifyWhen = "STARTING.RUNNING.COMPLETED.FAILED.STOPPED"

// notifyWhenRecorder is a NotifyWhenFunc fake that records the work units it was asked about.
type notifyWhenRecorder struct {
	value string
	err   error
	calls [][2]int64
}

func (r *notifyWhenRecorder) resolve(_ context.Context, xid, wid int64) (string, error) {
	r.calls = append(r.calls, [2]int64{xid, wid})
	return r.value, r.err
}

func createAction(notifyWhen string) *actuatortaskpb.Action {
	return &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Create{Create: &actuatortaskpb.CreateAction{NotifyWhen: proto.String(notifyWhen)}}}
}

func restartAction(notifyWhen string) *actuatortaskpb.Action {
	return &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Restart{Restart: &actuatortaskpb.RestartAction{NotifyWhen: proto.String(notifyWhen)}}}
}

func updateAction(notifyWhen string) *actuatortaskpb.Action {
	return &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Update{Update: &actuatortaskpb.UpdateAction{NotifyWhen: proto.String(notifyWhen)}}}
}

func TestResumptionWorker_ReplayAction(t *testing.T) {
	tests := []struct {
		name          string
		withResolver  bool
		resolverErr   error
		action        *actuatortaskpb.Action
		wantAction    *actuatortaskpb.Action
		wantResolveOn bool
	}{
		{
			name:          "create replays recomputed notify-when instead of stored empty value",
			withResolver:  true,
			action:        createAction(""),
			wantAction:    createAction(recomputedNotifyWhen),
			wantResolveOn: true,
		},
		{
			name:          "restart replays recomputed notify-when",
			withResolver:  true,
			action:        restartAction(""),
			wantAction:    restartAction(recomputedNotifyWhen),
			wantResolveOn: true,
		},
		{
			name:         "update is replayed as stored without recomputing",
			withResolver: true,
			action:       updateAction("RUNNING"),
			wantAction:   updateAction("RUNNING"),
		},
		{
			name:          "recompute failure replays stored action",
			withResolver:  true,
			resolverErr:   errors.New("list artifacts failed"),
			action:        createAction(""),
			wantAction:    createAction(""),
			wantResolveOn: true,
		},
		{
			name:       "without resolver (lifecycle disabled) action is replayed as stored",
			action:     createAction(""),
			wantAction: createAction(""),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &notifyWhenRecorder{value: recomputedNotifyWhen, err: tc.resolverErr}
			var opts []WorkerOption
			if tc.withResolver {
				opts = append(opts, WithNotifyWhen(recorder.resolve))
			}
			worker := NewResumptionWorker(nil, nil, nil, opts...)
			task := testValidTask(7, "task-7", 100, time.Now())
			task.Action = tc.action
			stored := proto.Clone(tc.action)

			got := worker.replayAction(context.Background(), task)

			if diff := cmp.Diff(tc.wantAction, got, protocmp.Transform()); diff != "" {
				t.Errorf("replayAction() mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(stored, task.Action, protocmp.Transform()); diff != "" {
				t.Errorf("stored task action was mutated (-want +got):\n%s", diff)
			}
			var wantCalls [][2]int64
			if tc.wantResolveOn {
				wantCalls = [][2]int64{{1, 7}}
			}
			if diff := cmp.Diff(wantCalls, recorder.calls); diff != "" {
				t.Errorf("resolver calls mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestResumptionWorker_ReplaysCreateWithRecomputedNotifyWhen(t *testing.T) {
	ctx := context.Background()
	pauseRepo := storagetest.NewFakePauseStateRepositoryWithState(&storage.PauseSchedulingState{ResumptionRunID: "run-1"})
	taskRepo := storagetest.NewFakePausedTaskRepository()
	task := testValidTask(1, "task-1", 100, time.Now())
	if err := taskRepo.Create(ctx, task); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	publisher := &mockPublisher{}
	recorder := &notifyWhenRecorder{value: recomputedNotifyWhen}
	worker := NewResumptionWorker(&storage.MockStorage{PauseStateRepository: pauseRepo, PausedTaskRepository: taskRepo}, newMockTaskQueue(publisher, nil), redisclient.NewFakeClient(), WithNotifyWhen(recorder.resolve))

	if err := worker.StartResumption(ctx, Request{ResumptionRunID: "run-1", BatchSize: 10}); err != nil {
		t.Fatalf("StartResumption() error = %v", err)
	}
	if len(publisher.calls) != 1 {
		t.Fatalf("publisher calls = %d, want 1", len(publisher.calls))
	}
	if diff := cmp.Diff(createAction(recomputedNotifyWhen), publisher.calls[0].Action, protocmp.Transform()); diff != "" {
		t.Errorf("replayed action mismatch (-want +got):\n%s", diff)
	}
}
