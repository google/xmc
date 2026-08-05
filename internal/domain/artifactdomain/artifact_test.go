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

package artifactdomain

import (
	"context"
	"testing"
	"time"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

func TestPrepareArtifactCreation(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		artifact *essmsgpb.Artifact
		wantErr  bool
	}{
		{
			name:     "nil_artifact",
			artifact: nil,
			wantErr:  true,
		},
		{
			name: "invalid_name",
			artifact: &essmsgpb.Artifact{
				Name: "invalid-name",
			},
			wantErr: true,
		},
		{
			name: "valid_artifact",
			artifact: &essmsgpb.Artifact{
				Name: "artifacts/456",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := PrepareArtifactCreation(ctx, tt.artifact, now)
			if (err != nil) != tt.wantErr {
				t.Fatalf("PrepareArtifactCreation() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if tt.artifact.GetCreateTime() == nil || tt.artifact.GetUpdateTime() == nil {
				t.Errorf("PrepareArtifactCreation() did not set timestamps on artifact: %v", tt.artifact)
			}
		})
	}
}
