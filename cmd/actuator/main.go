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
// Package main implements the Actuator Server for XManager Cloud.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"go/google"
	"golang/cloud_google_com/go/compute/metadata/v/v0/metadata"
	"golang/go_sync/errgroup/errgroup"
	"golang/k8s_io/client_go/v/v0_23/kubernetes/kubernetes"
	"xmanager_cloud/common/health"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/task_queue_redis/redisclient"

	"golang/prometheus/client/prometheus/promhttp/promhttp"
	ctrl "golang/sigs_k8s_io/controller_runtime/v/v0_11/controllerruntime"
)

func main() {
	google.Init()
	ctx := ctrl.SetupSignalHandler()

	cfg := Load()

	gceMeta, err := getGceMetadata(ctx)
	if err != nil {
		logging.Fatal(ctx, "Failed to get GCE Metadata", "error", err)
	}
	projectID := gceMeta.ProjectID

	logger := logging.NewGCPLogger(slog.LevelInfo, projectID)
	ctx = logging.WithLogger(ctx, logger)

	prodServer, err := NewProd(ctx, &cfg, projectID)
	if err != nil {
		logging.Fatal(ctx, "Failed to initialize production server", "error", err)
	}
	defer func() {
		if err := prodServer.Close(); err != nil {
			logging.Warn(ctx, "Failed to close production server", "error", err)
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", activeHealthCheck(prodServer.HealthCheckers()))
	mux.Handle("/metrics", promhttp.Handler())

	addr := fmt.Sprintf(":%s", cfg.Port)
	logging.Info(ctx, "Actuator server listening on", "addr", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		logging.Fatal(ctx, "Server failed to start", "error", err)
	}
}

// GceMetadata holds cluster metadata fetched from GCE metadata server.
type GceMetadata struct {
	ProjectID   string
	Location    string
	ClusterName string
}

func getGceMetadata(ctx context.Context) (GceMetadata, error) {
	logging.Debug(ctx, "Fetching GCE Metadata...")
	projectID, err := metadata.ProjectIDWithContext(ctx)
	if err != nil {
		return GceMetadata{}, fmt.Errorf("failed to get Project ID from metadata: %v", err)
	}
	clusterName, err := metadata.InstanceAttributeValueWithContext(ctx, "cluster-name")
	if err != nil {
		return GceMetadata{}, fmt.Errorf("failed to get cluster-name from metadata: %v", err)
	}
	location, err := metadata.InstanceAttributeValueWithContext(ctx, "cluster-location")
	if err != nil {
		return GceMetadata{}, fmt.Errorf("failed to get cluster-location from metadata: %v", err)
	}
	logging.Debug(ctx, "Successfully fetched GCE Metadata", "project_id", projectID, "location", location, "cluster_name", clusterName)
	return GceMetadata{
		ProjectID:   projectID,
		Location:    location,
		ClusterName: clusterName,
	}, nil
}

type k8sHealthClient interface {
	Clientset() kubernetes.Interface
}

func activeHealthCheck(k8s k8sHealthClient, spClient health.SpannerHealthClient, redis redisclient.Consumer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		eg, ctx := errgroup.WithContext(ctx)

		// 1. Kubernetes check
		eg.Go(func() error {
			if k8s == nil || k8s.Clientset() == nil {
				slog.Error("Kubernetes healthcheck failed: client is nil")
				return fmt.Errorf("k8s client is nil")
			}
			err := health.CheckKubernetes(k8s.Clientset())
			if err != nil {
				slog.Error("Kubernetes healthcheck failed", "error", err)
			}
			return err
		})

		// 2. Spanner check
		eg.Go(func() error {
			if spClient == nil {
				return nil
			}
			err := health.CheckSpannerESS(ctx, spClient)
			if err != nil {
				slog.Error("Spanner healthcheck failed", "error", err)
			}
			return err
		})

		// 3. Redis check
		eg.Go(func() error {
			if redis == nil {
				slog.Error("Redis healthcheck failed: client is nil")
				return fmt.Errorf("redis client is nil")
			}
			if err := redis.Ping(ctx); err != nil {
				slog.Error("Redis healthcheck failed", "error", err)
				return fmt.Errorf("redis reachability: %w", err)
			}
			return nil
		})

		if err := eg.Wait(); err != nil {
			slog.Error("Overall healthcheck failed", "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}
}
