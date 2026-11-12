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

package experiment_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/experiment"
	"xmanager_cloud/internal/experiment/experimenttest"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/pkg/components/store"
)

const pending = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION

var studyRequirements = experiment.RequirementsFor(&essmsgpb.Experiment{ExperimentType: essmsgpb.Experiment_STUDY})

// hpoCatalog returns a ready hpo component without parameters that depends on ready vizier and
// clickhouse components.
func hpoCatalog() map[string]*store.ComponentRecord {
	return experimenttest.HPOCatalog(nil)
}

func pinnedNames(recs []*store.ComponentRecord) []string {
	var names []string
	for _, rec := range recs {
		names = append(names, rec.Name)
	}
	slices.Sort(names)
	return names
}

func isRequirementNotMet(err error) bool {
	return errors.Is(err, experiment.ErrComponentRequirementNotMet) && status.Code(err) == codes.FailedPrecondition
}

// resolveAndPin resolves reqs and pins the resolved records to xid, as the creation transaction does.
func resolveAndPin(ctx context.Context, repo storage.ComponentTxRepository, xid int64, reqs []experiment.Requirement) error {
	recs, err := experiment.ResolveComponentRequirements(ctx, repo, reqs)
	if err != nil {
		return err
	}
	return experiment.PinComponentRecords(ctx, repo, xid, recs)
}

func TestResolveAndPinComponentRecords(t *testing.T) {
	const xid = 7
	allPinned := []string{"clickhouse", "hpo", "vizier"}
	tests := []struct {
		name       string
		reqs       []experiment.Requirement // Defaults to studyRequirements.
		modify     func(catalog map[string]*store.ComponentRecord)
		wantPinned []string
	}{
		{name: "all ready pins hpo and its transitive dependencies", wantPinned: allPinned},
		{name: "dependency cycle pins each component once", modify: func(c map[string]*store.ComponentRecord) { c["vizier"].Spec.Dependencies = []string{"hpo"} }, wantPinned: allPinned},
		{name: "requirement that is also a dependency is pinned once", reqs: append(slices.Clone(studyRequirements), experiment.Requirement{Name: "vizier"}), wantPinned: allPinned},
		{name: "missing hpo", modify: func(c map[string]*store.ComponentRecord) { delete(c, "hpo") }},
		{name: "pending hpo", modify: func(c map[string]*store.ComponentRecord) { c["hpo"].State = pending }},
		{name: "hpo of the wrong type", modify: func(c map[string]*store.ComponentRecord) {
			c["hpo"].Spec.Type = componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER
		}},
		{name: "pending vizier while hpo is enabled", modify: func(c map[string]*store.ComponentRecord) { c["vizier"].State = pending }},
		{name: "missing clickhouse", modify: func(c map[string]*store.ComponentRecord) { delete(c, "clickhouse") }},
		{name: "hpo observation lags its generation", modify: func(c map[string]*store.ComponentRecord) { c["hpo"].Generation++ }},
		{name: "vizier never observed", modify: func(c map[string]*store.ComponentRecord) { c["vizier"].ObservedGeneration = nil }},
		{name: "version below the minimum", reqs: []experiment.Requirement{{Name: "hpo", MinVersion: "v2.0.0"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			catalog := hpoCatalog()
			if tt.modify != nil {
				tt.modify(catalog)
			}
			reqs := tt.reqs
			if reqs == nil {
				reqs = studyRequirements
			}
			repo := &storage.MockComponentRepository{Records: catalog}

			err := resolveAndPin(context.Background(), repo, xid, reqs)

			if tt.wantPinned == nil {
				if !isRequirementNotMet(err) {
					t.Errorf("resolveAndPin() error = %v, want %v", err, experiment.ErrComponentRequirementNotMet)
				}
				if len(repo.Snapshots) != 0 {
					t.Errorf("resolveAndPin() pinned %v, want nothing", repo.Snapshots)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveAndPin() unexpected error: %v", err)
			}
			if diff := cmp.Diff(tt.wantPinned, pinnedNames(repo.Snapshots[xid])); diff != "" {
				t.Errorf("resolveAndPin() pinned components mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestResolveAndPinComponentRecords_StorageErrorsAreNotRequirementErrors(t *testing.T) {
	tests := []struct {
		name string
		repo *storage.MockComponentRepository
	}{
		{name: "read", repo: &storage.MockComponentRepository{Records: hpoCatalog(), GetActiveComponentErr: grpcerr.ErrAborted}},
		{name: "insert", repo: &storage.MockComponentRepository{Records: hpoCatalog(), InsertSnapshotErr: grpcerr.ErrAborted}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := resolveAndPin(context.Background(), tt.repo, 7, studyRequirements)

			if !errors.Is(err, grpcerr.ErrAborted) || errors.Is(err, experiment.ErrComponentRequirementNotMet) {
				t.Errorf("resolveAndPin() error = %v, want %v and not %v", err, grpcerr.ErrAborted, experiment.ErrComponentRequirementNotMet)
			}
		})
	}
}

func TestResolveComponentRequirements_Empty(t *testing.T) {
	recs, err := experiment.ResolveComponentRequirements(context.Background(), nil, nil)
	if err != nil || recs != nil {
		t.Errorf("ResolveComponentRequirements(empty) = %v, %v, want nil, nil", recs, err)
	}
}

func TestValidateComponentRequirements_PinsNothing(t *testing.T) {
	repo := &storage.MockComponentRepository{Records: hpoCatalog()}

	if err := experiment.ValidateComponentRequirements(context.Background(), repo, studyRequirements); err != nil {
		t.Errorf("ValidateComponentRequirements(ready) unexpected error: %v", err)
	}
	if len(repo.Snapshots) != 0 {
		t.Errorf("ValidateComponentRequirements() pinned %v, want nothing", repo.Snapshots)
	}
	if err := experiment.ValidateComponentRequirements(context.Background(), nil, nil); err != nil {
		t.Errorf("ValidateComponentRequirements(empty) unexpected error: %v", err)
	}
}

func TestRequirementsFor(t *testing.T) {
	hpo := []experiment.Requirement{{Name: "hpo", Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT}}
	tests := []struct {
		expType essmsgpb.Experiment_ExperimentType
		want    []experiment.Requirement
	}{
		{expType: essmsgpb.Experiment_STUDY, want: hpo},
		{expType: essmsgpb.Experiment_STANDARD},
		{expType: essmsgpb.Experiment_EXPERIMENT_TYPE_UNSPECIFIED},
	}
	for _, tt := range tests {
		t.Run(tt.expType.String(), func(t *testing.T) {
			got := experiment.RequirementsFor(&essmsgpb.Experiment{ExperimentType: tt.expType})
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("RequirementsFor(%v) mismatch (-want +got):\n%s", tt.expType, diff)
			}
		})
	}
}
