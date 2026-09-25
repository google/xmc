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
	r := &TxRepository{}

	err := r.UpdateExperimentStatus(ctx, nil)
	if err == nil || err.Error() != "experiment must not be nil" {
		t.Errorf("UpdateExperimentStatus(nil exp) returned error: %v, want 'experiment must not be nil'", err)
	}
}

func TestUpdateExperimentResources_NilExperiment(t *testing.T) {
	ctx := context.Background()
	r := &Repository{}

	err := r.UpdateExperimentResources(ctx, nil, nil)
	if err == nil || err.Error() != "experiment must not be nil" {
		t.Errorf("UpdateExperimentResources(nil exp) returned error: %v, want 'experiment must not be nil'", err)
	}
}

func TestUpdateExperimentResources_NilTransaction(t *testing.T) {
	ctx := context.Background()
	r := &Repository{}

	exp := &essmsgpb.Experiment{
		Name: "experiments/1001",
	}

	err := r.UpdateExperimentResources(ctx, nil, exp)
	if err == nil || err.Error() != "transaction must not be nil" {
		t.Errorf("UpdateExperimentResources(nil tx) returned error: %v, want 'transaction must not be nil'", err)
	}
}
