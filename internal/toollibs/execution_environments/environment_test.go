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
package executionenvironments

import (
	"context"
	"testing"

	jobsetv1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	epb "xmanager_cloud/api/protos/experiment"
)

type mockEnv struct {
	name string
}

func (m *mockEnv) Name() string {
	return m.name
}
func (m *mockEnv) Description() string {
	return ""
}
func (m *mockEnv) IsCompatibleWithWorkUnit(workUnit *epb.WorkUnit) (bool, string) {
	return true, ""
}
func (m *mockEnv) InjectOnJobSetCreation(ctx context.Context, workUnit *epb.WorkUnit, jobSet *jobsetv1alpha2.JobSet) (*jobsetv1alpha2.JobSet, error) {
	return jobSet, nil
}
func (m *mockEnv) InjectOnExperimentCreation(ctx context.Context, essServer epb.ExperimentStateServiceServer, experiment *epb.Experiment) (*epb.Experiment, error) {
	return experiment, nil
}
func (m *mockEnv) InjectOnWorkUnitCreation(ctx context.Context, essServer epb.ExperimentStateServiceServer, workUnit *epb.WorkUnit) (*epb.WorkUnit, error) {
	return workUnit, nil
}

func setup(t *testing.T) {
	t.Helper()
	old := executionEnvironments
	executionEnvironments = make(map[string]Environment)
	t.Cleanup(func() {
		executionEnvironments = old
	})
}

func TestRegister(t *testing.T) {
	setup(t)
	env := &mockEnv{name: "test"}
	Register(env)
	if len(executionEnvironments) != 1 {
		t.Errorf("Register() failed, expected 1 environment, got %d", len(executionEnvironments))
	}
	if _, ok := executionEnvironments["test"]; !ok {
		t.Errorf("Register() failed, environment 'test' not registered")
	}
}

func TestRegisterPanic(t *testing.T) {
	setup(t)
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("Register() should have panicked when registering duplicate environment")
		}
	}()
	env1 := &mockEnv{name: "test"}
	env2 := &mockEnv{name: "test"}
	Register(env1)
	Register(env2)
}

func TestLookup(t *testing.T) {
	setup(t)
	env := &mockEnv{name: "test"}
	Register(env)
	gotEnv, ok := Lookup("test")
	if !ok {
		t.Errorf("Lookup('test') failed, expected environment, got nil")
	}
	if gotEnv.Name() != "test" {
		t.Errorf("Lookup('test') failed, expected environment with name 'test', got %q", gotEnv.Name())
	}
	_, ok = Lookup("not-test")
	if ok {
		t.Errorf("Lookup('not-test') failed, expected no environment, got one")
	}
}

func TestAll(t *testing.T) {
	setup(t)
	env1 := &mockEnv{name: "test1"}
	env2 := &mockEnv{name: "test2"}
	Register(env1)
	Register(env2)
	allEnvs := All()
	if len(allEnvs) != 2 {
		t.Errorf("All() failed, expected 2 environments, got %d", len(allEnvs))
	}
	if _, ok := allEnvs["test1"]; !ok {
		t.Errorf("All() failed, environment 'test1' not returned")
	}
	if _, ok := allEnvs["test2"]; !ok {
		t.Errorf("All() failed, environment 'test2' not returned")
	}
}
