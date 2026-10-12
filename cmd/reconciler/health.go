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
	"errors"
	"fmt"
	"net/http"

	"golang.org/x/sync/errgroup"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"xmanager_cloud/cmd/actuator/k8s_client"
	"xmanager_cloud/common/health"
)

func activeHealthChecker(k8s *k8sclient.Client, spClient health.SpannerHealthClient) func(req *http.Request) error {
	return func(req *http.Request) error {
		ctx := req.Context()
		eg, ctx := errgroup.WithContext(ctx)

		// 1. Kubernetes check
		if k8s != nil && k8s.Clientset() != nil {
			eg.Go(func() error {
				return health.CheckKubernetes(k8s.Clientset())
			})
		}

		// 2. Spanner check
		if spClient != nil {
			eg.Go(func() error {
				return health.CheckSpannerESS(ctx, spClient)
			})
		}

		return eg.Wait()
	}
}

func registerHealthChecks(mgr ctrl.Manager, k8sClient *k8sclient.Client, spHealth health.SpannerHealthClient) error {
	if mgr == nil {
		return errors.New("manager cannot be nil")
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return fmt.Errorf("unable to set up health check: %w", err)
	}
	if err := mgr.AddReadyzCheck("readyz", activeHealthChecker(k8sClient, spHealth)); err != nil {
		return fmt.Errorf("unable to set up ready check: %w", err)
	}
	return nil
}
