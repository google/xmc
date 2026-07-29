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

// Package artifact contains the concrete Spanner repository implementation for artifacts.
package artifact

import (
	"context"
	"fmt"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"

	"xmanager_cloud/internal/storage"
	"xmanager_cloud/query_generator/querytools"
	"xmanager_cloud/storage/spanner/schema/artifactschema"
)

var _ storage.ArtifactStore = (*Repository)(nil)

type spannerTransaction interface {
	ReadRow(ctx context.Context, table string, key spanner.Key, columns []string) (*spanner.Row, error)
	Query(ctx context.Context, stmt spanner.Statement) *spanner.RowIterator
}

// Repository manages persistence for artifact entities in Cloud Spanner.
type Repository struct {
	client            *spanner.Client
	artifactsQueryGen querytools.QueryGenerator
}

// NewRepository creates a new Artifact repository instance with initialized query generator.
func NewRepository(client *spanner.Client) (*Repository, error) {
	if client == nil {
		return nil, fmt.Errorf("spanner client must not be nil")
	}

	artifactsQueryGen, err := querytools.NewQueryGenerator(artifactschema.ArtifactTables, nil, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create artifacts query generator: %w", err)
	}

	return &Repository{
		client:            client,
		artifactsQueryGen: artifactsQueryGen,
	}, nil
}
