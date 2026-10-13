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

	"cloud.google.com/go/spanner"
	"google.golang.org/api/iterator"

	fepb "xmanager_cloud/api/filter"
	aipfilter "xmanager_cloud/api/filter/go/filter"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/query_generator/querytools"
	"xmanager_cloud/internal/storage/spanner/dashboard/data"
	"xmanager_cloud/internal/storage/spanner/schema/dashboardschema"
)

// DeleteChart deletes a chart by its unique ID in a ReadWriteTransaction.
func (r *Repository) DeleteChart(ctx context.Context, chartId string) error {
	if chartId == "" {
		return fmt.Errorf("chartId must not be empty")
	}

	logging.Info(ctx, "Delete chart from storage", "chartId", chartId)

	_, err := r.client.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
		// 1. Read the existing chart from storage to verify existence.
		if _, err := tx.ReadRow(ctx, dashboardschema.ChartsTableName, spanner.Key{chartId}, []string{"ChartId"}); err != nil {
			return fmt.Errorf("failed to read chart %s: %w", chartId, err)
		}

		artMuts, err := r.deleteArtifactChartReferences(ctx, tx, chartId)
		if err != nil {
			return err
		}

		dashMuts, err := r.deleteDashboardChartReferences(ctx, tx, chartId)
		if err != nil {
			return err
		}

		muts := append(artMuts, dashMuts...)
		muts = append(muts, spanner.Delete(dashboardschema.ChartsTableName, spanner.Key{chartId}))

		return tx.BufferWrite(muts)
	})
	if err != nil {
		logging.Error(ctx, "Failed to delete chart", "chartId", chartId, "error", err)
		return err
	}

	logging.Info(ctx, "Deleted chart from storage", "chartId", chartId)
	return nil
}

func (r *Repository) deleteArtifactChartReferences(ctx context.Context, tx *spanner.ReadWriteTransaction, chartId string) ([]*spanner.Mutation, error) {
	filterExp, err := aipfilter.ParseExpressionWithOptions(fepb.ExpressionParserOptions_builder{
		RejectLegacyOperatorPrecedence: true,
		EnableSimpleQuotedStrings:      true,
		EnforceValidStrings:            true,
	}.Build(), fmt.Sprintf("chart_id = %q", chartId))
	if err != nil {
		return nil, fmt.Errorf("failed to parse filter for artifact charts: %w", err)
	}

	stmt, _, _, err := r.artifactChartsQueryGen.GenerateQuery(
		[]querytools.TableField{
			dashboardschema.BitReversedXidTableField,
			dashboardschema.WorkUnitIDTableField,
			dashboardschema.ArtifactIDTableField,
			dashboardschema.ArtifactChartIDTableField,
		},
		nil, nil, filterExp, nil, 0, 0, nil, nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to generate artifact charts query: %w", err)
	}

	iter := tx.Query(ctx, *stmt)
	defer iter.Stop()

	var muts []*spanner.Mutation
	for {
		row, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read artifact chart rows: %w", err)
		}
		mut, err := data.ArtifactChartRowToDeleteMutation(row)
		if err != nil {
			return nil, err
		}
		muts = append(muts, mut)
	}
	return muts, nil
}

func (r *Repository) deleteDashboardChartReferences(ctx context.Context, tx *spanner.ReadWriteTransaction, chartId string) ([]*spanner.Mutation, error) {
	filterExp, err := aipfilter.ParseExpressionWithOptions(fepb.ExpressionParserOptions_builder{
		RejectLegacyOperatorPrecedence: true,
		EnableSimpleQuotedStrings:      true,
		EnforceValidStrings:            true,
	}.Build(), fmt.Sprintf("chart_id = %q", chartId))
	if err != nil {
		return nil, fmt.Errorf("failed to parse filter for dashboard charts: %w", err)
	}

	stmt, _, _, err := r.dashboardChartsQueryGen.GenerateQuery(
		[]querytools.TableField{
			dashboardschema.DashboardIDTableField,
			dashboardschema.DashboardChartIDTableField,
		},
		nil, nil, filterExp, nil, 0, 0, nil, nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to generate dashboard charts query: %w", err)
	}

	iter := tx.Query(ctx, *stmt)
	defer iter.Stop()

	var muts []*spanner.Mutation
	for {
		row, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read dashboard chart rows: %w", err)
		}
		mut, err := data.DashboardChartRowToDeleteMutation(row)
		if err != nil {
			return nil, err
		}
		muts = append(muts, mut)
	}
	return muts, nil
}
