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

package storagetest

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/pkg/components/store"
)

// recordingComponentTxRepo logs every call in order and, like both backends, rejects a record
// whose dependencies are missing from depIDs with FailedPrecondition.
type recordingComponentTxRepo struct {
	storage.ComponentTxRepository
	insertErr, observeErr error
	calls                 []string
}

func (r *recordingComponentTxRepo) InsertComponent(_ context.Context, rec *store.ComponentRecord, depIDs map[string]int64) error {
	r.calls = append(r.calls, fmt.Sprintf("insert:%s deps=%v", rec.Name, depIDs))
	for _, dep := range rec.Dependencies {
		if _, ok := depIDs[dep]; !ok {
			return fmt.Errorf("missing component ID for dependency %q: %w", dep, grpcerr.ErrFailedPrecondition)
		}
	}
	return r.insertErr
}

func (r *recordingComponentTxRepo) UpdateComponentObservation(_ context.Context, rec *store.ComponentRecord, _ bool) error {
	r.calls = append(r.calls, "observe:"+rec.Name)
	return r.observeErr
}

func TestSeedComponentCatalog(t *testing.T) {
	clickhouse := &store.ComponentRecord{ComponentID: 1, Name: "clickhouse"}
	hpo := &store.ComponentRecord{ComponentID: 2, Name: "hpo", Dependencies: []string{"clickhouse"}}
	boom := errors.New("boom")
	for _, tc := range []struct {
		name      string
		repo      *recordingComponentTxRepo
		attempts  int
		recs      []*store.ComponentRecord
		wantCalls []string
		wantErr   error
	}{
		{name: "inserts_then_observes_each_record_and_resets_on_retry", repo: &recordingComponentTxRepo{}, attempts: 2, recs: []*store.ComponentRecord{clickhouse, hpo},
			wantCalls: []string{"insert:clickhouse deps=map[]", "observe:clickhouse", "insert:hpo deps=map[clickhouse:1]", "observe:hpo",
				"insert:clickhouse deps=map[]", "observe:clickhouse", "insert:hpo deps=map[clickhouse:1]", "observe:hpo"}},
		{name: "dependent_before_dependency_fails", repo: &recordingComponentTxRepo{}, recs: []*store.ComponentRecord{hpo, clickhouse},
			wantCalls: []string{"insert:hpo deps=map[]"}, wantErr: grpcerr.ErrFailedPrecondition},
		{name: "insert_error_is_returned", repo: &recordingComponentTxRepo{insertErr: boom}, recs: []*store.ComponentRecord{clickhouse},
			wantCalls: []string{"insert:clickhouse deps=map[]"}, wantErr: boom},
		{name: "observe_error_is_returned", repo: &recordingComponentTxRepo{observeErr: boom}, recs: []*store.ComponentRecord{clickhouse},
			wantCalls: []string{"insert:clickhouse deps=map[]", "observe:clickhouse"}, wantErr: boom},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &storage.MockStorage{Transaction: &storage.Transaction{Components: tc.repo}, RunInTransactionAttempts: tc.attempts}
			if err := SeedComponentCatalog(context.Background(), st, tc.recs...); !errors.Is(err, tc.wantErr) {
				t.Errorf("SeedComponentCatalog() error = %v, want %v", err, tc.wantErr)
			}
			if diff := cmp.Diff(tc.wantCalls, tc.repo.calls); diff != "" {
				t.Errorf("repository calls mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
