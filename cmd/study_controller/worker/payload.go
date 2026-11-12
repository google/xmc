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

package worker

import (
	"errors"
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"
	taskpb "xmanager_cloud/api/protos/task_queue"
	"xmanager_cloud/internal/validator"
)

// payloadUnmarshaler ignores unknown fields, so a Study Controller keeps consuming ticks while a
// newer Experiment State Server that adds payload fields rolls out.
var payloadUnmarshaler = protojson.UnmarshalOptions{DiscardUnknown: true}

// parseReconcilePayload decodes a protojson StudyReconcilePayload and returns the XID of the
// experiment it names.
func parseReconcilePayload(data string) (int64, error) {
	if data == "" {
		return 0, errors.New("study reconcile payload is empty")
	}
	payload := &taskpb.StudyReconcilePayload{}
	if err := payloadUnmarshaler.Unmarshal([]byte(data), payload); err != nil {
		return 0, fmt.Errorf("failed to decode study reconcile payload: %w", err)
	}
	xid, err := validator.ValidateExperimentName(payload.GetExperiment())
	if err != nil {
		return 0, fmt.Errorf("invalid experiment in study reconcile payload: %w", err)
	}
	return xid, nil
}
