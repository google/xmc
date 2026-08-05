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

package workunitdomain

import (
	"context"
	"testing"
	"time"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

func TestPrepareWorkUnit(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		wu       *essmsgpb.WorkUnit
		xid      int64
		wid      int64
		eid      int64
		wantName string
		wantErr  bool
	}{
		{
			name:    "nil_workunit",
			wu:      nil,
			wantErr: true,
		},
		{
			name: "assigns_names_and_timestamps",
			wu: &essmsgpb.WorkUnit{
				CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
					Status: &essmsgpb.WorkUnitStatus{
						State: essmsgpb.WorkUnitStatus_RUNNING,
					},
				},
			},
			xid:      100,
			wid:      5,
			eid:      1,
			wantName: "experiments/100/workUnits/5",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := PrepareWorkUnit(ctx, tt.wu, tt.xid, tt.wid, tt.eid, now)
			if (err != nil) != tt.wantErr {
				t.Fatalf("PrepareWorkUnit() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if tt.wu.GetName() != tt.wantName {
				t.Errorf("PrepareWorkUnit() Name = %s, want %s", tt.wu.GetName(), tt.wantName)
			}
			if tt.wu.GetCreateTime() == nil || tt.wu.GetUpdateTime() == nil {
				t.Errorf("PrepareWorkUnit() did not set timestamps on wu: %v", tt.wu)
			}
			if tt.wu.GetCurrentWorkUnitExecution().GetCreateTime() == nil {
				t.Errorf("PrepareWorkUnit() did not set execution timestamps: %v", tt.wu)
			}
		})
	}
}
