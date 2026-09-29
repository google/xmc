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
	"context"
	"fmt"
	"log/slog"

	"github.com/go-logr/logr"
	"go/google"
	ctrl "sigs.k8s.io/controller-runtime"
	"xmanager_cloud/logging/logging"
)

func main() {
	google.Init()
	ctx := ctrl.SetupSignalHandler()

	cfg := Load()
	logger := logging.NewGCPLogger(slog.LevelInfo, cfg.RuntimeConfig.QuotaProjectID)
	slog.SetDefault(logger)
	ctrl.SetLogger(logr.FromSlogHandler(logger.Handler()))
	ctx = logging.WithLogger(ctx, logger)

	if err := run(ctx, cfg); err != nil {
		logging.Fatal(ctx, "Failed to run reconciler", "error", err)
	}
}

func run(ctx context.Context, cfg Config) error {
	mgr, err := NewManager(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() {
		if err := mgr.Close(); err != nil {
			logging.Error(ctx, "Failed to close manager resources", "error", err)
		}
	}()

	k8s, spHealth := mgr.HealthCheckers()
	if err := registerHealthChecks(mgr.CtrlManager(), k8s, spHealth); err != nil {
		return fmt.Errorf("failed to register health checks: %w", err)
	}

	return mgr.Start(ctx)
}
