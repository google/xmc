// Copyright 2025 Google LLC
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
package main

import (
	"context"
	"fmt"

	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage/dbconfig"
	dbfactory "xmanager_cloud/internal/storage/factory"
)

func newStorage(ctx context.Context) (*dbfactory.Storage, error) {
	dbCfg, err := dbconfig.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load database config: %w", err)
	}
	applyLegacySpannerFlags(ctx, &dbCfg)
	return dbfactory.NewStorage(ctx, dbCfg, dbfactory.ESS)
}

// applyLegacySpannerFlags populates Spanner settings from the deprecated -spanner_database_name and
// -quota_project_id CLI flags when the corresponding db-config environment variables are unset.
// TODO: Remove once all deployments pass db-config environment variables.
func applyLegacySpannerFlags(ctx context.Context, dbCfg *dbconfig.Config) {
	if dbCfg.Backend != dbconfig.BackendSpanner {
		return
	}
	if dbCfg.Spanner.EssDatabase == "" && spannerDatabaseName != "" {
		logging.Warn(ctx, "Using deprecated database flag; migrate to db-config", "deprecated_flag", "-spanner_database_name", "replacement_env", "SPANNER_ESS_DATABASE")
		dbCfg.Spanner.EssDatabase = spannerDatabaseName
	}
	if dbCfg.Spanner.QuotaProjectID == "" && quotaProjectID != "" {
		logging.Warn(ctx, "Using deprecated database flag; migrate to db-config", "deprecated_flag", "-quota_project_id", "replacement_env", "SPANNER_QUOTA_PROJECT_ID")
		dbCfg.Spanner.QuotaProjectID = quotaProjectID
	}
}
