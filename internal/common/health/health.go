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

// Package health contains shared health check functions used by other services in the xmanager_cloud project.
package health

import (
	"context"
	"errors"
	"fmt"

	"xmanager_cloud/internal/logging"

	"k8s.io/client-go/kubernetes"
)

// ErrNilClient is returned when a client is nil.
var ErrNilClient = errors.New("client is nil")

// CheckKubernetes validates that a Kubernetes clientset is connected and ready.
func CheckKubernetes(clientset kubernetes.Interface) error {
	if clientset == nil || clientset.Discovery() == nil {
		return fmt.Errorf("kubernetes: %w", ErrNilClient)
	}
	_, err := clientset.Discovery().ServerVersion()
	if err != nil {
		return fmt.Errorf("kubernetes reachability: %w", err)
	}
	return nil
}

// SchemaVersionClient reads a database's schema version and whether its last migration is dirty.
type SchemaVersionClient interface {
	QuerySchemaVersion(ctx context.Context) (version int64, dirty bool, err error)
}

// CheckSchemaVersion returns an error unless the database is reachable, its last migration is not
// dirty, and its schema version is expectedVersion.
func CheckSchemaVersion(ctx context.Context, client SchemaVersionClient, expectedVersion int64) error {
	if client == nil {
		return fmt.Errorf("schema version client: %w", ErrNilClient)
	}
	if expectedVersion <= 0 {
		return fmt.Errorf("invalid expected schema version: %d", expectedVersion)
	}
	versionVal, dirty, err := client.QuerySchemaVersion(ctx)
	if err != nil {
		return err
	}

	if dirty {
		return fmt.Errorf("schema migration is in a dirty state at version %d", versionVal)
	}

	if versionVal != expectedVersion {
		return fmt.Errorf("schema version %d does not match expected version %d", versionVal, expectedVersion)
	}

	logging.Debug(ctx, "Schema migration version check completed successfully", "version", versionVal)
	return nil
}

// ReadinessChecker returns an error unless a dependency, such as a Storage opened by
// internal/storage/factory, is ready to serve requests.
type ReadinessChecker interface {
	CheckReady(ctx context.Context) error
}
