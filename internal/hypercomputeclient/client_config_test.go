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

package hypercomputeclient

import (
	"context"
	"testing"
)

func TestNewControlPlaneClientFromConfig_InvalidEnvironment(t *testing.T) {
	_, err := NewControlPlaneClientFromConfig(context.Background(), Config{
		Project:     "test-project",
		Environment: "invalid-env",
		Location:    "us-central1",
	})
	if err == nil {
		t.Fatal("NewControlPlaneClientFromConfig() error = nil, want error for invalid environment")
	}
}

func TestNewControlPlaneClientFromConfig_EmptyProject(t *testing.T) {
	_, err := NewControlPlaneClientFromConfig(context.Background(), Config{Environment: "prod"})
	if err == nil {
		t.Fatal("NewControlPlaneClientFromConfig() error = nil, want error for empty project")
	}
}
