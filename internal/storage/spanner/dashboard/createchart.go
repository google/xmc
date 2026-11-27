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

package dashboard

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	dpb "xmanager_cloud/api/protos/dashboard"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage/spanner/dashboard/data"
)

// CreateChart writes a new chart and its plot specifications to Spanner. It never overwrites an
// existing chart: a duplicate chart ID returns grpcerr.ErrAlreadyExists and writes nothing.
func (r *Repository) CreateChart(ctx context.Context, chart *dpb.Chart) error {
	logging.Info(ctx, "Create chart in storage", "name", chart.GetName())

	mutations, err := data.ChartProtoToInsertMutations(chart)
	if err != nil {
		logging.Error(ctx, "Failed to generate create chart mutations", "error", err)
		return err
	}

	_, err = r.client.Apply(ctx, mutations)
	if status.Code(err) == codes.AlreadyExists {
		return fmt.Errorf("chart %q already exists: %w: %w", chart.GetName(), grpcerr.ErrAlreadyExists, err)
	}
	if err != nil {
		logging.Error(ctx, "Failed to apply create chart mutations", "error", err)
		return err
	}
	return nil
}
