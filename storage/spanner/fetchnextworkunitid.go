package spanner

import (
	"context"
	"fmt"

	"googlesql/safesql/go/safesql"
	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"xmanager_cloud/logging/logging"
)

var nextWorkUnitIDSQL = safesql.New(`
	SELECT
		IFNULL(MAX(WorkUnitId), 0)
	FROM
		WorkUnits
	WHERE
		BitReversedXid = @bit_reversed_xid
`)

// fetchNextWorkUnitIDTransaction retrieves the next available WorkUnit ID for a given XID. This is
// a private method because the caller MUST provide their own transaction to avoid a race condition,
// so only the storage layer should call this method.
func (s *Storage) fetchNextWorkUnitIDTransaction(ctx context.Context, xid int64, txn spannerTransaction) (int64, error) {
	logging.Info(ctx, "Fetch next WorkUnit ID from storage")

	stmt := spanner.Statement{
		SQL:    nextWorkUnitIDSQL.String(),
		Params: map[string]any{"bit_reversed_xid": reverseBits(xid)},
	}

	iter := txn.Query(ctx, stmt)
	defer iter.Stop()
	row, err := iter.Next()
	if err != nil {
		logging.Error(ctx, "Failed to read WorkUnit row", "error", err)
		return 0, fmt.Errorf("failed to read WorkUnit row: %w", err)
	}

	var workUnitID int64
	if err := row.Column(0, &workUnitID); err != nil {
		logging.Error(ctx, "Failed to get WorkUnit ID from row", "error", err)
		return 0, fmt.Errorf("failed to get WorkUnit ID from row: %w", err)
	}

	logging.Info(ctx, "Fetched next WorkUnit ID from storage", "workUnitID", workUnitID+1)
	return workUnitID + 1, nil
}
