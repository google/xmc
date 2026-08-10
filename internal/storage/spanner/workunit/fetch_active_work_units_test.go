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

package workunit

import (
	"context"
	"testing"
	"time"
)

func TestFetchActiveWorkUnitsByQueues_EmptyQueue(t *testing.T) {
	repo := &Repository{}
	workUnits, _, err := repo.FetchActiveWorkUnitsByQueues(context.Background(), nil, 10, "")
	if err != nil {
		t.Errorf("FetchActiveWorkUnitsByQueues(nil) expected nil error, got %v", err)
	}
	if len(workUnits) != 0 {
		t.Errorf("FetchActiveWorkUnitsByQueues(nil) expected empty workUnits slice, got %d", len(workUnits))
	}
}

func TestFetchActiveWorkUnitsByQueuesAt_EmptyQueuesList(t *testing.T) {
	repo := &Repository{}
	workUnits, _, err := repo.FetchActiveWorkUnitsByQueuesAt(context.Background(), []string{}, 10, "", time.Now())
	if err != nil {
		t.Errorf("FetchActiveWorkUnitsByQueuesAt(empty) expected nil error, got %v", err)
	}
	if len(workUnits) != 0 {
		t.Errorf("FetchActiveWorkUnitsByQueuesAt(empty) expected empty workUnits slice, got %d", len(workUnits))
	}
}
