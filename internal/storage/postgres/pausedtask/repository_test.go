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
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5/pgxpool"
	actuatortaskpb "xmanager_cloud/api/protos/task_queue"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/postgres/pgdb"
)

func TestNewRepository_BindsPoolAndTx(t *testing.T) {
	pool := &pgxpool.Pool{}
	repo := NewRepository(pool)
	if repo.db != pool {
		t.Errorf("NewRepository(pool).db = %v, want %p", repo.db, pool)
	}

	// Any DBTX implementation can stand in for the transaction handle.
	var tx pgdb.DBTX = &pgxpool.Pool{}
	txRepo := repo.WithTx(tx)
	if txRepo.tx != tx {
		t.Errorf("WithTx(tx).tx = %v, want %v", txRepo.tx, tx)
	}
}

func TestValidateTask(t *testing.T) {
	valid := func() *storage.PausedTask {
		return &storage.PausedTask{
			XID:        1,
			WID:        2,
			TaskID:     "task-1",
			CreateTime: time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC),
			Action:     &actuatortaskpb.Action{},
		}
	}
	noAction := valid()
	noAction.Action = nil
	noID := valid()
	noID.TaskID = ""
	noCreateTime := valid()
	noCreateTime.CreateTime = time.Time{}

	tests := []struct {
		name    string
		task    *storage.PausedTask
		wantErr error
	}{
		{name: "valid", task: valid()},
		{name: "nil_task", task: nil, wantErr: grpcerr.ErrInvalidArgument},
		{name: "nil_action", task: noAction, wantErr: grpcerr.ErrInvalidArgument},
		{name: "empty_task_id", task: noID, wantErr: grpcerr.ErrInvalidArgument},
		{name: "zero_create_time", task: noCreateTime, wantErr: grpcerr.ErrInvalidArgument},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateTask(tt.task); !errors.Is(err, tt.wantErr) {
				t.Errorf("validateTask() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestCompareResolutionKeys_OrdersByPrimaryKey(t *testing.T) {
	res := func(xid, wid int64, taskID string) storage.PausedTaskResolution {
		return storage.PausedTaskResolution{XID: xid, WID: wid, TaskID: taskID}
	}
	tests := []struct {
		name string
		a, b storage.PausedTaskResolution
		want int
	}{
		{name: "equal", a: res(1, 1, "a"), b: res(1, 1, "a"), want: 0},
		{name: "xid_first", a: res(1, 9, "z"), b: res(2, 1, "a"), want: -1},
		{name: "wid_second", a: res(1, 2, "a"), b: res(1, 1, "z"), want: 1},
		{name: "task_id_last", a: res(1, 1, "a"), b: res(1, 1, "b"), want: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := compareResolutionKeys(tt.a, tt.b); got != tt.want {
				t.Errorf("compareResolutionKeys() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestMarshalProto_EmptyMessageIsNotNil(t *testing.T) {
	got, err := marshalProto(&actuatortaskpb.Action{})
	if err != nil {
		t.Fatalf("marshalProto() unexpected error: %v", err)
	}
	if got == nil {
		t.Error("marshalProto(empty) = nil, want a non-nil zero-length slice so pgx does not send SQL NULL")
	}
	if diff := cmp.Diff([]byte{}, got); diff != "" {
		t.Errorf("marshalProto(empty) mismatch (-want +got):\n%s", diff)
	}
}
