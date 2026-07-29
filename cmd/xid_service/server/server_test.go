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
package server

import (
	"context"
	"testing"
	"time"

	"golang/google_api/cloudidentity/v1beta1/cloudidentity"
	"xmanager_cloud/auth/auth"
	storagetesting "xmanager_cloud/internal/storage/testing"
	"xmanager_cloud/xid_service/config"
)

func createTestAuthorizer(ctx context.Context, t *testing.T) *auth.Authorizer {
	t.Helper()
	idService := &cloudidentity.Service{}
	authorizer, err := auth.NewAuthorizer(ctx, idService, 10*time.Minute, 1*time.Minute, []string{"test-audience"}, auth.DefaultIAPIssuer, true, nil)
	if err != nil {
		t.Fatalf("Failed to create mock authorizer: %v", err)
	}
	return authorizer
}

func createXidServer(ctx context.Context, t *testing.T) *XIDServer {
	t.Helper()
	fakeStorage := storagetesting.NewFakeStorage()
	cfg := config.Config{}
	return NewXIDServer(cfg, createTestAuthorizer(ctx, t), fakeStorage)
}
