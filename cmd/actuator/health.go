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
	"fmt"
	"log/slog"
	"net/http"

	"golang/go_sync/errgroup/errgroup"
	"golang/k8s_io/client_go/v/v0_23/kubernetes/kubernetes"
	"xmanager_cloud/common/health"
	"xmanager_cloud/task_queue_redis/redisclient"
)

// k8sHealthClient defines the Kubernetes client interface required for health checking.
type k8sHealthClient interface {
	Clientset() kubernetes.Interface
}

// activeHealthCheck performs active checks against Kubernetes, Spanner, and Redis.
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

// livenessCheck performs a simple liveness probe.
func livenessCheck() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}
}
