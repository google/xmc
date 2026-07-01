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

package server

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	msgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	tq "xmanager_cloud/task_queue/client"
	"xmanager_cloud/validator/validator"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"

)

type leaderMockRedis struct {
	redis.Cmdable
}

func (m *leaderMockRedis) SetNX(ctx context.Context, key string, value any, expiration time.Duration) *redis.BoolCmd {
	cmd := redis.NewBoolCmd(ctx)
	cmd.SetVal(true)
	return cmd
}

func (m *leaderMockRedis) Get(ctx context.Context, key string) *redis.StringCmd {
	cmd := redis.NewStringCmd(ctx)
	cmd.SetVal("replica-1")
	return cmd
}

func (m *leaderMockRedis) Expire(ctx context.Context, key string, expiration time.Duration) *redis.BoolCmd {
	cmd := redis.NewBoolCmd(ctx)
	cmd.SetVal(true)
	return cmd
}

func (m *leaderMockRedis) Eval(ctx context.Context, script string, keys []string, args ...any) *redis.Cmd {
	cmd := redis.NewCmd(ctx)
	cmd.SetVal(int64(1))
	return cmd
}

func TestWorkloadPauseResumeGatingAndDraining(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cleanupOIDC := tq.MockGenerateOIDCTokenForTest()
	defer cleanupOIDC()

	server, _ := createExperimentStateServer(ctx, t)
	realTaskQueueClient := tq.CreateTaskQueueClient(ctx, t)
	server.taskQueueClient = realTaskQueueClient
	redisFake := tq.GetFakeRedisClient(realTaskQueueClient)

	// Start background workers
	mr := &leaderMockRedis{}
	server.Start(ctx, mr, "replica-1")

	// Create a parent experiment
	exp := &msgpb.Experiment{
		Title: "test experiment for pause/resume gating",
		Acls: &xidmsgpb.XidAcls{
			Owners: []*xidmsgpb.XidAcls_Role{{Principal: &xidmsgpb.XidAcls_Role_User{User: "test@example.com"}}},
		},
	}
	createdExp := mustCreateExperiment(ctx, t, server, exp)

	// 1. Initially unpaused: CreateWorkUnit should enqueue directly to Redis
	workUnit1 := &msgpb.WorkUnit{
		WorkUnitType: msgpb.WorkUnit_STANDARD,
		Executable:   testExecutable,
	}
	_ = mustCreateWorkUnit(ctx, t, server, createdExp.GetName(), 1, workUnit1)

	if len(redisFake.Messages) != 1 {
		t.Errorf("Expected 1 task in Redis task queue, got %d", len(redisFake.Messages))
	}

	// 2. Pause scheduling globally via UpdateSchedulingPause API
	_, err := server.UpdateSchedulingPause(ctx, &apipb.UpdateSchedulingPauseRequest{Paused: true})
	if err != nil {
		t.Fatalf("Failed to call UpdateSchedulingPause: %v", err)
	}

	// Wait for polling loop to sync status (polls every 5s, so sleep 6s)
	time.Sleep(6 * time.Second)

	if !server.localPaused.Load() {
		t.Errorf("Expected localPaused cache to be true after polling database")
	}

	// 3. CreateWorkUnit while paused: should buffer in Spanner and set state to GLOBAL_PAUSE
	workUnit2 := &msgpb.WorkUnit{
		WorkUnitType: msgpb.WorkUnit_STANDARD,
		Executable:   testExecutable,
	}
	_ = mustCreateWorkUnit(ctx, t, server, createdExp.GetName(), 2, workUnit2)

	// Redis queue should still have only 1 message (from workUnit1)
	if len(redisFake.Messages) != 1 {
		t.Errorf("Expected task queue to still have 1 message, got %d", len(redisFake.Messages))
	}

	// Spanner PausedTasks should have 1 task
	tasks, err := server.storage.FetchPausedTasksBatch(ctx, 10)
	if err != nil {
		t.Fatalf("Failed to fetch paused tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("Expected 1 task in Spanner PausedTasks, got %d", len(tasks))
	}

	// WorkUnit2 state should be GLOBAL_PAUSE
	_, wid2, _ := validator.ValidateWorkUnitName(workUnit2.GetName())
	xid, _ := validator.ValidateExperimentName(createdExp.GetName())
	wu2Fetched, err := server.storage.FetchWorkUnit(ctx, xid, wid2)
	if err != nil {
		t.Fatalf("Failed to fetch work unit: %v", err)
	}
	if wu2Fetched.GetCurrentWorkUnitExecution().Status.State != msgpb.WorkUnitStatus_GLOBAL_PAUSE {
		t.Errorf("Expected WorkUnit2 state to be GLOBAL_PAUSE, got %s", wu2Fetched.GetCurrentWorkUnitExecution().Status.State)
	}

	// 4. Unpause scheduling globally via API
	_, err = server.UpdateSchedulingPause(ctx, &apipb.UpdateSchedulingPauseRequest{Paused: false})
	if err != nil {
		t.Fatalf("Failed to unpause scheduling: %v", err)
	}

	// Wait for polling loop to sync and trigger draining
	time.Sleep(6 * time.Second)

	if server.localPaused.Load() {
		t.Errorf("Expected localPaused cache to be false after unpausing")
	}

	// Task should be drained and published to Redis task queue
	if len(redisFake.Messages) != 2 {
		t.Errorf("Expected 2 tasks in Redis task queue after draining, got %d", len(redisFake.Messages))
	}

	// Spanner PausedTasks should be empty
	tasks, err = server.storage.FetchPausedTasksBatch(ctx, 10)
	if err != nil {
		t.Fatalf("Failed to fetch paused tasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("Expected 0 tasks in Spanner PausedTasks after draining, got %d", len(tasks))
	}

	// Assert that WorkUnit2 status state was updated back to SCHEDULING
	wu2FetchedAfterResume, err := server.storage.FetchWorkUnit(ctx, xid, wid2)
	if err != nil {
		t.Fatalf("Failed to fetch work unit: %v", err)
	}
	if wu2FetchedAfterResume.GetCurrentWorkUnitExecution().Status.State != msgpb.WorkUnitStatus_SCHEDULING {
		t.Errorf("Expected WorkUnit2 state to transition back to SCHEDULING after resumption, got %s", wu2FetchedAfterResume.GetCurrentWorkUnitExecution().Status.State)
	}
}
