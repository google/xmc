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

package experimenttest

import (
	"testing"

	essmsgpb "xmanager_cloud/api/protos/experiment"
	xidmessagespb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/internal/validator"
)

func TestStudyConfig_IsValid(t *testing.T) {
	req := &essmsgpb.CreateExperimentRequest{Experiment: &essmsgpb.Experiment{
		Title:          "study",
		Acls:           &xidmessagespb.XidAcls{},
		ExperimentType: essmsgpb.Experiment_STUDY,
		StudyConfig:    StudyConfig(),
	}}
	if err := validator.ValidateCreateExperimentRequest(req); err != nil {
		t.Errorf("ValidateCreateExperimentRequest(StudyConfig()) = %v, want nil", err)
	}
}
