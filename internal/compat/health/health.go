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
