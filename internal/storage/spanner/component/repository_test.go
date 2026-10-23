// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package component

import (
	"context"
	"errors"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/internal/storage/spanner/emulator"
	"xmanager_cloud/pkg/components/naming"
	"xmanager_cloud/pkg/components/store"
)

var baseTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func seedCatalog(t *testing.T, client *spanner.Client) (*store.ComponentRecord, *store.ComponentRecord) {
	t.Helper()
	app := buildRecord(1, "app", componentspb.ComponentState_COMPONENT_STATE_ENABLED, "db")
	db := buildRecord(2, "db", componentspb.ComponentState_COMPONENT_STATE_ENABLED)
	deleted := buildRecord(3, "cache", componentspb.ComponentState_COMPONENT_STATE_DELETED)

	mutations := []*spanner.Mutation{
		insertComponentMutation(t, app),
		insertComponentMutation(t, db),
		insertComponentMutation(t, deleted),
		spanner.Insert("ComponentDependencies", []string{"ComponentId", "DependencyComponentId"}, []any{int64(1), int64(2)}),
		spanner.Insert("ComponentDependencies", []string{"ComponentId", "DependencyComponentId"}, []any{int64(1), int64(3)}),
	}
	if _, err := client.Apply(context.Background(), mutations); err != nil {
		t.Fatalf("Apply() seed failed: %v", err)
	}
	return app, db
}

func buildRecord(id int64, name string, state componentspb.ComponentState, deps ...string) *store.ComponentRecord {
	rec := &store.ComponentRecord{
		ComponentID:  id,
		Name:         name,
		Spec:         &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, Version: "v1.2.0", Generation: 1},
		Status:       &componentspb.ComponentAccessStatus{State: state},
		State:        state,
		Generation:   1,
		Dependencies: deps,
		CreateTime:   baseTime,
		UpdateTime:   baseTime,
		Labels:       map[string]string{"env": "prod"},
	}
	rec.Etag = store.GenerateEtag(rec)
	return rec
}

func insertComponentMutation(t *testing.T, rec *store.ComponentRecord) *spanner.Mutation {
	t.Helper()
	cfgBytes, err := proto.Marshal(&componentspb.Component{
		Name:   naming.FullComponentName(rec.Name),
		Spec:   rec.Spec,
		Labels: rec.Labels,
	})
	if err != nil {
		t.Fatalf("proto.Marshal(Component) failed: %v", err)
	}
	statusBytes, err := proto.Marshal(rec.Status)
	if err != nil {
		t.Fatalf("proto.Marshal(ComponentAccessStatus) failed: %v", err)
	}
	return spanner.Insert(
		"Components",
		[]string{"ComponentId", "Name", "ConfigProto", "AccessPayload", "State", "Generation", "CreateTime", "UpdateTime"},
		[]any{rec.ComponentID, rec.Name, cfgBytes, statusBytes, int64(rec.State), rec.Generation, rec.CreateTime, rec.UpdateTime},
	)
}

func TestRepository_ReadsAndWithTx(t *testing.T) {
	ctx := context.Background()
	_, client := emulator.Setup(ctx, t)
	repo := NewRepository(client)
	wantApp, _ := seedCatalog(t, client)

	t.Run("GetActiveComponent", func(t *testing.T) {
		got, err := repo.GetActiveComponent(ctx, "app")
		if err != nil {
			t.Fatalf("GetActiveComponent(app) failed: %v", err)
		}
		if diff := cmp.Diff(wantApp, got, protocmp.Transform()); diff != "" {
			t.Errorf("GetActiveComponent(app) mismatch (-want +got):\n%s", diff)
		}
		for _, missing := range []string{"missing", "cache"} {
			if _, err := repo.GetActiveComponent(ctx, missing); !errors.Is(err, store.ErrComponentNotFound) {
				t.Errorf("GetActiveComponent(%q) error = %v, want %v", missing, err, store.ErrComponentNotFound)
			}
		}
	})

	t.Run("GetComponentByID", func(t *testing.T) {
		got, err := repo.GetComponentByID(ctx, 1)
		if err != nil {
			t.Fatalf("GetComponentByID(1) failed: %v", err)
		}
		if diff := cmp.Diff(wantApp, got, protocmp.Transform()); diff != "" {
			t.Errorf("GetComponentByID(1) mismatch (-want +got):\n%s", diff)
		}
		for _, id := range []int64{3, 99} {
			if _, err := repo.GetComponentByID(ctx, id); !errors.Is(err, store.ErrComponentNotFound) {
				t.Errorf("GetComponentByID(%d) error = %v, want %v", id, err, store.ErrComponentNotFound)
			}
		}
	})

	t.Run("WithTx", func(t *testing.T) {
		_, err := client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
			got, err := repo.WithTx(tx).GetActiveComponent(ctx, "app")
			if err != nil {
				return err
			}
			if diff := cmp.Diff(wantApp, got, protocmp.Transform()); diff != "" {
				t.Errorf("WithTx().GetActiveComponent(app) mismatch (-want +got):\n%s", diff)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("ReadWriteTransaction() failed: %v", err)
		}
	})
}
