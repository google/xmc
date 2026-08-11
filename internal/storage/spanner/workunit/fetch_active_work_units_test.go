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
	res, err := repo.FetchActiveWorkUnitsByQueues(context.Background(), nil, 10, "")
	if err != nil {
		t.Fatalf("FetchActiveWorkUnitsByQueues(nil) expected nil error, got %v", err)
	}
	if res == nil || len(res.WorkUnits) != 0 {
		t.Fatalf("FetchActiveWorkUnitsByQueues(nil) expected empty workUnits slice, got %v", res)
	}
}

func TestFetchActiveWorkUnitsByQueuesAt_EmptyQueuesList(t *testing.T) {
	repo := &Repository{}
	res, err := repo.FetchActiveWorkUnitsByQueuesAt(context.Background(), []string{}, 10, "", time.Now())
	if err != nil {
		t.Fatalf("FetchActiveWorkUnitsByQueuesAt(empty) expected nil error, got %v", err)
	}
	if res == nil || len(res.WorkUnits) != 0 {
		t.Fatalf("FetchActiveWorkUnitsByQueuesAt(empty) expected empty workUnits slice, got %v", res)
	}
}

func TestFetchActiveWorkUnitsByQueues_WithQueues(t *testing.T) {
	repo := &Repository{}
	res, err := repo.FetchActiveWorkUnitsByQueues(context.Background(), []string{"gpu-queue"}, 10, "")
	if err != nil {
		t.Fatalf("FetchActiveWorkUnitsByQueues(queue) expected nil error, got %v", err)
	}
	if res == nil || len(res.WorkUnits) != 2 {
		t.Fatalf("FetchActiveWorkUnitsByQueues(queue) expected 2 mock work units, got %d", len(res.WorkUnits))
	}
	wu := res.WorkUnits[0]
	if wu.GetName() != "experiments/100/workUnits/1" {
		t.Errorf("WorkUnit name = %q, want experiments/100/workUnits/1", wu.GetName())
	}
	if wu.GetNotes() != "test@example.com" {
		t.Errorf("WorkUnit notes = %q, want test@example.com", wu.GetNotes())
	}
}

func TestFetchActiveWorkUnitsByQueuesAt_CanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	repo := &Repository{}
	_, err := repo.FetchActiveWorkUnitsByQueuesAt(ctx, []string{"gpu-queue"}, 10, "", time.Now())
	if err == nil {
		t.Errorf("FetchActiveWorkUnitsByQueuesAt(canceled) expected error, got nil")
	}
}
