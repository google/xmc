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

package experiment

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	pb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/internal/validator"
)

// FetchExperiment retrieves a single Experiment from storage by its unique numerical ID without
// locking it, so callers that write the experiment back must read it with
// TxRepository.FetchExperiment instead.
func (r *Repository) FetchExperiment(ctx context.Context, xid int64) (*pb.Experiment, error) {
	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(xid))
	if r.db == nil {
		return nil, errNilPool
	}
	return fetchExperiment(ctx, r.db, selectExperimentSQL, xid)
}

// FetchExperiment retrieves a single Experiment from storage by its unique numerical ID within
// the bound read-write transaction and locks its row with FOR NO KEY UPDATE until the transaction
// ends, so that concurrent read-modify-write transactions do not lose each other's updates.
func (r *TxRepository) FetchExperiment(ctx context.Context, xid int64) (*pb.Experiment, error) {
	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(xid))
	if r.tx == nil {
		return nil, errNilTx
	}
	return fetchExperiment(ctx, r.tx, lockExperimentSQL, xid)
}

// fetchExperiment reads the experiment xid with query, either selectExperimentSQL or
// lockExperimentSQL, and translates a missing row to a NotFound error.
func fetchExperiment(ctx context.Context, db pgdb.DBTX, query safesql.TrustedSQLString, xid int64) (*pb.Experiment, error) {
	logging.Info(ctx, "Fetch experiment row")

	exp, err := fetchExperimentRow(ctx, db, query, xid)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			logging.Error(ctx, "Failed to read Experiment row", "error", err)
		}
		return nil, pgdb.TranslateError(err, experimentEntity(xid))
	}

	logging.Info(ctx, "Fetched experiment from storage", "experiment", exp)

	return exp, nil
}
