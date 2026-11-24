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

	"xmanager_cloud/internal/storage"
	"xmanager_cloud/pkg/components/store"
)

// SeedComponentCatalog inserts and marks observed each record in recs (ordered before dependents).
func SeedComponentCatalog(ctx context.Context, st storage.TransactionRunner, recs ...*store.ComponentRecord) error {
	_, err := st.RunInSerializableTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		depIDs := make(map[string]int64, len(recs))
		for _, rec := range recs {
			if err := tx.Components.InsertComponent(ctx, rec, depIDs); err != nil {
				return err
			}
			if err := tx.Components.UpdateComponentObservation(ctx, rec, false); err != nil {
				return err
			}
			depIDs[rec.Name] = rec.ComponentID
		}
		return nil
	})
	return err
}
