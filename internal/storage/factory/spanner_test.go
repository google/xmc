// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dbfactory

import (
	"context"
	"testing"

	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/dbconfig"
	"xmanager_cloud/internal/storage/spanner/emulator"
)

func TestOpen_Spanner(t *testing.T) {
	ctx := context.Background()
	clients := emulator.SetupWithEmulator(ctx, t)
	cfg := dbconfig.Config{
		Backend: dbconfig.BackendSpanner,
		Spanner: dbconfig.Spanner{
			EssDatabase: clients.ESSClient.DatabaseName(),
			XidDatabase: clients.XIDClient.DatabaseName(),
		},
	}

	for _, db := range []Database{ESS, XID} {
		t.Run(string(db), func(t *testing.T) {
			st, err := NewStorage(ctx, cfg, db)
			if err != nil {
				t.Fatalf("NewStorage(%q) failed: %v", db, err)
			}
			defer st.Close()

			wantDatabase, _ := spannerDatabase(cfg.Spanner, db)
			if got := st.SpannerClient.DatabaseName(); got != wantDatabase {
				t.Errorf("SpannerClient.DatabaseName() = %q, want %q", got, wantDatabase)
			}
			noop := func(context.Context, *storage.Transaction) error { return nil }
			if err := st.RunInTransaction(ctx, noop); err != nil {
				t.Errorf("RunInTransaction() failed: %v", err)
			}
		})
	}
}
