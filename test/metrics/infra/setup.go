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
	"path/filepath"
	"strings"
	"time"

	clickhousego "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	mobyclient "github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/clickhouse"
	"github.com/testcontainers/testcontainers-go/modules/redpanda"
	tcnetwork "github.com/testcontainers/testcontainers-go/network"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
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
	ClickHouseHTTPAddr   string
	network              *testcontainers.DockerNetwork
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

// RedpandaNode encapsulates a Redpanda container instance and its broker addresses.
type RedpandaNode struct {
	Container *redpanda.Container
	Brokers   []string
}

// StartRedpanda spins up a Redpanda test container and returns the container instance and broker address.
func StartRedpanda(ctx context.Context, nw *testcontainers.DockerNetwork) (*RedpandaNode, error) {
	opts := []testcontainers.ContainerCustomizer{
		testcontainers.WithCmd("redpanda", "start", "--mode", "dev-container", "--smp", "1", "--memory", "1G", "--reserve-memory", "0M", "--overprovisioned", "--kafka-addr", "PLAINTEXT://0.0.0.0:9092", "--advertise-kafka-addr", "PLAINTEXT://redpanda:9092"),
	}
	if nw != nil {
		opts = append(opts, tcnetwork.WithNetwork([]string{"redpanda"}, nw))
	}

	c, err := redpanda.Run(ctx, defaultRedpandaImage, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to start redpanda container: %w", err)
	}

	broker, err := c.KafkaSeedBroker(ctx)
	if err != nil {
		terminateWithTimeout(c)
		return nil, fmt.Errorf("failed to get redpanda seed broker: %w", err)
	}

	if broker == "" {
		terminateWithTimeout(c)
		return nil, fmt.Errorf("no redpanda seed broker returned")
	}

	if err := VerifyTCPConnectivity(ctx, broker); err != nil {
		terminateWithTimeout(c)
		return nil, fmt.Errorf("redpanda TCP verification failed: %w", err)
	}

	return &RedpandaNode{
		Container: c,
		Brokers:   []string{broker},
	}, nil
}

// PauseContainer pauses a Redpanda container instance.
func PauseContainer(ctx context.Context, c *redpanda.Container) error {
	if c == nil {
		return errors.New("cannot pause nil container")
	}
	cli, err := testcontainers.NewDockerClientWithOpts(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()
	_, err = cli.ContainerPause(ctx, c.GetContainerID(), mobyclient.ContainerPauseOptions{})
	return err
}

// UnpauseContainer unpauses a Redpanda container instance.
func UnpauseContainer(ctx context.Context, c *redpanda.Container) error {
	if c == nil {
		return errors.New("cannot unpause nil container")
	}
	cli, err := testcontainers.NewDockerClientWithOpts(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()
	_, err = cli.ContainerUnpause(ctx, c.GetContainerID(), mobyclient.ContainerUnpauseOptions{})
	return err
}

func locateMetricsProtoFile() string {
	if srcDir := os.Getenv("TEST_SRCDIR"); srcDir != "" {
		for _, ws := range []string{os.Getenv("TEST_WORKSPACE"), "_main", "xmanager_cloud"} {
			if ws == "" {
				continue
			}
			path := filepath.Join(srcDir, ws, "api", "protos", "metrics", "metrics.proto")
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
	}
	candidates := []string{
		filepath.Join("..", "..", "api", "protos", "metrics", "metrics.proto"),
		filepath.Join("..", "api", "protos", "metrics", "metrics.proto"),
		filepath.Join("api", "protos", "metrics", "metrics.proto"),
	}
	for _, cand := range candidates {
		if abs, err := filepath.Abs(cand); err == nil {
			if _, err := os.Stat(abs); err == nil {
				return abs
			}
		}
	}
	return ""
}

// ClickHouseNode encapsulates a ClickHouse container instance, driver connection, and mapped endpoint addresses.
type ClickHouseNode struct {
	Container *clickhouse.ClickHouseContainer
	Conn      driver.Conn
	// Address is the host:port for ClickHouse's native TCP binary protocol (9000/tcp), used by Go database drivers.
	Address string
	// HTTPAddress is the host:port for ClickHouse's REST HTTP protocol (8123/tcp), used by Python/HTTP query clients.
	HTTPAddress string
}

// StartClickHouse spins up a ClickHouse test container and establishes a native driver connection.
func StartClickHouse(ctx context.Context, nw *testcontainers.DockerNetwork) (*ClickHouseNode, error) {
	opts := []testcontainers.ContainerCustomizer{
		clickhouse.WithUsername("clickhouse"),
		clickhouse.WithPassword("clickhouse"),
		clickhouse.WithDatabase("default"),
	}

	protoPath := locateMetricsProtoFile()
	if protoPath == "" {
		return nil, fmt.Errorf("metrics.proto file not found")
	}
	opts = append(opts, testcontainers.WithFiles(
		testcontainers.ContainerFile{
			HostFilePath:      protoPath,
			ContainerFilePath: "/var/lib/clickhouse/format_schemas/metrics.proto",
			FileMode:          0644,
		},
	))

	if nw != nil {
		opts = append(opts, tcnetwork.WithNetwork([]string{"clickhouse"}, nw))
	}

	c, err := clickhouse.Run(ctx, defaultClickHouseImage, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to start clickhouse container: %w", err)
	}

	nativePort, err := c.MappedPort(ctx, "9000/tcp")
	if err != nil {
		terminateWithTimeout(c)
		return nil, fmt.Errorf("failed to get clickhouse native port: %w", err)
	}

	httpPort, err := c.MappedPort(ctx, "8123/tcp")
	if err != nil {
		terminateWithTimeout(c)
		return nil, fmt.Errorf("failed to get clickhouse http port: %w", err)
	}

	host, err := c.Host(ctx)
	if err != nil {
		terminateWithTimeout(c)
		return nil, fmt.Errorf("failed to get clickhouse host: %w", err)
	}

	addr := net.JoinHostPort(host, nativePort.Port())
	httpAddr := net.JoinHostPort(host, httpPort.Port())

	if err := VerifyTCPConnectivity(ctx, addr); err != nil {
		terminateWithTimeout(c)
		return nil, fmt.Errorf("clickhouse TCP verification failed: %w", err)
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
		return nil, fmt.Errorf("failed to open clickhouse connection: %w", err)
	}

	if err := conn.Ping(ctx); err != nil {
		_ = conn.Close()
		terminateWithTimeout(c)
		return nil, fmt.Errorf("clickhouse ping failed: %w", err)
	}

	return &ClickHouseNode{
		Container:   c,
		Conn:        conn,
		Address:     addr,
		HTTPAddress: httpAddr,
	}, nil
}

func ExecuteSQLScript(ctx context.Context, conn driver.Conn, scriptPath string, internalBroker string) error {
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		return fmt.Errorf("failed to read script %s: %w", scriptPath, err)
	}

	s := string(content)
	s = strings.ReplaceAll(s, "{kafka_broker_list:redpanda-0.redpanda.xmc.svc.cluster.local:9092}", internalBroker)
	s = strings.ReplaceAll(s, "'redpanda-0.redpanda.xmc.svc.cluster.local:9092'", fmt.Sprintf("'%s'", internalBroker))
	s = strings.ReplaceAll(s, "{kafka_topic_list:xmc-metrics}", "xmc-metrics")
	s = strings.ReplaceAll(s, "{kafka_group_name:xmc-clickhouse-ingester}", "xmc-clickhouse-ingester")
	s = strings.ReplaceAll(s, "stream_flush_interval_ms", "kafka_flush_interval_ms")

	var cleanLines []string
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") {
			continue
		}
		cleanLines = append(cleanLines, line)
	}
	cleanScript := strings.Join(cleanLines, "\n")

	var statements []string
	var current strings.Builder
	inQuote := false
	for i := 0; i < len(cleanScript); i++ {
		char := cleanScript[i]
		if char == '\'' {
			inQuote = !inQuote
		}
		if char == ';' && !inQuote {
			statements = append(statements, current.String())
			current.Reset()
		} else {
			current.WriteByte(char)
		}
	}
	if current.Len() > 0 {
		statements = append(statements, current.String())
	}

	for _, stmt := range statements {
		cleanStmt := strings.TrimSpace(stmt)
		if cleanStmt == "" {
			continue
		}
		if err := conn.Exec(ctx, cleanStmt); err != nil {
			return fmt.Errorf("failed to execute SQL statement: %w\nSQL: %s", err, cleanStmt)
		}
	}
	return nil
}

// KafkaDialer returns a kgo.Opt that configures a custom dialer to route all traffic to the first broker.
// This is necessary because Redpanda advertises its internal container address (redpanda:9092) which is unreachable from the host.
func KafkaDialer(brokers []string) kgo.Opt {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	return kgo.Dialer(func(ctx context.Context, network, hostport string) (net.Conn, error) {
		if len(brokers) > 0 {
			return dialer.DialContext(ctx, network, brokers[0])
		}
		return dialer.DialContext(ctx, network, hostport)
	})
}

func createKafkaTopic(ctx context.Context, brokers []string, topic string) error {
	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		KafkaDialer(brokers),
	)
	if err != nil {
		return err
	}
	defer client.Close()

	admin := kadm.NewClient(client)
	_, err = admin.CreateTopics(ctx, 1, 1, nil, topic)
	return err
}

// StartHarness initializes both Redpanda and ClickHouse containers on a shared network.
func StartHarness(ctx context.Context, schemaPath string) (*Harness, error) {
	nw, err := tcnetwork.New(ctx, tcnetwork.WithCheckDuplicate())
	if err != nil {
		return nil, fmt.Errorf("failed creating container network: %w", err)
	}

	rpNode, err := StartRedpanda(ctx, nw)
	if err != nil {
		removeNetworkWithTimeout(nw)
		return nil, fmt.Errorf("failed starting redpanda: %w", err)
	}

	if err := createKafkaTopic(ctx, rpNode.Brokers, "xmc-metrics"); err != nil {
		terminateWithTimeout(rpNode.Container)
		removeNetworkWithTimeout(nw)
		return nil, fmt.Errorf("failed to create kafka topic: %w", err)
	}

	chNode, err := StartClickHouse(ctx, nw)
	if err != nil {
		terminateWithTimeout(rpNode.Container)
		removeNetworkWithTimeout(nw)
		return nil, fmt.Errorf("failed starting clickhouse: %w", err)
	}

	if err := ExecuteSQLScript(ctx, chNode.Conn, schemaPath, internalRedpandaAddr); err != nil {
		_ = chNode.Conn.Close()
		terminateWithTimeout(chNode.Container)
		terminateWithTimeout(rpNode.Container)
		removeNetworkWithTimeout(nw)
		return nil, fmt.Errorf("failed applying clickhouse schema: %w", err)
	}

	harness := &Harness{
		RedpandaContainer:    rpNode.Container,
		ClickHouseContainer:  chNode.Container,
		ClickHouseConn:       chNode.Conn,
		RedpandaBrokers:      rpNode.Brokers,
		ClickHouseNativeAddr: chNode.Address,
		ClickHouseHTTPAddr:   chNode.HTTPAddress,
		network:              nw,
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
