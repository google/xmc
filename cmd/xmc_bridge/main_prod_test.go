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

//go:build !mockauth

package main

import (
	"context"
	"errors"
	"testing"

	"xmanager_cloud/pkg/auth/oauth2"
)

func TestRunBridgeFailsClosedOnMockAuthWithoutMockAuthTag(t *testing.T) {
	t.Setenv("MOCK_AUTH", "true")
	t.Setenv("DISABLE_AUTHENTICATION", "false")
	err := RunBridge(context.Background(), &Config{CMSAddress: "localhost:0"})
	if !errors.Is(err, oauth2.ErrDevAuthNotCompiledIn) {
		t.Errorf("RunBridge() error = %v, want %v", err, oauth2.ErrDevAuthNotCompiledIn)
	}
}
