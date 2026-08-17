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
// Package main implements the Reconciler for XManager Cloud.
package main

import (
	"log/slog"

	"go/google"
	ctrl "golang/sigs_k8s_io/controller_runtime/v/v0_11/controllerruntime"
	"xmanager_cloud/logging/logging"
)

func main() {
	google.Init()
	ctx := ctrl.SetupSignalHandler()

	cfg := Load()
	logger := logging.NewGCPLogger(slog.LevelInfo, cfg.RuntimeConfig.QuotaProjectID)
	ctx = logging.WithLogger(ctx, logger)

	mgr, err := NewManager(ctx, cfg)
	if err != nil {
		logging.Fatal(ctx, "Failed to initialize manager", "error", err)
	}
	defer func() {
		if err := mgr.Close(); err != nil {
			logging.Error(ctx, "Failed to close manager resources", "error", err)
		}
	}()

	if err := mgr.Start(ctx); err != nil {
		logging.Fatal(ctx, "Failed to start manager", "error", err)
	}
}
