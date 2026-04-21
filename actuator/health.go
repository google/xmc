package main

import (
	"fmt"
	"net/http"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"golang/go_sync/errgroup/errgroup"
	commonhealth "xmanager_cloud/common/health"
	"xmanager_cloud/kubernetes/actuator/k8s_client/k8sclient"
	"xmanager_cloud/task_queue_redis/redisclient"
)

func activeHealthCheck(k8s *k8sclient.Client, spClient *spanner.Client, redis redisclient.Consumer, useRedisQueue bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		eg, ctx := errgroup.WithContext(ctx)

		// 1. Kubernetes check
		eg.Go(func() error {
			if k8s == nil || k8s.Clientset() == nil || k8s.Clientset().Discovery() == nil {
				return fmt.Errorf("kubernetes client is nil or uninitialized")
			}
			_, err := k8s.Clientset().Discovery().ServerVersion()
			if err != nil {
				return fmt.Errorf("kubernetes reachability: %w", err)
			}
			return nil
		})

		// 2. Spanner check
		eg.Go(func() error {
			if spClient == nil {
				return fmt.Errorf("spanner client is nil")
			}
			return commonhealth.CheckSpanner(ctx, spClient)
		})

		// 3. Redis check
		eg.Go(func() error {
			if useRedisQueue {
				if redis == nil {
					return fmt.Errorf("redis client is nil but useRedisQueue is true")
				}
				if err := redis.Ping(ctx); err != nil {
					return fmt.Errorf("redis reachability: %w", err)
				}
			}
			return nil
		})

		if err := eg.Wait(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}
}
