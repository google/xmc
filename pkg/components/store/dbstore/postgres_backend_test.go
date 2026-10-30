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

package dbstore_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"

	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/postgres"
	postgrestest "xmanager_cloud/internal/storage/postgres/testing"
	"xmanager_cloud/pkg/components/store"
)

// postgresBackend runs the dbstore tests against a fresh PostgreSQL test database.
func postgresBackend() dbBackend {
	return dbBackend{name: "postgres", open: openPostgresStorage}
}

// openPostgresStorage returns a storage.Storage on a fresh PostgreSQL test database and the hooks
// that reach its rows.
func openPostgresStorage(ctx context.Context, t *testing.T) (storage.Storage, backendRows) {
	t.Helper()
	_, pool := postgrestest.Setup(ctx, t)
	st, err := postgres.NewStorage(pool)
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}
	return st, postgresRows{pool: pool}
}

// postgresRows implements backendRows with SQL on a PostgreSQL database.
type postgresRows struct {
	pool *pgxpool.Pool
}

// OwnerByID implements backendRows.
func (r postgresRows) OwnerByID(ctx context.Context, componentID int64) (*store.SourceOwner, error) {
	var uid, namespace, cluster pgtype.Text
	var generation, epoch pgtype.Int8
	err := r.pool.QueryRow(ctx, `SELECT SourceUid, SourceGeneration, SourceNamespace, SourceClusterUid, SourceEpoch FROM Components WHERE ComponentId = $1`, componentID).
		Scan(&uid, &generation, &namespace, &cluster, &epoch)
	if err != nil {
		return nil, err
	}
	if !cluster.Valid {
		return nil, fmt.Errorf("component %d has no recorded owner", componentID)
	}
	return &store.SourceOwner{UID: uid.String, Generation: generation.Int64, Namespace: namespace.String, ClusterUID: cluster.String, Epoch: epoch.Int64}, nil
}

// SetSourceLastSeenTime implements backendRows.
func (r postgresRows) SetSourceLastSeenTime(ctx context.Context, canonicalName string, seenAt time.Time) error {
	_, err := r.pool.Exec(ctx, `UPDATE Components SET SourceLastSeenTime = $1 WHERE ActiveName = $2`, seenAt, canonicalName)
	return err
}

// SourceTimes implements backendRows.
func (r postgresRows) SourceTimes(ctx context.Context, canonicalName string) (lastSeen, update time.Time, err error) {
	var nullLastSeen pgtype.Timestamptz
	err = r.pool.QueryRow(ctx, `SELECT SourceLastSeenTime, UpdateTime FROM Components WHERE ActiveName = $1`, canonicalName).Scan(&nullLastSeen, &update)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if nullLastSeen.Valid {
		lastSeen = nullLastSeen.Time.UTC()
	}
	return lastSeen, update.UTC(), nil
}

// ComponentRowTimestamps implements backendRows.
func (r postgresRows) ComponentRowTimestamps(ctx context.Context, componentID int64) (rowTimestamps, error) {
	var createTime, updateTime time.Time
	var lastSeen pgtype.Timestamptz
	err := r.pool.QueryRow(ctx, `SELECT CreateTime, UpdateTime, SourceLastSeenTime FROM Components WHERE ComponentId = $1`, componentID).
		Scan(&createTime, &updateTime, &lastSeen)
	if err != nil {
		return rowTimestamps{}, err
	}
	ts := rowTimestamps{Create: createTime.UTC(), Update: updateTime.UTC()}
	if lastSeen.Valid {
		ts.LastSeen = lastSeen.Time.UTC()
	}
	return ts, nil
}

// ComponentRows implements backendRows.
func (r postgresRows) ComponentRows(ctx context.Context, name string) ([]componentRow, error) {
	rows, err := r.pool.Query(ctx, `SELECT ComponentId, State, ActiveName FROM Components WHERE Name = $1 ORDER BY ComponentId ASC`, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []componentRow
	for rows.Next() {
		var cr componentRow
		var activeName pgtype.Text
		if err := rows.Scan(&cr.ID, &cr.State, &activeName); err != nil {
			return nil, err
		}
		if activeName.Valid {
			cr.ActiveName = &activeName.String
		}
		result = append(result, cr)
	}
	return result, rows.Err()
}

// HardDeleteComponent implements backendRows.
func (r postgresRows) HardDeleteComponent(ctx context.Context, componentID int64) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM Components WHERE ComponentId = $1`, componentID)
	return err
}

// InsertExperimentWithSnapshot implements backendRows. PostgreSQL keys snapshots by the experiment
// ID itself, and their foreign key requires the experiment row first.
func (r postgresRows) InsertExperimentWithSnapshot(ctx context.Context, exp *essmsgpb.Experiment, snap snapshotRow) error {
	status, err := proto.Marshal(exp.GetStatus())
	if err != nil {
		return fmt.Errorf("failed to marshal experiment status: %w", err)
	}
	if status == nil {
		status = []byte{}
	}
	_, err = r.pool.Exec(ctx, `INSERT INTO Experiments (Xid, Title, Creator, Priority, Urls, Tags, Notes, Resources, Status, ActiveState, LaunchState, CreateTime, UpdateTime)
VALUES ($1, $2, $3, $4, '{}', $5, $6, '{}', $7, $8, $9, now(), now())`,
		snap.Xid, exp.GetTitle(), exp.GetCreator(), exp.GetPriority(), append([]string{}, exp.GetTags()...), exp.GetNotes(), status,
		int64(exp.GetStatus().GetActiveState()), int64(exp.GetStatus().GetLaunchState()))
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `INSERT INTO ExperimentComponentSnapshots (Xid, ComponentId, LockedGeneration, ConfigProto, AccessPayload) VALUES ($1, $2, $3, $4, $5)`,
		snap.Xid, snap.ComponentID, snap.LockedGeneration, snap.ConfigProto, snap.AccessPayload)
	return err
}

// CountSnapshots implements backendRows.
func (r postgresRows) CountSnapshots(ctx context.Context, componentID int64) (int64, error) {
	var count int64
	err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM ExperimentComponentSnapshots WHERE ComponentId = $1`, componentID).Scan(&count)
	return count, err
}

// FenceDeclaredTime implements backendRows.
func (r postgresRows) FenceDeclaredTime(ctx context.Context) (time.Time, error) {
	var declared time.Time
	err := r.pool.QueryRow(ctx, `SELECT DeclaredTime FROM ComponentOwnershipFence WHERE FenceId = 1`).Scan(&declared)
	return declared.UTC(), err
}

// InsertFence implements backendRows.
func (r postgresRows) InsertFence(ctx context.Context, row fenceRow) error {
	now := time.Now().UTC()
	var lastSeen any = now
	if row.NullLastSeenTime {
		lastSeen = nil
	}
	_, err := r.pool.Exec(ctx, `INSERT INTO ComponentOwnershipFence (FenceId, ActiveClusterUid, ActiveClusterName, Epoch, DeclaredTime, LastSeenTime) VALUES ($1, $2, $3, $4, $5, $6)`,
		row.FenceID, row.ClusterUID, row.ClusterName, row.Epoch, now, lastSeen)
	return err
}
