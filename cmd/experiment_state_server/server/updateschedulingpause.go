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

	"google.golang.org/protobuf/types/known/emptypb"
	"task/go/status"
	apipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	"xmanager_cloud/logging/logging"
)

// UpdateSchedulingPause toggles global workload scheduling.
func (s *Server) UpdateSchedulingPause(ctx context.Context, req *apipb.UpdateSchedulingPauseRequest) (*emptypb.Empty, error) {
	logging.Info(ctx, "Received UpdateSchedulingPause request", "paused", req.GetPaused())

	if err := s.storage.UpdateSchedulingPause(ctx, req.GetPaused()); err != nil {
		logging.Error(ctx, "Failed to update scheduling pause state in Spanner", "error", err)
		return nil, status.ErrInternal
	}

	logging.Info(ctx, "Successfully updated scheduling pause state", "paused", req.GetPaused())
	return &emptypb.Empty{}, nil
}
