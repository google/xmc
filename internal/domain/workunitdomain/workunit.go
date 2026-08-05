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

// Package workunitdomain contains pure domain logic for work units.
package workunitdomain

import (
	"context"
	"fmt"
	"time"

	timestamppb "protobuf/timestamp_go_proto"
	"task/go/status"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/validator/validator"
)

// PrepareWorkUnit initializes a work unit with names and timestamps before persistence.
func PrepareWorkUnit(ctx context.Context, wu *essmsgpb.WorkUnit, xid, wid, eid int64, now time.Time) error {
	if wu == nil {
		return fmt.Errorf("work unit must not be nil: %w", status.ErrInvalidArgument)
	}

	if wid > 0 {
		wu.Name = validator.ConstructWorkUnitName(xid, wid)
		if wu.CurrentWorkUnitExecution != nil {
			wu.CurrentWorkUnitExecution.Name = validator.ConstructWorkUnitExecutionName(xid, wid, eid)
			if wu.CurrentWorkUnitExecution.Status != nil && wu.CurrentWorkUnitExecution.Status.SystemMessage != nil {
				wu.CurrentWorkUnitExecution.Status.SystemMessage.Parent = wu.GetName()
			}
		}
	}

	nowProto := timestamppb.New(now)
	wu.CreateTime = nowProto
	wu.UpdateTime = nowProto
	if wu.CurrentWorkUnitExecution != nil {
		wu.CurrentWorkUnitExecution.CreateTime = nowProto
		wu.CurrentWorkUnitExecution.UpdateTime = nowProto
		if wu.CurrentWorkUnitExecution.Status != nil {
			wu.CurrentWorkUnitExecution.Status.UpdateTime = nowProto
			wu.CurrentWorkUnitExecution.Status.StateUpdateTime = nowProto
		}
	}

	return nil
}
