// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package experiment

import (
	"context"
	"testing"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

func TestUpdateExperimentStatus_NilExperiment(t *testing.T) {
	ctx := context.Background()
	r := &Repository{}

	// UpdateExperimentStatus with nil experiment should fail during mutation generation.
	err := r.UpdateExperimentStatus(ctx, nil, nil)
	if err == nil {
		t.Errorf("UpdateExperimentStatus(nil) succeeded, want error")
	}
}

func TestUpdateExperimentStatus_InvalidExperimentName(t *testing.T) {
	ctx := context.Background()
	r := &Repository{}

	exp := &essmsgpb.Experiment{
		Name: "invalid-name",
	}

	err := r.UpdateExperimentStatus(ctx, nil, exp)
	if err == nil {
		t.Errorf("UpdateExperimentStatus(invalid name) succeeded, want error")
	}
}

func TestUpdateExperimentResources_NilExperiment(t *testing.T) {
	ctx := context.Background()
	r := &Repository{}

	// UpdateExperimentResources with nil experiment should fail during mutation generation.
	err := r.UpdateExperimentResources(ctx, nil, nil)
	if err == nil {
		t.Errorf("UpdateExperimentResources(nil) succeeded, want error")
	}
}

func TestUpdateExperimentResources_InvalidExperimentName(t *testing.T) {
	ctx := context.Background()
	r := &Repository{}

	exp := &essmsgpb.Experiment{
		Name: "invalid-name",
	}

	err := r.UpdateExperimentResources(ctx, nil, exp)
	if err == nil {
		t.Errorf("UpdateExperimentResources(invalid name) succeeded, want error")
	}
}
