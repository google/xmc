// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package main implements the xmc-bridge daemon for XManager on Cloud (XMC).
//
// The xmc-bridge controller continuously watches and reconciles Component custom
// resources (xmc.dev/v1alpha1) within a target Kubernetes namespace, updating
// runtime conditions, synchronizing intent with the Component Manager Service (CMS),
// and managing deletion protection finalizers.
//
// # Command Line Configuration
//
// The daemon accepts standard CLI configuration flags:
//   - --cms-address: gRPC endpoint of the Component Manager Service (default: "localhost:50051").
//   - --namespace: Kubernetes namespace to watch for Component CRs (default: "xmc").
//   - --probe-interval: Interval duration between dynamic reconciliation sweeps (default: 5s).
//   - --probe-timeout: Timeout duration for individual component probe and reconcile passes (default: 2s).
//   - --kubeconfig: Optional explicit path to kubeconfig file (defaults to in-cluster config or KUBECONFIG env).
package main
