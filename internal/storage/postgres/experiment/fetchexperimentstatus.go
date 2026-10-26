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
	"fmt"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	pb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/internal/validator"
)

// FetchExperimentStatus retrieves the ExperimentStatus for a given XID.
func (r *Repository) FetchExperimentStatus(ctx context.Context, xid int64) (*pb.ExperimentStatus, error) {
	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(xid))
	if r.db == nil {
		return nil, errNilPool
	}
	logging.Info(ctx, "Fetch ExperimentStatus row")

	var raw []byte
	if err := r.db.QueryRow(ctx, selectExperimentStatusSQL.String(), xid).Scan(&raw); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			logging.Error(ctx, "Failed to read Experiment row", "error", err)
		}
		return nil, pgdb.TranslateError(err, experimentEntity(xid))
	}

	var expStatus pb.ExperimentStatus
	if err := proto.Unmarshal(raw, &expStatus); err != nil {
		return nil, fmt.Errorf("failed to read column Status: %w", err)
	}

	logging.Info(ctx, "Fetched ExperimentStatus from storage", "status", &expStatus)

	return &expStatus, nil
}
