// Copyright 2026 Google LLC
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

package xid

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/spanner"
	"google.golang.org/protobuf/proto"
	fpb "xmanager_cloud/api/filter"
	xidpb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	spannerdialect "xmanager_cloud/internal/query_generator/dialect/spanner"
	queryfilter "xmanager_cloud/internal/query_generator/filter"
	"xmanager_cloud/internal/query_generator/liststatement"
	"xmanager_cloud/internal/query_generator/normalizer"
	"xmanager_cloud/internal/query_generator/querytools"
	"xmanager_cloud/internal/safesql"
	spannerdata "xmanager_cloud/internal/storage/spanner/data"
	"xmanager_cloud/internal/storage/spanner/schema/xidschema"
	spannerutils "xmanager_cloud/internal/storage/spanner/utils"
	"xmanager_cloud/internal/validator"
)

// ListXidEntries returns a paginated list of XidEntry protos based on the provided filter and order.
func (r *Repository) ListXidEntries(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*xidpb.XidEntry, string, error) {
	stmt, err := r.BuildListStatement(ctx, pageSize, pageToken, filter, orderBy)
	if err != nil {
		return nil, "", err
	}

	xidIter := r.client.Single().WithTimestampBound(spanner.ExactStaleness(staleness)).Query(ctx, *stmt.Spanner())
	defer xidIter.Stop()

	var xidEntries []*xidpb.XidEntry
	if err := xidIter.Do(func(row *spanner.Row) error {
		xidEntry, err := spannerdata.XIDRowToProto(row)
		if err != nil {
			logging.Error(ctx, "failed to convert Xid row to XidEntry", "error", err)
			return err
		}
		xidEntries = append(xidEntries, xidEntry)
		return nil
	}); err != nil {
		logging.Error(ctx, "failed to read Xid rows", "error", err)
		return nil, "", fmt.Errorf("failed to read Xid rows: %w", err)
	}

	return liststatement.ApplyPagination(xidEntries, pageSize, ExtractXidEntryCursor)
}

// BuildListStatement normalizes the filter and generates the Spanner SQL statement for listing XID entries.
func (r *Repository) BuildListStatement(ctx context.Context, pageSize int32, pageToken, filter, orderBy string) (*spannerdialect.Statement, error) {
	normalizedFilter, err := normalizer.Normalize(filter, r.xidsQueryGen.Tables()[0])
	if err != nil {
		return nil, fmt.Errorf("failed to normalize filter: %w", err)
	}

	listParams := liststatement.ListParams{
		PageSize:              pageSize,
		Filter:                normalizedFilter,
		OrderBy:               orderBy,
		QueryGen:              xidQueryGen{QueryGenerator: r.xidsQueryGen},
		DefaultSelectFields:   xidschema.XidsSelectFields,
		DefaultFullTextFields: xidschema.XidsFullTextFields,
		ValidOrderByFields:    xidschema.XidsOrderByFields,
	}
	if err := listParams.DecodePageToken(pageToken); err != nil {
		return nil, err
	}

	stmt := spannerdialect.NewStatement(nil)
	if err := liststatement.ListStatement(ctx, stmt, listParams); err != nil {
		return nil, fmt.Errorf("failed to generate list statement: %w", err)
	}
	return stmt, nil
}

type xidQueryGen struct {
	querytools.QueryGenerator
}

func (g xidQueryGen) GenerateQuery(
	stmt queryfilter.Statement,
	selectFields []querytools.TableField,
	fullTextFields []safesql.TrustedSQLString,
	baseFilterExp, userFilterExp *fpb.Expression,
	userOrderBy []querytools.UserOrderedIndex,
	pageSize, offset int,
	ptFields map[querytools.TableField]any,
	preamble *safesql.TrustedSQLString,
) (map[querytools.TableField]int, querytools.Unnester, error) {
	rewrittenFilter, err := rewriteXidExpression(userFilterExp)
	if err != nil {
		return nil, nil, err
	}
	return g.QueryGenerator.GenerateQuery(stmt, selectFields, fullTextFields, baseFilterExp, rewrittenFilter, userOrderBy, pageSize, offset, ptFields, preamble)
}

func rewriteXidExpression(exp *fpb.Expression) (*fpb.Expression, error) {
	if exp == nil {
		return nil, nil
	}
	cloned := proto.Clone(exp).(*fpb.Expression)
	if err := rewriteXidNode(cloned); err != nil {
		return nil, err
	}
	return cloned, nil
}

func rewriteXidNode(exp *fpb.Expression) error {
	if exp == nil {
		return nil
	}
	switch exp.WhichExpression() {
	case fpb.Expression_Conjunction_case:
		return rewriteXidList(exp.GetConjunction().GetExpressions())
	case fpb.Expression_Disjunction_case:
		return rewriteXidList(exp.GetDisjunction().GetExpressions())
	case fpb.Expression_Negation_case:
		return rewriteXidNode(exp.GetNegation())
	case fpb.Expression_Restriction_case:
		rewritten, err := RewriteXidRestriction(exp.GetRestriction())
		if err != nil {
			return err
		}
		*exp = *fpb.Expression_builder{Restriction: rewritten}.Build()
	}
	return nil
}

func rewriteXidList(exprs []*fpb.Expression) error {
	for _, child := range exprs {
		if err := rewriteXidNode(child); err != nil {
			return err
		}
	}
	return nil
}

// RewriteXidRestriction rewrites equality and inequality restrictions on "xid"
// to "bit_reversed_xid = ReverseBits(v)" / "bit_reversed_xid != ReverseBits(v)"
// while leaving range comparisons on "xid" and validating integer literals.
func RewriteXidRestriction(r *fpb.Restriction) (*fpb.Restriction, error) {
	valExpr, ok := extractXidOperand(r)
	if !ok {
		return r, nil
	}
	v, err := parseXidLiteral(valExpr)
	if err != nil {
		return nil, err
	}
	if r.GetComparator() != fpb.Restriction_EQUALS && r.GetComparator() != fpb.Restriction_NOT_EQUALS {
		return r, nil
	}
	return fpb.Restriction_builder{
		Comparator: r.GetComparator(),
		Lhs:        fpb.Expression_builder{Value: proto.String("bit_reversed_xid")}.Build(),
		Rhs:        fpb.Expression_builder{Value: proto.String(strconv.FormatInt(spannerutils.ReverseBits(v), 10))}.Build(),
	}.Build(), nil
}

func extractXidOperand(r *fpb.Restriction) (*fpb.Expression, bool) {
	if r.GetLhs() == nil || r.GetRhs() == nil {
		return nil, false
	}
	if r.GetLhs().WhichExpression() == fpb.Expression_Value_case {
		lhs := r.GetLhs().GetValue()
		if lhs == "xid" {
			return r.GetRhs(), true
		}
		if _, isField := xidschema.XidTables[0].Fields[querytools.TableField(lhs)]; isField {
			return nil, false
		}
	}
	if rhs, ok := extractExprString(r.GetRhs()); ok && rhs == "xid" {
		return r.GetLhs(), true
	}
	return nil, false
}

func parseXidLiteral(valExpr *fpb.Expression) (int64, error) {
	raw, ok := extractExprString(valExpr)
	if !ok {
		return 0, fmt.Errorf("invalid integer literal for field \"xid\": %w", grpcerr.ErrInvalidArgument)
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid integer literal %q for field \"xid\": %w", raw, grpcerr.ErrInvalidArgument)
	}
	return v, nil
}

func extractExprString(expr *fpb.Expression) (string, bool) {
	switch expr.WhichExpression() {
	case fpb.Expression_Value_case:
		return strings.Trim(expr.GetValue(), "\"'"), true
	case fpb.Expression_Phrase_case:
		return strings.Join(expr.GetPhrase().GetValues(), " "), true
	default:
		return "", false
	}
}

// ExtractXidEntryCursor extracts pagination cursor values from an XidEntry proto.
func ExtractXidEntryCursor(entry *xidpb.XidEntry) (map[string]any, error) {
	xid, err := validator.ValidateXIDName(entry.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to validate XID name %q: %w", entry.GetName(), err)
	}

	cursor := map[string]any{
		"xid":              xid,
		"bit_reversed_xid": spannerutils.ReverseBits(xid),
	}

	if entry.CreateTime != nil {
		cursor["created"] = entry.CreateTime.AsTime()
	}
	if entry.UpdateTime != nil {
		cursor["updated"] = entry.UpdateTime.AsTime()
	}

	return cursor, nil
}
