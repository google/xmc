// Copyright 2025 Google LLC
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
// Package spanner contains the concrete implementation for the storage layer using Cloud Spanner.
package spanner

import (
	"context"
	"fmt"
	"math/bits"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"xmanager_cloud/query_generator/querytools"
	"xmanager_cloud/storage/spanner/schema/artifactschema"
	"xmanager_cloud/storage/spanner/schema/experimentschema"
	"xmanager_cloud/storage/spanner/schema/statusmessageschema"
	"xmanager_cloud/storage/spanner/schema/workunitschema"
	"xmanager_cloud/storage/spanner/schema/xidschema"
	"task/go/status"
)

// spanner.ReadOnlyTransaction and spanner.ReadWriteTransaction.
type spannerTransaction interface {
	ReadRow(ctx context.Context, table string, key spanner.Key, columns []string) (*spanner.Row, error)
	Query(ctx context.Context, stmt spanner.Statement) *spanner.RowIterator
}

// Storage implements the storage.Storage interface using Google Cloud Spanner.
type Storage struct {
	client                      *spanner.Client
	experimentsQueryGen         querytools.QueryGenerator
	workUnitsQueryGen           querytools.QueryGenerator
	xidsQueryGen                querytools.QueryGenerator
	artifactsQueryGen           querytools.QueryGenerator
	statusMessagesQueryGen      querytools.QueryGenerator
	statusMessageHistoryEnabled bool
}

// NewStorage creates a new instance of the Spanner-backed storage.
func NewStorage(client *spanner.Client) (*Storage, error) {
	if client == nil {
		return nil, fmt.Errorf("spanner client must not be nil: %w", status.ErrInvalidArgument)
	}

	xidsQueryGen, err := querytools.NewQueryGenerator(xidschema.XidTables, nil, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Xids query generator: %w", err)
	}

	workUnitsQueryGen, err := querytools.NewQueryGenerator(workunitschema.WorkUnitTables, nil, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create WorkUnits query generator: %w", err)
	}

	experimentsQueryGen, err := querytools.NewQueryGenerator(experimentschema.ExperimentTables, nil, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Experiments query generator: %w", err)
	}

	artifactsQueryGen, err := querytools.NewQueryGenerator(artifactschema.ArtifactTables, nil, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Artifacts query generator: %w", err)
	}

	statusMessagesQueryGen, err := querytools.NewQueryGenerator(statusmessageschema.StatusMessageTables, nil, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create StatusMessages query generator: %w", err)
	}

	return &Storage{
		client:                 client,
		xidsQueryGen:           xidsQueryGen,
		experimentsQueryGen:    experimentsQueryGen,
		workUnitsQueryGen:      workUnitsQueryGen,
		artifactsQueryGen:      artifactsQueryGen,
		statusMessagesQueryGen: statusMessagesQueryGen,
	}, nil
}

// StorageConfig holds configuration for the Storage.
type StorageConfig struct {
	StatusMessageHistoryEnabled bool
}

// NewStorageWithConfig creates a new instance of the Spanner-backed storage with the given configuration.
func NewStorageWithConfig(client *spanner.Client, cfg StorageConfig) (*Storage, error) {
	s, err := NewStorage(client)
	if err != nil {
		return nil, err
	}
	s.statusMessageHistoryEnabled = cfg.StatusMessageHistoryEnabled
	return s, nil
}

func reverseBits(x int64) int64 {
	return int64(bits.Reverse64(uint64(x)))
}
