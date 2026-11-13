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

package workunit_test

import (
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/experiment/experimenttest"
	"xmanager_cloud/internal/storage"
)

const (
	// batchXID is the STUDY experiment that the batch creation tests create trials in.
	batchXID = 42
	// studyControllerGSA is the study controller service account configured on the hpo component.
	studyControllerGSA = "xmc-study-controller@my-project.iam.gserviceaccount.com"
)

// hpoCatalog returns a component repository whose hpo component has the given
// studyControllerServiceAccount parameter.
func hpoCatalog(serviceAccount string) *storage.MockComponentRepository {
	return &storage.MockComponentRepository{Records: experimenttest.HPOCatalog(map[string]string{"studyControllerServiceAccount": serviceAccount})}
}

// newTrialRequest returns a study trial work unit of batchXID as the ESS handler assembles it
// before creation, with trialID as its notes.
func newTrialRequest(trialID string) *essmsgpb.WorkUnit {
	return &essmsgpb.WorkUnit{
		Name:         "experiments/42/workUnits/0",
		WorkUnitType: essmsgpb.WorkUnit_STANDARD,
		Priority:     essmsgpb.Priority_LOW,
		Urls:         []*essmsgpb.Url{},
		Tags:         []string{},
		Executable: &essmsgpb.Executable{
			SchedulingConstraints: &essmsgpb.SchedulingConstraints{QueueName: "queue"},
			Type: &essmsgpb.Executable_Jobset{Jobset: &essmsgpb.JobSetExecutable{Jobs: []*essmsgpb.KubernetesJob{{
				Name: "trainer", Replicas: 1,
				Spec: &essmsgpb.ExecutableSpec{Resources: &essmsgpb.ResourceSet{Resources: &essmsgpb.ResourceSet_CpuResources{CpuResources: &essmsgpb.CpuResources{CpuMillicores: 1000, MemoryBytes: 1 << 30}}}},
			}}}},
		},
		CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
			Name:     "experiments/42/workUnits/0/executions/1",
			State:    essmsgpb.WorkUnitExecution_ACTIVE,
			Metadata: &essmsgpb.WorkUnitExecutionMetadata{},
			Status: &essmsgpb.WorkUnitStatus{
				State:         essmsgpb.WorkUnitStatus_INITIALIZING,
				SystemMessage: &essmsgpb.StructuredMessage{Severity: essmsgpb.StructuredMessage_DEBUG, UserFacingText: "initializing", Parent: "experiments/42/workUnits/0"},
			},
		},
		Notes: trialID,
	}
}
