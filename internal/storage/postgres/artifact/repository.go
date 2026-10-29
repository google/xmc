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

// Package artifact contains the concrete PostgreSQL repository implementation for artifacts.
package artifact

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kylelemons/godebug/diff"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/query_generator/liststatement"
	"xmanager_cloud/internal/query_generator/querytools"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/data"
	postgresdata "xmanager_cloud/internal/storage/postgres/data"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/internal/storage/postgres/schema/artifactschema"
	"xmanager_cloud/internal/validator"
)

const entityName = "artifact"

var (
	insertArtifactSQL = safesql.New(`INSERT INTO Artifacts (Xid, WorkUnitId, ArtifactId, Url, Icon, DisplayName, Priority, Tags, Notes, LifecyclePhase, DataType, MimeType, AdditionalInfo, Payload, PayloadType, SearchableContent, CreateTime, UpdateTime)
VALUES (@xid, @workunitid, @artifactid, @url, @icon, @displayname, @priority, @tags, @notes, @lifecyclephase, @datatype, @mimetype, @additionalinfo, @payload, @payloadtype, @searchablecontent, @createtime, @updatetime)`)

	selectArtifactSQL = safesql.New(`SELECT Xid, WorkUnitId, ArtifactId, Url, Icon, DisplayName, Priority, Tags, Notes, LifecyclePhase, DataType, MimeType, AdditionalInfo, Payload, CreateTime, UpdateTime FROM Artifacts WHERE ArtifactId = $1`)

	// lockArtifactSQL locks the row with FOR NO KEY UPDATE so foreign-key FOR KEY SHARE checks do not conflict.
	lockArtifactSQL = safesql.Concat(selectArtifactSQL, safesql.New(" FOR NO KEY UPDATE"))

	updateArtifactSQL = safesql.New(`UPDATE Artifacts SET Url = @url, Icon = @icon, DisplayName = @displayname, Priority = @priority, Tags = @tags, Notes = @notes, LifecyclePhase = @lifecyclephase, DataType = @datatype, MimeType = @mimetype, AdditionalInfo = @additionalinfo, Payload = @payload, PayloadType = @payloadtype, SearchableContent = @searchablecontent, UpdateTime = @updatetime WHERE Xid = @xid AND ArtifactId = @artifactid`)

	deleteArtifactSQL = safesql.New(`DELETE FROM Artifacts WHERE ArtifactId = $1`)
)

var _ storage.ArtifactStore = (*Repository)(nil)

// Repository manages persistence for artifact entities in PostgreSQL.
type Repository struct {
	pool              *pgxpool.Pool
	artifactsQueryGen querytools.QueryGenerator
}

// NewRepository creates a new PostgreSQL Artifact repository instance bound to pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	qgen, err := querytools.NewQueryGenerator(artifactschema.ArtifactTables, nil, nil, nil, querytools.DeduplicateSelects(true))
	if err != nil {
		panic(fmt.Sprintf("failed to initialize artifacts query generator: %v", err))
	}
	return &Repository{pool: pool, artifactsQueryGen: qgen}
}

// CreateArtifact creates a new artifact.
func (r *Repository) CreateArtifact(ctx context.Context, artifact *essmsgpb.Artifact) (*essmsgpb.Artifact, error) {
	if r.pool == nil {
		return nil, pgdb.WrapStatusError("postgres pool must not be nil", grpcerr.ErrFailedPrecondition, nil)
	}
	if artifact == nil {
		return nil, pgdb.WrapStatusError("artifact must not be nil", grpcerr.ErrInvalidArgument, nil)
	}
	ctx = logging.WithArtifactName(ctx, artifact.GetName())
	logging.Info(ctx, "Write artifact to storage", "artifact", artifact)
	now := timestamppb.New(time.Now().UTC().Truncate(time.Microsecond))
	created := proto.Clone(artifact).(*essmsgpb.Artifact)
	created.CreateTime = now
	created.UpdateTime = now
	row, err := postgresdata.ArtifactProtoToRow(created)
	if err != nil {
		return nil, pgdb.WrapStatusError("invalid Artifact", grpcerr.ErrInvalidArgument, err)
	}
	if _, err := r.pool.Exec(ctx, insertArtifactSQL.String(), row.GetInsertArgs()); err != nil {
		logging.Error(ctx, "Failed to create artifact", "error", err)
		return nil, pgdb.TranslateError(err, entityName)
	}
	logging.Info(ctx, "Written artifact to storage")
	return created, nil
}

// FetchArtifact retrieves an artifact by its unique ID.
func (r *Repository) FetchArtifact(ctx context.Context, aid int64) (*essmsgpb.Artifact, error) {
	ctx = logging.WithArtifactName(ctx, validator.ConstructArtifactName(aid))
	return queryArtifact(ctx, pgdb.FromPool(r.pool), selectArtifactSQL, aid)
}

// ListArtifacts retrieves a list of artifacts matching the given filter.
func (r *Repository) ListArtifacts(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*essmsgpb.Artifact, string, error) {
	logging.Info(ctx, "List artifacts", "pageSize", pageSize, "pageToken", pageToken, "filter", filter, "orderBy", orderBy, "staleness", staleness)
	var err error
	filter, ctx, err = buildParentFilter(ctx, parent, filter)
	if err != nil {
		return nil, "", err
	}
	artifacts, nextPageToken, err := pgdb.List(ctx, pgdb.FromPool(r.pool), entityName, pageToken, liststatement.ListParams{
		PageSize: pageSize, Filter: filter, OrderBy: orderBy, QueryGen: r.artifactsQueryGen,
		DefaultSelectFields:   artifactschema.ArtifactsSelectFields,
		DefaultFullTextFields: artifactschema.ArtifactsFullTextFields,
		ValidOrderByFields:    artifactschema.ArtifactsOrderByFields,
	}, postgresdata.ArtifactRowToProto, ExtractArtifactCursor)
	if err != nil {
		return nil, "", err
	}
	logging.Info(ctx, "Listed artifacts", "count", len(artifacts), "nextPageToken", nextPageToken)
	return artifacts, nextPageToken, nil
}

// UpdateArtifact updates an artifact with the given update mask.
func (r *Repository) UpdateArtifact(ctx context.Context, update *storage.ArtifactUpdate) (storage.UpdatedArtifacts, error) {
	if err := validateArtifactUpdate(update); err != nil {
		return storage.UpdatedArtifacts{}, err
	}
	ctx = logging.WithArtifactName(ctx, validator.ConstructArtifactName(update.AID))
	logging.Info(ctx, "Update artifact in storage", "update", update.Artifact)
	var originalArtifact, updatedArtifact *essmsgpb.Artifact
	var mutated bool
	err := pgdb.RunInTx(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		mutated, originalArtifact, updatedArtifact = false, nil, nil
		var err error
		if originalArtifact, err = queryArtifact(ctx, tx, lockArtifactSQL, update.AID); err != nil {
			return err
		}
		if updatedArtifact, err = data.MergeArtifacts(originalArtifact, update.Artifact, update.UpdateMask); err != nil {
			return fmt.Errorf("failed to merge Artifact: %w", err)
		}
		if proto.Equal(originalArtifact, updatedArtifact) {
			return nil
		}
		if err := persistUpdatedArtifact(ctx, tx, updatedArtifact); err != nil {
			return err
		}
		mutated = true
		return nil
	})
	if err != nil {
		return storage.UpdatedArtifacts{}, err
	}
	logArtifactUpdate(ctx, mutated, originalArtifact, updatedArtifact)
	return storage.UpdatedArtifacts{Original: originalArtifact, Updated: updatedArtifact}, nil
}

// DeleteArtifact deletes an artifact by its unique ID.
func (r *Repository) DeleteArtifact(ctx context.Context, aid int64) error {
	if r.pool == nil {
		return pgdb.WrapStatusError("postgres pool must not be nil", grpcerr.ErrFailedPrecondition, nil)
	}
	ctx = logging.WithArtifactName(ctx, validator.ConstructArtifactName(aid))
	logging.Info(ctx, "Delete artifact in storage")
	tag, err := r.pool.Exec(ctx, deleteArtifactSQL.String(), aid)
	if err != nil {
		logging.Error(ctx, "Failed to delete artifact", "error", err)
		return pgdb.TranslateError(err, entityName)
	}
	if tag.RowsAffected() == 0 {
		return pgdb.TranslateError(pgx.ErrNoRows, entityName)
	}
	logging.Info(ctx, "Deleted artifact from storage")
	return nil
}

// ExtractArtifactCursor extracts pagination cursor values from an Artifact proto.
func ExtractArtifactCursor(artifact *essmsgpb.Artifact) (map[string]any, error) {
	aid, err := validator.ValidateArtifactName(artifact.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to validate artifact name %q: %w", artifact.GetName(), err)
	}
	xid, wid, err := validator.ValidateArtifactParent(artifact.GetParent())
	if err != nil {
		return nil, fmt.Errorf("failed to validate artifact parent %q: %w", artifact.GetParent(), err)
	}
	cursor := map[string]any{
		"xid": xid, "work_unit_id": nil, "artifact_id": aid,
		"display_name": artifact.GetUrl().GetDisplayName(), "priority": data.ArtifactPriority(artifact),
	}
	if wid != 0 {
		cursor["work_unit_id"] = wid
	}
	if artifact.GetCreateTime() != nil {
		cursor["created"] = artifact.GetCreateTime().AsTime()
	}
	if artifact.GetUpdateTime() != nil {
		cursor["updated"] = artifact.GetUpdateTime().AsTime()
	}
	return cursor, nil
}

func validateArtifactUpdate(u *storage.ArtifactUpdate) error {
	if u == nil || u.Artifact == nil || len(u.UpdateMask.GetPaths()) == 0 {
		return pgdb.WrapStatusError("update, artifact, and updateMask must not be empty", grpcerr.ErrInvalidArgument, nil)
	}
	return nil
}

func queryArtifact(ctx context.Context, db pgdb.DBTX, query safesql.TrustedSQLString, aid int64) (*essmsgpb.Artifact, error) {
	logging.Info(ctx, "Fetch artifact row")
	row, err := pgdb.QueryRow[postgresdata.ArtifactRow](ctx, db, query.String(), entityName, aid)
	if err != nil {
		return nil, err
	}
	artifact, err := postgresdata.ArtifactRowToProto(row)
	if err != nil {
		return nil, fmt.Errorf("failed to convert Artifact row to proto: %w", err)
	}
	logging.Info(ctx, "Fetched artifact from storage", "artifact", artifact)
	return artifact, nil
}

func persistUpdatedArtifact(ctx context.Context, tx pgx.Tx, updatedArtifact *essmsgpb.Artifact) error {
	now := time.Now().UTC().Truncate(time.Microsecond)
	if prev := updatedArtifact.GetUpdateTime().AsTime(); !now.After(prev) {
		now = prev.Add(time.Microsecond)
	}
	updatedArtifact.UpdateTime = timestamppb.New(now)
	row, err := postgresdata.ArtifactProtoToRow(updatedArtifact)
	if err != nil {
		return fmt.Errorf("failed to convert Artifact to row: %w", err)
	}
	if _, err := tx.Exec(ctx, updateArtifactSQL.String(), row.GetUpdateArgs()); err != nil {
		return pgdb.TranslateError(err, entityName)
	}
	return nil
}

func logArtifactUpdate(ctx context.Context, mutated bool, original, updated *essmsgpb.Artifact) {
	if !mutated {
		logging.Debug(ctx, "Artifact not updated; no changes detected", "updatedArtifact", updated)
		return
	}
	logging.Info(ctx, fmt.Sprintf("Updated artifact in storage: %s", diff.Diff(prototext.Format(original), prototext.Format(updated))), "updatedArtifact", updated)
}

func buildParentFilter(ctx context.Context, parent, filter string) (string, context.Context, error) {
	if parent == "" {
		return filter, ctx, nil
	}
	xid, wid, err := validator.ValidateArtifactParent(parent)
	if err != nil {
		return "", ctx, pgdb.WrapStatusError(fmt.Sprintf("failed to validate artifact parent name %q", parent), grpcerr.ErrInvalidArgument, err)
	}
	ctx = logging.WithArtifactParent(ctx, parent)
	parentFilter := fmt.Sprintf("xid = %d", xid)
	if wid != 0 {
		parentFilter = fmt.Sprintf("%s AND work_unit_id = %d", parentFilter, wid)
	}
	if filter == "" {
		return parentFilter, ctx, nil
	}
	return fmt.Sprintf("%s AND (%s)", parentFilter, filter), ctx, nil
}
