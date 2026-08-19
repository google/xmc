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

package dataengine

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestExtractSeedXIDs(t *testing.T) {
	tempDir := t.TempDir()
	seedPath := filepath.Join(tempDir, "metrics_seed.json")

	content := `[
		{"xid": 9001, "metric_name": "train/loss", "metric_value": 1.5, "step": 0},
		{"xid": 9001, "metric_name": "train/accuracy", "metric_value": 0.8, "step": 0},
		{"xid": 234911122, "metric_name": "val/loss", "metric_value": 1.2, "step": 0},
		{"xid": 234911123, "metric_name": "val/loss", "metric_value": 0.9, "step": 0},
		{"xid": 234911122, "metric_name": "val/loss", "metric_value": 1.1, "step": 50}
	]`
	if err := os.WriteFile(seedPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test seed file: %v", err)
	}

	got, err := ExtractSeedXIDs(seedPath)
	if err != nil {
		t.Fatalf("ExtractSeedXIDs failed: %v", err)
	}

	want := []int64{9001, 234911122, 234911123}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExtractSeedXIDs = %v, want %v", got, want)
	}
}

func TestExtractSeedXIDs_NotFound(t *testing.T) {
	_, err := ExtractSeedXIDs("non_existent_file.json")
	if err == nil {
		t.Errorf("ExtractSeedXIDs for non-existent file should return error, got nil")
	}
}
