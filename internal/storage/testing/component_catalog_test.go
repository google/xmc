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
	"maps"
	"testing"

	"github.com/google/go-cmp/cmp"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/pkg/components/store"
)

type recordingComponentTxRepo struct {
	storage.ComponentTxRepository
	insertErr, observeErr error
	insertedDeps          []map[string]int64
	observed              []string
}

func (r *recordingComponentTxRepo) InsertComponent(_ context.Context, _ *store.ComponentRecord, depIDs map[string]int64) error {
	r.insertedDeps = append(r.insertedDeps, maps.Clone(depIDs))
	return r.insertErr
}
func (r *recordingComponentTxRepo) UpdateComponentObservation(_ context.Context, rec *store.ComponentRecord, _ bool) error {
	r.observed = append(r.observed, rec.Name)
	return r.observeErr
}

func TestSeedComponentCatalog(t *testing.T) {
	recs := []*store.ComponentRecord{{ComponentID: 1, Name: "clickhouse"}, {ComponentID: 2, Name: "hpo", Dependencies: []string{"clickhouse"}}}
	repo := &recordingComponentTxRepo{}
	if err := SeedComponentCatalog(context.Background(), &storage.MockStorage{Transaction: &storage.Transaction{Components: repo}, RunInTransactionAttempts: 2}, recs...); err != nil {
		t.Fatalf("SeedComponentCatalog() error = %v", err)
	}
	if diff := cmp.Diff([]map[string]int64{{}, {"clickhouse": 1}, {}, {"clickhouse": 1}}, repo.insertedDeps); diff != "" {
		t.Errorf("insertedDeps across retry attempts mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"clickhouse", "hpo", "clickhouse", "hpo"}, repo.observed); diff != "" {
		t.Errorf("observed mismatch (-want +got):\n%s", diff)
	}
	boom := errors.New("boom")
	for _, errRepo := range []*recordingComponentTxRepo{{insertErr: boom}, {observeErr: boom}} {
		if err := SeedComponentCatalog(context.Background(), &storage.MockStorage{Transaction: &storage.Transaction{Components: errRepo}}, recs...); !errors.Is(err, boom) {
			t.Errorf("SeedComponentCatalog(%+v) = %v, want %v", errRepo, err, boom)
		}
	}
}
