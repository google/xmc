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

package data

import (
	"fmt"
	"time"

	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	"xmanager_cloud/internal/validator"
)

// StatusMessageParentIDs extracts the experiment ID and the work unit ID from a status message
// parent resource name. The work unit ID is 0 for experiment-level messages.
func StatusMessageParentIDs(parent string) (xid, wid int64, err error) {
	xid, wid, err = validator.ValidateStatusMessageParent(parent)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to validate status message parent %q: %w", parent, err)
	}
	return xid, wid, nil
}

// StatusMessageCreateTime returns the create time to persist for a status message, defaulting
// to now when the message has no create time.
func StatusMessageCreateTime(createTime *timestamppb.Timestamp, now time.Time) time.Time {
	if createTime == nil {
		return now
	}
	return createTime.AsTime()
}
