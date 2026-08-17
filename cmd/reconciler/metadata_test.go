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
package main

import (
	"context"
	"testing"
)

func TestGetGceMetadata_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := getGceMetadata(ctx)
	if err == nil {
		t.Error("getGceMetadata() with cancelled context expected error, got nil")
	}
}

func TestGetGceMetadata_NonGCEEnvironment(t *testing.T) {
	// In standard unit test environments, GCE metadata server is unreachable.
	_, err := getGceMetadata(context.Background())
	if err == nil {
		t.Log("getGceMetadata succeeded in GCE environment")
	} else {
		t.Logf("getGceMetadata properly returned error in non-GCE environment: %v", err)
	}
}
