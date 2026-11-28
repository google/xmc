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

package k8sclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/kubernetes/common/constants"
	"xmanager_cloud/internal/task_queue_redis/redisclient"
	"xmanager_cloud/internal/validator"
	"xmanager_cloud/internal/workunit"
	"xmanager_cloud/pkg/mlflow"
)

// MLflowSyncOptions configures retry and claim-extension hooks for MLflow status sync.
type MLflowSyncOptions struct {
	ExtendClaim                func(context.Context) error
	InitialBackoff, MaxBackoff time.Duration
	InHandlerTrials            int
}

// HandleMLflowSyncTask processes a single MLflowSyncPayload from the Redis stream.
func (c *Client) HandleMLflowSyncTask(ctx context.Context, data string, opts MLflowSyncOptions) redisclient.TaskResult {
	var p workunit.MLflowSyncPayload
	if err := json.Unmarshal([]byte(data), &p); err != nil || p.XID <= 0 || p.WID <= 0 || p.EID <= 0 {
		return redisclient.TaskResult{Error: fmt.Errorf("invalid payload: %q", data)}
	}
	if c.storage == nil {
		return redisclient.TaskResult{WasRunSuccessful: true}
	}
	mu := c.lockMLflowExperiment(p.XID)
	mu.Lock()
	defer mu.Unlock()
	exec, childStatus, err := c.fetchTerminalMLflowExecution(ctx, p)
	if err != nil {
		return redisclient.TaskResult{ShouldBeRetried: status.Code(err) != codes.NotFound, Error: err}
	}
	if exec == nil {
		return redisclient.TaskResult{WasRunSuccessful: true}
	}
	mlClient, skip, err := c.mlflowClientForSync(ctx, exec.GetMetadata().GetNamespace())
	if skip {
		return redisclient.TaskResult{WasRunSuccessful: true}
	}
	if err != nil {
		return redisclient.TaskResult{ShouldBeRetried: true, Error: err}
	}
	meta := exec.GetMetadata()
	if err := c.syncMLflowWithBackoff(ctx, mlClient, p.XID, meta.GetMlflowParentRunId(), meta.GetMlflowRunId(), childStatus, executionEndTimeMs(exec), opts); err != nil {
		return redisclient.TaskResult{ShouldBeRetried: !mlflow.IsPermanentError(err), Error: err}
	}
	return redisclient.TaskResult{WasRunSuccessful: true}
}

func (c *Client) fetchTerminalMLflowExecution(ctx context.Context, p workunit.MLflowSyncPayload) (*essmsgpb.WorkUnitExecution, string, error) {
	wu, err := c.storage.FetchWorkUnit(ctx, p.XID, p.WID)
	if err != nil {
		return nil, "", err
	}
	exec := wu.GetCurrentWorkUnitExecution()
	if exec == nil {
		return nil, "", nil
	}
	if _, _, curEID, err := validator.ValidateWorkUnitExecutionName(exec.GetName()); err != nil || curEID != p.EID {
		return nil, "", nil
	}
	childStatus, isTerminal := mlflow.MapWorkUnitTerminalState(exec.GetStatus().GetState())
	if !isTerminal || exec.GetMetadata().GetMlflowRunId() == "" {
		return nil, "", nil
	}
	return exec, childStatus, nil
}

func (c *Client) mlflowClientForSync(ctx context.Context, namespace string) (*mlflow.Client, bool, error) {
	cfg, enabled, err := mlflow.ResolveConfig(ctx, &clientComponentGetter{c: c}, c.secretReader, namespace)
	if !enabled {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	httpClient := c.mlflowHTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: mlflowLaunchTimeout}
	}
	mlClient, err := mlflow.NewClient(cfg, httpClient)
	return mlClient, false, err
}

func (c *Client) syncMLflowWithBackoff(ctx context.Context, mlClient *mlflow.Client, xid int64, parentRunID, childRunID, childStatus string, endTimeMs int64, opts MLflowSyncOptions) error {
	trials, backoff, maxBackoff := max(opts.InHandlerTrials, 3), max(opts.InitialBackoff, time.Second), max(opts.MaxBackoff, 16*time.Second)
	var lastErr error
	for attempt := 0; attempt < trials; attempt++ {
		if attempt > 0 {
			if opts.ExtendClaim != nil {
				_ = opts.ExtendClaim(ctx)
			}
			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			backoff = min(backoff*2, maxBackoff)
		}
		err := c.syncMLflowOnce(ctx, mlClient, xid, parentRunID, childRunID, childStatus, endTimeMs)
		if err == nil || mlflow.IsPermanentError(err) {
			return err
		}
		lastErr = err
	}
	return lastErr
}

func (c *Client) syncMLflowOnce(ctx context.Context, mlClient *mlflow.Client, xid int64, parentRunID, childRunID, childStatus string, endTimeMs int64) error {
	mlflowCtx, cancel := context.WithTimeout(ctx, mlflowLaunchTimeout)
	defer cancel()
	if err := mlClient.UpdateRunStatus(mlflowCtx, childRunID, childStatus, endTimeMs); err != nil {
		return err
	}
	if parentRunID == "" {
		return nil
	}
	wus, _, err := c.storage.ListWorkUnits(ctx, validator.ConstructExperimentName(xid), 10000, "", "", "", 0)
	if err != nil {
		return err
	}
	states, maxEndMs := make([]essmsgpb.WorkUnitStatus_WorkUnitState, 0, len(wus)), endTimeMs
	for _, wu := range wus {
		if cur := wu.GetCurrentWorkUnitExecution(); cur != nil {
			states = append(states, cur.GetStatus().GetState())
			maxEndMs = max(maxEndMs, executionEndTimeMs(cur))
		}
	}
	if parentStatus, allTerminal := mlflow.ComputeParentRunStatus(states); allTerminal {
		return mlClient.UpdateRunStatus(mlflowCtx, parentRunID, parentStatus, maxEndMs)
	}
	return nil
}

func executionEndTimeMs(exec *essmsgpb.WorkUnitExecution) int64 {
	for _, ts := range []*timestamppb.Timestamp{exec.GetCompleteTime(), exec.GetUpdateTime()} {
		if ts != nil && !ts.AsTime().IsZero() {
			return ts.AsTime().UnixMilli()
		}
	}
	return time.Now().UnixMilli()
}

// StartMLflowSyncConsumer starts the Redis-stream consumer for MLflow status sync and returns a close function.
func (c *Client) StartMLflowSyncConsumer(ctx context.Context, addr string) func() error {
	cc := redisclient.NewConsumer(
		redisclient.Config{Addr: addr, ContextTimeoutEnabled: true},
		constants.RedisMLflowSyncStream, constants.RedisMLflowSyncGroup, constants.RedisMLflowSyncDLQ,
		constants.RedisMaxRetries, constants.RedisProcessTimeout, constants.RedisAbandonedMessageTimeout, constants.RedisReadBatchSize,
	)
	cc.SetTaskHandler(func(taskCtx context.Context, _ string, data string) redisclient.TaskResult {
		return c.HandleMLflowSyncTask(taskCtx, data, MLflowSyncOptions{ExtendClaim: cc.ExtendClaim})
	})
	loopCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		cc.StartWorker(loopCtx, redisclient.HostConsumerName(constants.MLflowSyncWorkerID))
	}()
	go func() {
		defer wg.Done()
		cc.RunCleanup(loopCtx, redisclient.HostConsumerName(constants.MLflowSyncCleanupWorkerID))
	}()
	return func() error {
		cancel()
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(redisclient.SettleTimeout + 3*time.Second):
		}
		return cc.Close(context.Background())
	}
}
