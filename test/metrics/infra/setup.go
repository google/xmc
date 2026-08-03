// Copyright 2026 Google LLC
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

// Package infra provides hermetic testing infrastructure using testcontainers-go
// for Redpanda and ClickHouse integration testing.
package infra

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	clickhousego "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/clickhouse"
	"github.com/testcontainers/testcontainers-go/modules/redpanda"
	tcnetwork "github.com/testcontainers/testcontainers-go/network"
)

const (
	defaultRedpandaImage   = "docker.redpanda.com/redpandadata/redpanda:v24.1.1"
	defaultClickHouseImage = "clickhouse/clickhouse-server:24.3-alpine"
	internalRedpandaAddr   = "redpanda:9092"
)

// Harness holds container instances and active connections for hermetic testing.
type Harness struct {
	RedpandaContainer    *redpanda.Container
	ClickHouseContainer  *clickhouse.ClickHouseContainer
	ClickHouseConn       driver.Conn
	RedpandaBrokers      []string
	ClickHouseNativeAddr string
	network              *testcontainers.DockerNetwork
	cleanups             []func()
}

// VerifyTCPConnectivity attempts a TCP connection to the specified address.
func VerifyTCPConnectivity(ctx context.Context, addr string) error {
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("TCP connection to %s failed: %w", addr, err)
	}
	_ = conn.Close()
	return nil
}

// StartRedpanda spins up a Redpanda test container and returns the container instance and broker address.
func StartRedpanda(ctx context.Context, nw *testcontainers.DockerNetwork) (*redpanda.Container, []string, error) {
	opts := []testcontainers.ContainerCustomizer{}
	if nw != nil {
		opts = append(opts, tcnetwork.WithNetwork([]string{"redpanda"}, nw))
	}

	c, err := redpanda.Run(ctx, defaultRedpandaImage, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to start redpanda container: %w", err)
	}

	broker, err := c.KafkaSeedBroker(ctx)
	if err != nil {
		terminateWithTimeout(c)
		return nil, nil, fmt.Errorf("failed to get redpanda seed broker: %w", err)
	}

	if broker == "" {
		terminateWithTimeout(c)
		return nil, nil, fmt.Errorf("no redpanda seed broker returned")
	}

	if err := VerifyTCPConnectivity(ctx, broker); err != nil {
		terminateWithTimeout(c)
		return nil, nil, fmt.Errorf("redpanda TCP verification failed: %w", err)
	}

	return c, []string{broker}, nil
}

func prepareInitScript(scriptPath string, internalBroker string) (string, func(), error) {
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		return "", nil, fmt.Errorf("failed to read init script %s: %w", scriptPath, err)
	}

	s := string(content)
	s = strings.ReplaceAll(s, "{kafka_broker_list:redpanda-0.redpanda.xmc.svc.cluster.local:9092}", internalBroker)
	s = strings.ReplaceAll(s, "{kafka_topic_list:xmc-metrics}", "xmc-metrics")
	s = strings.ReplaceAll(s, "{kafka_group_name:xmc-clickhouse-ingester}", "xmc-clickhouse-ingester")
	s = strings.ReplaceAll(s, "stream_flush_interval_ms", "kafka_flush_interval_ms")

	tmpFile, err := os.CreateTemp("", "clickhouse-init-*.sql")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create temp init script: %w", err)
	}

	if _, err := tmpFile.WriteString(s); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpFile.Name())
		return "", nil, fmt.Errorf("failed to write temp init script: %w", err)
	}
	_ = tmpFile.Close()

	if err := os.Chmod(tmpFile.Name(), 0644); err != nil {
		_ = os.Remove(tmpFile.Name())
		return "", nil, fmt.Errorf("failed to set permissions on temp init script: %w", err)
	}

	cleanup := func() {
		_ = os.Remove(tmpFile.Name())
	}
	return tmpFile.Name(), cleanup, nil
}

// StartClickHouse spins up a ClickHouse test container and establishes a native driver connection.
func StartClickHouse(ctx context.Context, nw *testcontainers.DockerNetwork, initScripts ...string) (*clickhouse.ClickHouseContainer, driver.Conn, string, []func(), error) {
	opts := []testcontainers.ContainerCustomizer{
		clickhouse.WithUsername("clickhouse"),
		clickhouse.WithPassword("clickhouse"),
		clickhouse.WithDatabase("default"),
	}

	if nw != nil {
		opts = append(opts, tcnetwork.WithNetwork([]string{"clickhouse"}, nw))
	}

	var cleanups []func()
	for _, script := range initScripts {
		if script == "" {
			continue
		}
		preparedScript, cleanup, err := prepareInitScript(script, internalRedpandaAddr)
		if err != nil {
			for _, cl := range cleanups {
				cl()
			}
			return nil, nil, "", nil, fmt.Errorf("failed to prepare init script %s: %w", script, err)
		}
		cleanups = append(cleanups, cleanup)
		opts = append(opts, clickhouse.WithInitScripts(preparedScript))
	}

	c, err := clickhouse.Run(ctx, defaultClickHouseImage, opts...)
	if err != nil {
		for _, cl := range cleanups {
			cl()
		}
		return nil, nil, "", nil, fmt.Errorf("failed to start clickhouse container: %w", err)
	}

	nativePort, err := c.MappedPort(ctx, "9000/tcp")
	if err != nil {
		terminateWithTimeout(c)
		for _, cl := range cleanups {
			cl()
		}
		return nil, nil, "", nil, fmt.Errorf("failed to get clickhouse native port: %w", err)
	}

	host, err := c.Host(ctx)
	if err != nil {
		terminateWithTimeout(c)
		for _, cl := range cleanups {
			cl()
		}
		return nil, nil, "", nil, fmt.Errorf("failed to get clickhouse host: %w", err)
	}

	addr := net.JoinHostPort(host, nativePort.Port())

	if err := VerifyTCPConnectivity(ctx, addr); err != nil {
		terminateWithTimeout(c)
		for _, cl := range cleanups {
			cl()
		}
		return nil, nil, "", nil, fmt.Errorf("clickhouse TCP verification failed: %w", err)
	}

	conn, err := clickhousego.Open(&clickhousego.Options{
		Addr: []string{addr},
		Auth: clickhousego.Auth{
			Database: c.DbName,
			Username: c.User,
			Password: c.Password,
		},
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		terminateWithTimeout(c)
		for _, cl := range cleanups {
			cl()
		}
		return nil, nil, "", nil, fmt.Errorf("failed to open clickhouse connection: %w", err)
	}

	if err := conn.Ping(ctx); err != nil {
		_ = conn.Close()
		terminateWithTimeout(c)
		for _, cl := range cleanups {
			cl()
		}
		return nil, nil, "", nil, fmt.Errorf("clickhouse ping failed: %w", err)
	}

	return c, conn, addr, cleanups, nil
}

// StartHarness initializes both Redpanda and ClickHouse containers on a shared network.
func StartHarness(ctx context.Context, schemaPath string) (*Harness, error) {
	nw, err := tcnetwork.New(ctx, tcnetwork.WithCheckDuplicate())
	if err != nil {
		return nil, fmt.Errorf("failed creating container network: %w", err)
	}

	rpContainer, brokers, err := StartRedpanda(ctx, nw)
	if err != nil {
		removeNetworkWithTimeout(nw)
		return nil, fmt.Errorf("failed starting redpanda: %w", err)
	}

	chContainer, conn, chAddr, cleanups, err := StartClickHouse(ctx, nw, schemaPath)
	if err != nil {
		terminateWithTimeout(rpContainer)
		removeNetworkWithTimeout(nw)
		return nil, fmt.Errorf("failed starting clickhouse: %w", err)
	}

	harness := &Harness{
		RedpandaContainer:    rpContainer,
		ClickHouseContainer:  chContainer,
		ClickHouseConn:       conn,
		RedpandaBrokers:      brokers,
		ClickHouseNativeAddr: chAddr,
		network:              nw,
		cleanups:             cleanups,
	}

	return harness, nil
}

// Teardown closes connections, terminates container instances, and removes the shared network.
func (h *Harness) Teardown(ctx context.Context) error {
	if h == nil {
		return nil
	}
	var errs []error

	if h.ClickHouseConn != nil {
		if err := h.ClickHouseConn.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close clickhouse connection: %w", err))
		}
	}

	if h.ClickHouseContainer != nil {
		if err := h.ClickHouseContainer.Terminate(ctx); err != nil {
			errs = append(errs, fmt.Errorf("failed to terminate clickhouse container: %w", err))
		}
	}

	if h.RedpandaContainer != nil {
		if err := h.RedpandaContainer.Terminate(ctx); err != nil {
			errs = append(errs, fmt.Errorf("failed to terminate redpanda container: %w", err))
		}
	}

	if h.network != nil {
		if err := h.network.Remove(ctx); err != nil {
			errs = append(errs, fmt.Errorf("failed to remove container network: %w", err))
		}
	}

	for _, cl := range h.cleanups {
		cl()
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func terminateWithTimeout(c testcontainers.Container) {
	if c == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = c.Terminate(ctx)
}

func removeNetworkWithTimeout(nw *testcontainers.DockerNetwork) {
	if nw == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = nw.Remove(ctx)
}
