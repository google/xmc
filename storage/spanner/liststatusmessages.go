package spanner

import (
	"context"
	"time"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

// ListStatusMessages retrieves a list of status messages for an experiment or work unit.
// It returns a hardcoded list of messages for now as a stub.
func (s *Storage) ListStatusMessages(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*essmsgpb.StructuredMessage, string, error) {

	return []*essmsgpb.StructuredMessage{
		{
			UserFacingText: "Dummy status message 1 from Spanner stub",
			Severity:       essmsgpb.StructuredMessage_INFO,
			Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
		},
		{
			UserFacingText: "Dummy status message 2 from Spanner stub",
			Severity:       essmsgpb.StructuredMessage_WARNING,
			Source:         essmsgpb.StructuredMessage_APPLICATION,
		},
	}, "", nil
}
