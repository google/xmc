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
package spanner

import (
	"context"
	"fmt"
	"time"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"xmanager_cloud/logging/logging"
	ls "xmanager_cloud/query_generator/liststatement"
	"xmanager_cloud/query_generator/normalizer/normalizer"
	"xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/storage/spanner/schema/statusmessageschema"
	"xmanager_cloud/validator/validator"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

// ListStatusMessages retrieves a list of status messages for an experiment or work unit.
func (s *Storage) ListStatusMessages(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*essmsgpb.StatusMessage, string, error) {
	logging.Debug(ctx, "ListStatusMessages", "parent", parent, "pageSize", pageSize, "filter", filter)

	xid, wid, err := validator.ValidateStatusMessageParent(parent)
	if err != nil {
		return nil, "", fmt.Errorf("invalid parent resource name %q: %w", parent, err)
	}

	bitReversedXid := reverseBits(xid)
	parentFilter := fmt.Sprintf("bit_reversed_xid = %d", bitReversedXid)
	if wid != 0 {
		parentFilter = fmt.Sprintf("%s AND work_unit_id = %d", parentFilter, wid)
	}
	if filter == "" {
		filter = parentFilter
	} else {
		filter = fmt.Sprintf("%s AND (%s)", parentFilter, filter)
	}

	normalizedFilter, err := normalizer.Normalize(filter, s.statusMessagesQueryGen.Tables()[0])
	if err != nil {
		return nil, "", fmt.Errorf("failed to normalize filter: %w", err)
	}

	listParams := ls.ListParams{
		PageSize:            pageSize,
		Filter:              normalizedFilter,
		OrderBy:             orderBy,
		QueryGen:            s.statusMessagesQueryGen,
		DefaultSelectFields: statusmessageschema.StatusMessagesSelectFields,
		ValidOrderByFields:  statusmessageschema.StatusMessagesOrderByFields,
	}

	if err := listParams.DecodePageToken(pageToken); err != nil {
		return nil, "", err
	}

	stmt, err := ls.ListStatement(ctx, listParams)
	if err != nil {
		return nil, "", fmt.Errorf("failed to generate list statement: %w", err)
	}

	iter := s.client.Single().WithTimestampBound(spanner.ExactStaleness(staleness)).Query(ctx, *stmt)
	defer iter.Stop()

	var messages []*essmsgpb.StatusMessage
	if err := iter.Do(func(r *spanner.Row) error {
		msg, err := spannerdata.StatusMessageRowToProto(r)
		if err != nil {
			return err
		}
		messages = append(messages, msg)
		return nil
	}); err != nil {
		return nil, "", fmt.Errorf("failed to read status messages: %w", err)
	}

	pagedMessages, nextPageToken, err := ls.ApplyPagination(messages, pageSize, extractStatusMessageCursor)
	if err != nil {
		return nil, "", fmt.Errorf("failed to paginate status messages: %w", err)
	}

	return pagedMessages, nextPageToken, nil
}

func extractStatusMessageCursor(msg *essmsgpb.StatusMessage) (map[string]any, error) {
	xid, wid, messageID, err := validator.ValidateStatusMessageName(msg.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to validate status message name %q: %w", msg.GetName(), err)
	}

	cursor := map[string]any{
		"bit_reversed_xid": reverseBits(xid),
		"message_id":       messageID,
		"create_time":      msg.GetContent().GetCreateTime().AsTime(),
	}

	if wid != 0 {
		cursor["work_unit_id"] = wid
	}

	return cursor, nil
}
