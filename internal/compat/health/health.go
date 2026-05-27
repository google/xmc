// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package health_go_proto

import "google.golang.org/grpc/health/grpc_health_v1"

type HealthCheckRequest = grpc_health_v1.HealthCheckRequest
type HealthCheckResponse = grpc_health_v1.HealthCheckResponse
type HealthCheckResponse_ServingStatus = grpc_health_v1.HealthCheckResponse_ServingStatus

const (
	HealthCheckResponse_UNKNOWN     = grpc_health_v1.HealthCheckResponse_UNKNOWN
	HealthCheckResponse_SERVING     = grpc_health_v1.HealthCheckResponse_SERVING
	HealthCheckResponse_NOT_SERVING = grpc_health_v1.HealthCheckResponse_NOT_SERVING
)
