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

package workunit

import (
	"context"
	"testing"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

func TestInsertWorkUnit_NilWorkUnit(t *testing.T) {
	ctx := context.Background()
	r := &Repository{}

	// InsertWorkUnit with nil work unit should fail during mutation generation.
	err := r.InsertWorkUnit(ctx, nil, nil)
	if err == nil {
		t.Errorf("InsertWorkUnit(nil) succeeded, want error")
	}
}

func TestInsertWorkUnit_InvalidWorkUnitName(t *testing.T) {
	ctx := context.Background()
	r := &Repository{}

	wu := &essmsgpb.WorkUnit{
		Name: "invalid-name",
	}

	err := r.InsertWorkUnit(ctx, nil, wu)
	if err == nil {
		t.Errorf("InsertWorkUnit(invalid name) succeeded, want error")
	}
}
