// Copyright 2026 Google LLC
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
	"fmt"

	"github.com/hibiken/asynq"
)

// AsynqServerConfig holds configuration parameters for the Asynq worker server.
type AsynqServerConfig struct {
	RedisAddr     string
	RedisPassword string
	Concurrency   int
	Queues        map[string]int
}

// DefaultQueueWeights provides the default weighted queue distribution.
func DefaultQueueWeights() map[string]int {
	return map[string]int{
		"critical": 6,
		"default":  3,
		"low":      1,
	}
}

// ServerRunner abstracts the underlying Asynq server operations for testing.
type ServerRunner interface {
	Start(handler asynq.Handler) error
	Stop()
	Shutdown()
	Ping() error
}

// AsynqWorkerServer encapsulates an Asynq worker pool.
type AsynqWorkerServer struct {
	runner  ServerRunner
	handler asynq.Handler
}

// NewAsynqWorkerServer creates an AsynqWorkerServer configured for the actuator.
func NewAsynqWorkerServer(cfg AsynqServerConfig, handler asynq.Handler) (*AsynqWorkerServer, error) {
	if cfg.RedisAddr == "" {
		return nil, fmt.Errorf("redis address cannot be empty")
	}
	if handler == nil {
		return nil, fmt.Errorf("handler cannot be nil")
	}

	concurrency := cfg.Concurrency
	if concurrency <= 0 {
		concurrency = 10
	}

	queues := cfg.Queues
	if len(queues) == 0 {
		queues = DefaultQueueWeights()
	}

	asynqCfg := asynq.Config{
		Concurrency: concurrency,
		Queues:      queues,
	}

	server := asynq.NewServer(
		asynq.RedisClientOpt{
			Addr:     cfg.RedisAddr,
			Password: cfg.RedisPassword,
		},
		asynqCfg,
	)

	return &AsynqWorkerServer{
		runner:  server,
		handler: handler,
	}, nil
}

// NewAsynqWorkerServerWithRunner creates an AsynqWorkerServer with an injected ServerRunner.
func NewAsynqWorkerServerWithRunner(runner ServerRunner, handler asynq.Handler) *AsynqWorkerServer {
	return &AsynqWorkerServer{
		runner:  runner,
		handler: handler,
	}
}

// Start runs the worker server in a background goroutine.
func (s *AsynqWorkerServer) Start() error {
	if s.runner == nil {
		return fmt.Errorf("server runner is nil")
	}
	return s.runner.Start(s.handler)
}

// Stop stops the server from accepting new tasks.
func (s *AsynqWorkerServer) Stop() {
	if s.runner != nil {
		s.runner.Stop()
	}
}

// Shutdown gracefully shuts down the server runner.
func (s *AsynqWorkerServer) Shutdown() {
	if s.runner != nil {
		s.runner.Shutdown()
	}
}

// Ping verifies connectivity to Redis to satisfy readiness health checks.
func (s *AsynqWorkerServer) Ping(ctx context.Context) error {
	if s.runner == nil {
		return fmt.Errorf("server runner is nil")
	}
	return s.runner.Ping()
}
