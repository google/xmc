package main

import (
	"context"
	"fmt"
	"net/http"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"golang/go_sync/errgroup/errgroup"
	commonhealth "xmanager_cloud/common/health"
	xidgrpcpb "xmanager_cloud/xid_service/proto/api_go_grpc_proto"
)

// TaskQueueClient is an interface for the task queue client.
type TaskQueueClient interface {
	PingRedis(ctx context.Context) error
}

// activeHealthCheck performs active checks against Spanner, XID Service, and Redis.
func activeHealthCheck(spClient *spanner.Client, xidClient xidgrpcpb.XidServiceClient, tqClient TaskQueueClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		eg, ctx := errgroup.WithContext(ctx)

		// 1. Spanner check
		eg.Go(func() error {
			if spClient == nil {
				return fmt.Errorf("spanner client is nil")
			}
			return commonhealth.CheckSpanner(ctx, spClient)
		})

		// 2. XID Service check
		eg.Go(func() error {
			if xidClient == nil {
				return fmt.Errorf("xid client is nil")
			}
			return nil
		})

		// 3. Redis check
		eg.Go(func() error {
			if tqClient == nil {
				return fmt.Errorf("task queue client is nil")
			}
			return tqClient.PingRedis(ctx)
		})

		if err := eg.Wait(); err != nil {
			http.Error(w, fmt.Sprintf("Unhealthy: %v", err), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}
}
