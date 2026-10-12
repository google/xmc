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
	"errors"
	"fmt"
	"net/http"

	"golang.org/x/sync/errgroup"
	"k8s.io/client-go/kubernetes"
	"xmanager_cloud/internal/common/health"
	"xmanager_cloud/internal/task_queue_redis/redisclient"
	"xmanager_cloud/logging/logging"
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
				return fmt.Errorf("k8s client is nil")
			}
			return health.CheckKubernetes(k8s.Clientset())
		})

		// 2. Spanner check
		eg.Go(func() error {
			if spClient == nil {
				return nil
			}
			return health.CheckSpannerESS(ctx, spClient)
		})

		// 3. Redis check
		eg.Go(func() error {
			if redis == nil {
				return fmt.Errorf("redis client is nil")
			}
			if err := redis.Ping(ctx); err != nil {
				return fmt.Errorf("redis reachability: %w", err)
			}
			return nil
		})

		if err := eg.Wait(); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(r.Context().Err(), context.Canceled) {
				logging.Info(ctx, "Healthcheck canceled by client", "error", err)
			} else {
				logging.Error(ctx, "Overall healthcheck failed", "error", err)
			}
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
