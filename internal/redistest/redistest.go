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

// Package redistest manages disposable Redis containers for integration tests (tags = ["local"]).
// RunMain explicitly terminates the container because CI sets TESTCONTAINERS_RYUK_DISABLED=true.
package redistest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	redisImage       = "redis:6-alpine"
	redisPort        = "6379/tcp"
	startTimeout     = 2 * time.Minute
	terminateTimeout = 15 * time.Second
)

// RunMain starts a redis:6-alpine container, stores host:port in *addr, runs m, and terminates it.
func RunMain(m *testing.M, addr *string) int {
	return runSuite(m.Run, addr, startRedisContainer)
}

func runSuite(run func() int, addr *string, start func(context.Context) (testcontainers.Container, string, error)) int {
	startCtx, cancel := context.WithTimeout(context.Background(), startTimeout)
	c, endpoint, err := start(startCtx)
	cancel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "redistest: %v\n", err)
		return 1
	}
	defer func() { _ = terminateContainer(c) }()
	*addr = endpoint
	return run()
}

func startRedisContainer(ctx context.Context) (testcontainers.Container, string, error) {
	req := testcontainers.ContainerRequest{Image: redisImage, ExposedPorts: []string{redisPort}, WaitingFor: wait.ForListeningPort(redisPort)}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	if err != nil {
		return nil, "", errors.Join(fmt.Errorf("failed to start %s: %w", redisImage, err), terminateContainer(c))
	}
	endpoint, err := c.PortEndpoint(ctx, redisPort, "")
	if err != nil {
		return nil, "", errors.Join(fmt.Errorf("failed to resolve %s endpoint: %w", redisImage, err), terminateContainer(c))
	}
	return c, endpoint, nil
}

func terminateContainer(c testcontainers.Container) error {
	if c == nil {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), terminateTimeout)
	defer cancel()
	return testcontainers.TerminateContainer(c, testcontainers.StopContext(cleanupCtx))
}
