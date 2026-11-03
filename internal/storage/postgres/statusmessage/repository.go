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

// Package statusmessage contains the concrete PostgreSQL repository implementation for status messages.
package statusmessage

import (
	"context"
	"crypto/rand"
	"fmt"
	"math"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	postgresdialect "xmanager_cloud/internal/query_generator/dialect/postgres"
	ls "xmanager_cloud/internal/query_generator/liststatement"
	"xmanager_cloud/internal/query_generator/normalizer"
	"xmanager_cloud/internal/query_generator/querytools"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/internal/storage"
	postgresdata "xmanager_cloud/internal/storage/postgres/data"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/internal/storage/postgres/schema/statusmessageschema"
	"xmanager_cloud/internal/validator"
)

const entityName = "status message"

var insertStatusMessageSQL = safesql.New(`INSERT INTO StatusMessages (Xid, WorkUnitId, MessageId, ExecutionId, Severity, Source, UserFacingText, UserFacingUrl, CreateTime, PreviousMessage, AuxiliaryInfo)
VALUES (@xid, @workunitid, @messageid, @executionid, @severity, @source, @userfacingtext, @userfacingurl, @createtime, @previousmessage, @auxiliaryinfo)`)

var (
	_ storage.StatusMessageStore        = (*Repository)(nil)
	_ storage.StatusMessageTxRepository = (*TxRepository)(nil)
)

// Repository manages persistence for status message entities in PostgreSQL.
type Repository struct {
	db pgdb.DBTX
}

// TxRepository manages transactional persistence for status message entities in PostgreSQL.
type TxRepository struct {
	tx pgdb.DBTX
}

// NewRepository creates a new PostgreSQL StatusMessage repository instance bound to pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{db: pgdb.FromPool(pool)}
}

// WithTx returns a wrapper bound to the read-write transaction tx.
func (r *Repository) WithTx(tx pgdb.DBTX) *TxRepository {
	return &TxRepository{tx: tx}
}

// ListStatusMessages retrieves a list of status messages for an experiment or work unit.
func (r *Repository) ListStatusMessages(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, _ time.Duration) ([]*essmsgpb.StatusMessage, string, error) {
	logging.Debug(ctx, "ListStatusMessages", "parent", parent, "pageSize", pageSize, "filter", filter)
	if r.db == nil {
		return nil, "", pgdb.WrapStatusError("postgres statusmessage repository db must not be nil", grpcerr.ErrFailedPrecondition, nil)
	}
	stmt, err := buildListStatement(ctx, parent, pageSize, pageToken, filter, orderBy)
	if err != nil {
		return nil, "", err
	}
	messages, err := r.queryStatusMessages(ctx, stmt)
	if err != nil {
		return nil, "", err
	}
	pagedMessages, nextPageToken, err := ls.ApplyPagination(messages, pageSize, ExtractStatusMessageCursor)
	if err != nil {
		return nil, "", fmt.Errorf("failed to paginate status messages: %w", err)
	}
	return pagedMessages, nextPageToken, nil
}

// SaveStatusMessageHistory records a status message history entry within the transaction.
func (r *TxRepository) SaveStatusMessageHistory(ctx context.Context, eid *int64, oldMsg, newMsg *essmsgpb.StructuredMessage) error {
	if err := r.validateTxContext(ctx); err != nil {
		return err
	}
	if newMsg == nil {
		return pgdb.WrapStatusError("new status message must not be nil", grpcerr.ErrInvalidArgument, nil)
	}
	messageID, err := generateMessageID()
	if err != nil {
		return fmt.Errorf("failed to generate message ID: %w", err)
	}
	row, err := postgresdata.StatusMessageProtoToRow(newMsg, messageID, eid, oldMsg)
	if err != nil {
		return fmt.Errorf("failed to create row for status message: %w", err)
	}
	_, err = r.tx.Exec(ctx, insertStatusMessageSQL.String(), row.GetInsertArgs())
	return pgdb.TranslateError(err, entityName)
}

// ExtractStatusMessageCursor extracts pagination cursor values from a StatusMessage proto.
func ExtractStatusMessageCursor(msg *essmsgpb.StatusMessage) (map[string]any, error) {
	xid, wid, messageID, err := validator.ValidateStatusMessageName(msg.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to validate status message name %q: %w", msg.GetName(), err)
	}
	var workUnitID any
	if wid != 0 {
		workUnitID = wid
	}
	return map[string]any{
		"xid":          xid,
		"work_unit_id": workUnitID,
		"message_id":   messageID,
		"create_time":  msg.GetContent().GetCreateTime().AsTime(),
	}, nil
}

func buildListStatement(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string) (*postgresdialect.Statement, error) {
	combinedFilter, err := buildParentFilter(parent, filter)
	if err != nil {
		return nil, err
	}
	queryGen, err := querytools.NewQueryGenerator(statusmessageschema.StatusMessageTables, nil, nil, nil, querytools.DeduplicateSelects(true))
	if err != nil {
		return nil, fmt.Errorf("failed to create StatusMessages query generator: %w", err)
	}
	normalizedFilter, err := normalizer.Normalize(combinedFilter, queryGen.Tables()[0])
	if err != nil {
		return nil, fmt.Errorf("failed to normalize filter: %w", err)
	}
	listParams := ls.ListParams{
		PageSize:            pageSize,
		Filter:              normalizedFilter,
		OrderBy:             orderBy,
		QueryGen:            queryGen,
		DefaultSelectFields: statusmessageschema.StatusMessagesSelectFields,
		ValidOrderByFields:  statusmessageschema.StatusMessagesOrderByFields,
	}
	if err := listParams.DecodePageToken(pageToken); err != nil {
		return nil, err
	}
	stmt := postgresdialect.NewStatement("")
	if err := ls.ListStatement(ctx, stmt, listParams); err != nil {
		return nil, fmt.Errorf("failed to generate list statement: %w", err)
	}
	return stmt, nil
}

func buildParentFilter(parent, filter string) (string, error) {
	xid, wid, err := validator.ValidateStatusMessageParent(parent)
	if err != nil {
		return "", fmt.Errorf("invalid parent resource name %q: %w", parent, err)
	}
	parentFilter := fmt.Sprintf("xid = %d", xid)
	if wid != 0 {
		parentFilter = fmt.Sprintf("%s AND work_unit_id = %d", parentFilter, wid)
	}
	if filter == "" {
		return parentFilter, nil
	}
	return fmt.Sprintf("%s AND (%s)", parentFilter, filter), nil
}

func (r *Repository) queryStatusMessages(ctx context.Context, stmt *postgresdialect.Statement) ([]*essmsgpb.StatusMessage, error) {
	rows, err := r.db.Query(ctx, stmt.SQL, stmt.Args...)
	if err != nil {
		return nil, pgdb.TranslateError(err, entityName)
	}
	rawRows, err := pgx.CollectRows(rows, pgx.RowToStructByNameLax[postgresdata.StatusMessageRow])
	if err != nil {
		return nil, pgdb.TranslateError(err, entityName)
	}
	messages := make([]*essmsgpb.StatusMessage, len(rawRows))
	for i, row := range rawRows {
		msg, err := postgresdata.StatusMessageRowToProto(row)
		if err != nil {
			return nil, fmt.Errorf("failed to decode status message row: %w", err)
		}
		messages[i] = msg
	}
	return messages, nil
}

func (r *TxRepository) validateTxContext(ctx context.Context) error {
	if ctx == nil {
		return pgdb.WrapStatusError("context must not be nil", grpcerr.ErrInvalidArgument, nil)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.tx == nil {
		return pgdb.WrapStatusError("transaction must not be nil", grpcerr.ErrFailedPrecondition, nil)
	}
	return nil
}

func generateMessageID() (int64, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(math.MaxInt64))
	if err != nil {
		return 0, fmt.Errorf("failed to generate random MessageId: %w", err)
	}
	return n.Int64() + 1, nil
}
