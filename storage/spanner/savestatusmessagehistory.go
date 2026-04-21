package spanner

import (
	"context"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"golang/protobuf/v2/proto/proto"
	"xmanager_cloud/logging/logging"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

func (s *Storage) saveStatusMessageHistoryIfChanged(ctx context.Context, xid int64, currentMsg, newMsg *essmsgpb.StructuredMessage, wid *int64, txn *spanner.ReadWriteTransaction) {
	if !s.statusMessageHistoryEnabled {
		return
	}

	if newMsg == nil {
		return
	}

	if currentMsg != nil {
		currentClone := proto.Clone(currentMsg).(*essmsgpb.StructuredMessage)
		newClone := proto.Clone(newMsg).(*essmsgpb.StructuredMessage)

		currentClone.CreateTime = nil
		newClone.CreateTime = nil

		if proto.Equal(currentClone, newClone) {
			return
		}
	}

	s.saveStatusMessageHistory(ctx, xid, wid, newMsg, txn)
}

func (s *Storage) saveStatusMessageHistory(ctx context.Context, xid int64, wid *int64, msg *essmsgpb.StructuredMessage, txn *spanner.ReadWriteTransaction) {
	logging.Info(ctx, "[STUB] Save status message to history", "xid", xid, "wid", wid, "user_facing_text", msg.GetUserFacingText())
}
