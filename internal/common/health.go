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
// Package health contains shared health check functions used by other services in the xmanager_cloud project.
package health

import (
	"context"
	"errors"
	"fmt"

	"cloud.google.com/go/spanner"
	"google.golang.org/api/iterator"
	"k8s.io/client-go/kubernetes"
	"xmanager_cloud/logging/logging"
)

// ErrNilClient is returned when a client is nil.
var ErrNilClient = errors.New("client is nil")

// CheckSpanner validates that a Spanner client is connected and ready using a simple query.
func CheckSpanner(ctx context.Context, sc *spanner.Client) error {
	if sc == nil {
		return fmt.Errorf("spanner: %w", ErrNilClient)
	}

	iter := sc.Single().Query(ctx, spanner.Statement{SQL: "SELECT 1"})
	defer iter.Stop()
	_, err := iter.Next()
	if err != nil && err != iterator.Done {
		return fmt.Errorf("spanner reachability: %w", err)
	}
	logging.Debug(ctx, "Spanner health check completed successfully")
	return nil
}

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
