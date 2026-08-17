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

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"golang/go_sync/errgroup/errgroup"
	ctrl "golang/sigs_k8s_io/controller_runtime/v/v0_11/controllerruntime"
	"xmanager_cloud/common/health"
	"xmanager_cloud/kubernetes/actuator/k8s_client/k8sclient"
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

func registerHealthChecks(mgr ctrl.Manager, k8sClient *k8sclient.Client, spClient *spanner.Client) error {
	if mgr == nil {
		return errors.New("manager cannot be nil")
	}
	var spHealth health.SpannerHealthClient
	if spClient != nil {
		spHealth = health.NewSpannerHealthClient(spClient)
	}

	if err := mgr.AddHealthzCheck("healthz", func(req *http.Request) error { return nil }); err != nil {
		return fmt.Errorf("unable to set up health check: %w", err)
	}
	if err := mgr.AddReadyzCheck("readyz", activeHealthChecker(k8sClient, spHealth)); err != nil {
		return fmt.Errorf("unable to set up ready check: %w", err)
	}
	return nil
}
