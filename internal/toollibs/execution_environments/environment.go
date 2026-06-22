// Copyright 2025 Google LLC
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
// Package executionenvironments provides an interface for execution environments and utilities to
// work with them.
package executionenvironments

import (
	"context"
	"fmt"

	jobsetv1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	spb "xmanager_cloud/experiment_state_server/proto/api_go_grpc_proto"
	epb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

const (
	// Priority used for dashboard URLs. 0 is the default priority, so this places the dashboard
	// URLs after default user-specified URLs but before debug URLs.
	dashboardPriority = 10
	// Priority used for debug URLs. This places the debug URLs at the end of the list of URLs.
	debugPriority = 20
)

// Environment is an interface for execution environments. An execution environment defines
// the physical environment in which an executable is run, such as a Cloud Service (GCP/GKE) or a
// local cluster.
type Environment interface {
	// Name returns the name of the execution environment.
	Name() string
	// Description returns a description of the execution environment.
	Description() string
	// IsCompatibleWithWorkUnit returns whether the execution environment is compatible with the given
	// work unit. It returns a reason if the environment is not compatible.
	IsCompatibleWithWorkUnit(workUnit *epb.WorkUnit) (bool, string)
	// InjectOnJobSetCreation injects the environment on job set creation based on the work unit.
	InjectOnJobSetCreation(ctx context.Context, workUnit *epb.WorkUnit, jobset *jobsetv1alpha2.JobSet) (*jobsetv1alpha2.JobSet, error)
	// InjectOnExperimentCreation injects the environment on experiment creation.
	InjectOnExperimentCreation(ctx context.Context, essServer spb.ExperimentStateServiceServer, experiment *epb.Experiment) (*epb.Experiment, error)
	// InjectOnWorkUnitCreation injects the environment on work unit creation.
	InjectOnWorkUnitCreation(ctx context.Context, essServer spb.ExperimentStateServiceServer, workUnit *epb.WorkUnit) (*epb.WorkUnit, error)
}

var executionEnvironments = make(map[string]Environment)

// Register registers an execution environment.
func Register(env Environment) {
	if _, ok := executionEnvironments[env.Name()]; ok {
		panic(fmt.Sprintf("execution environment %q already registered", env.Name()))
	}
	executionEnvironments[env.Name()] = env
}

// Lookup returns the execution environment with the given name.
func Lookup(name string) (Environment, bool) {
	env, ok := executionEnvironments[name]
	return env, ok
}

// All returns all registered execution environments.
func All() map[string]Environment {
	copy := make(map[string]Environment)
	for k, v := range executionEnvironments {
		copy[k] = v
	}
	return copy
}
