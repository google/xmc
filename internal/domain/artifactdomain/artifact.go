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

// Package artifactdomain contains pure domain logic for artifacts.
package artifactdomain

import (
	"context"
	"fmt"
	"time"

	timestamppb "protobuf/timestamp_go_proto"
	"task/go/status"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/validator/validator"
)

// PrepareArtifactCreation validates an artifact proto and assigns initial creation timestamps.
func PrepareArtifactCreation(ctx context.Context, artifact *essmsgpb.Artifact, now time.Time) error {
	if artifact == nil {
		return fmt.Errorf("artifact must not be nil: %w", status.ErrInvalidArgument)
	}

	_, err := validator.ValidateArtifactName(artifact.GetName())
	if err != nil {
		return fmt.Errorf("failed to validate artifact name: %w", err)
	}

	nowProto := timestamppb.New(now)
	artifact.CreateTime = nowProto
	artifact.UpdateTime = nowProto

	return nil
}
