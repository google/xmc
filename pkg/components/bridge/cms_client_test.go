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

package bridge

import (
	"context"
	"testing"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

type dummyServer struct {
	componentspb.UnimplementedComponentManagerServiceServer
}

func (d *dummyServer) SetComponentIntent(context.Context, *componentspb.SetComponentIntentRequest) (*componentspb.SetComponentIntentResponse, error) {
	return &componentspb.SetComponentIntentResponse{}, nil
}

func (d *dummyServer) UpdateObservedGeneration(context.Context, *componentspb.UpdateObservedGenerationRequest) (*componentspb.UpdateObservedGenerationResponse, error) {
	return &componentspb.UpdateObservedGenerationResponse{}, nil
}

func (d *dummyServer) ExecuteDeletion(context.Context, *componentspb.ExecuteDeletionRequest) (*componentspb.ExecuteDeletionResponse, error) {
	return &componentspb.ExecuteDeletionResponse{}, nil
}

func TestCMSClientAdapter(t *testing.T) {
	a := NewCMSClientAdapter(&dummyServer{})
	ctx := context.Background()
	if _, err := a.SetComponentIntent(ctx, nil); err != nil {
		t.Error(err)
	}
	if _, err := a.UpdateObservedGeneration(ctx, nil); err != nil {
		t.Error(err)
	}
	if _, err := a.ExecuteDeletion(ctx, nil); err != nil {
		t.Error(err)
	}
}
