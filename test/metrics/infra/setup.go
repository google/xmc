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
)

const (
	defaultRedpandaImage   = "docker.redpanda.com/redpandadata/redpanda:v24.1.1"
	defaultClickHouseImage = "clickhouse/clickhouse-server:24.3-alpine"
)

// Harness holds container instances and active connections for hermetic testing.
type Harness struct {
	RedpandaContainer    *redpanda.Container
	ClickHouseContainer  *clickhouse.ClickHouseContainer
	ClickHouseConn       driver.Conn
	RedpandaBrokers      []string
	ClickHouseNativeAddr string
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
func StartRedpanda(ctx context.Context) (*redpanda.Container, []string, error) {
	c, err := redpanda.Run(ctx, defaultRedpandaImage)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to start redpanda container: %w", err)
	}

	broker, err := c.KafkaSeedBroker(ctx)
	if err != nil {
		_ = testcontainers.TerminateContainer(c)
		return nil, nil, fmt.Errorf("failed to get redpanda seed broker: %w", err)
	}

	if broker == "" {
		_ = testcontainers.TerminateContainer(c)
		return nil, nil, fmt.Errorf("no redpanda seed broker returned")
	}

	if err := VerifyTCPConnectivity(ctx, broker); err != nil {
		_ = testcontainers.TerminateContainer(c)
		return nil, nil, fmt.Errorf("redpanda TCP verification failed: %w", err)
	}

	return c, []string{broker}, nil
}

// StartClickHouse spins up a ClickHouse test container and establishes a native driver connection.
func StartClickHouse(ctx context.Context) (*clickhouse.ClickHouseContainer, driver.Conn, string, error) {
	c, err := clickhouse.Run(
		ctx,
		defaultClickHouseImage,
		clickhouse.WithUsername("clickhouse"),
		clickhouse.WithPassword("clickhouse"),
		clickhouse.WithDatabase("default"),
	)
	if err != nil {
		return nil, nil, "", fmt.Errorf("failed to start clickhouse container: %w", err)
	}

	nativePort, err := c.MappedPort(ctx, "9000/tcp")
	if err != nil {
		_ = testcontainers.TerminateContainer(c)
		return nil, nil, "", fmt.Errorf("failed to get clickhouse native port: %w", err)
	}

	host, err := c.Host(ctx)
	if err != nil {
		_ = testcontainers.TerminateContainer(c)
		return nil, nil, "", fmt.Errorf("failed to get clickhouse host: %w", err)
	}

	addr := net.JoinHostPort(host, nativePort.Port())

	if err := VerifyTCPConnectivity(ctx, addr); err != nil {
		_ = testcontainers.TerminateContainer(c)
		return nil, nil, "", fmt.Errorf("clickhouse TCP verification failed: %w", err)
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
		_ = testcontainers.TerminateContainer(c)
		return nil, nil, "", fmt.Errorf("failed to open clickhouse connection: %w", err)
	}

	if err := conn.Ping(ctx); err != nil {
		_ = conn.Close()
		_ = testcontainers.TerminateContainer(c)
		return nil, nil, "", fmt.Errorf("clickhouse ping failed: %w", err)
	}

	return c, conn, addr, nil
}

func cleanSQLStatement(stmt string) string {
	var lines []string
	for _, line := range strings.Split(stmt, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func isEscaped(runes []rune, i int) bool {
	return i > 0 && runes[i-1] == '\\'
}

// splitSQLStatements splits SQL content by semicolons, ignoring semicolons inside quotes or comments.
func splitSQLStatements(content string) []string {
	var statements []string
	var current strings.Builder
	inSingleQuote := false
	inDoubleQuote := false
	inLineComment := false
	inBlockComment := false

	runes := []rune(content)
	for i := 0; i < len(runes); i++ {
		r := runes[i]

		if inLineComment {
			if r == '\n' {
				inLineComment = false
			}
			current.WriteRune(r)
			continue
		}
		if inBlockComment {
			if r == '*' && i+1 < len(runes) && runes[i+1] == '/' {
				inBlockComment = false
				current.WriteRune(r)
				current.WriteRune('/')
				i++
			} else {
				current.WriteRune(r)
			}
			continue
		}
		if inSingleQuote {
			if r == '\'' && !isEscaped(runes, i) {
				inSingleQuote = false
			}
			current.WriteRune(r)
			continue
		}
		if inDoubleQuote {
			if r == '"' && !isEscaped(runes, i) {
				inDoubleQuote = false
			}
			current.WriteRune(r)
			continue
		}

		if r == '-' && i+1 < len(runes) && runes[i+1] == '-' {
			inLineComment = true
			current.WriteRune(r)
			continue
		}
		if r == '/' && i+1 < len(runes) && runes[i+1] == '*' {
			inBlockComment = true
			current.WriteRune(r)
			continue
		}

		if r == '\'' {
			inSingleQuote = true
			current.WriteRune(r)
			continue
		}
		if r == '"' {
			inDoubleQuote = true
			current.WriteRune(r)
			continue
		}

		if r == ';' {
			statements = append(statements, current.String())
			current.Reset()
			continue
		}

		current.WriteRune(r)
	}
	if current.Len() > 0 {
		statements = append(statements, current.String())
	}
	return statements
}

// ApplyClickHouseSchema reads and executes SQL statements from a file against ClickHouse.
func ApplyClickHouseSchema(ctx context.Context, conn driver.Conn, schemaPath string) error {
	content, err := os.ReadFile(schemaPath)
	if err != nil {
		return fmt.Errorf("failed to read schema file %s: %w", schemaPath, err)
	}

	statements := splitSQLStatements(string(content))
	for _, rawStmt := range statements {
		stmt := cleanSQLStatement(rawStmt)
		if stmt == "" {
			continue
		}
		if err := conn.Exec(ctx, stmt); err != nil {
			// Skip errors for Kafka engine virtual queues if Kafka/proto schemas are optional in basic tests
			if strings.Contains(stmt, "ENGINE = Kafka") || strings.Contains(stmt, "xmc_metrics_mv") {
				fmt.Fprintf(os.Stderr, "Warning: skipping error for statement %q: %v\n", stmt, err)
				continue
			}
			return fmt.Errorf("failed executing schema statement %q: %w", stmt, err)
		}
	}
	return nil
}

// StartHarness initializes both Redpanda and ClickHouse containers and applies the DDL schema if provided.
func StartHarness(ctx context.Context, schemaPath string) (*Harness, error) {
	rpContainer, brokers, err := StartRedpanda(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed starting redpanda: %w", err)
	}

	chContainer, conn, chAddr, err := StartClickHouse(ctx)
	if err != nil {
		_ = testcontainers.TerminateContainer(rpContainer)
		return nil, fmt.Errorf("failed starting clickhouse: %w", err)
	}

	harness := &Harness{
		RedpandaContainer:    rpContainer,
		ClickHouseContainer:  chContainer,
		ClickHouseConn:       conn,
		RedpandaBrokers:      brokers,
		ClickHouseNativeAddr: chAddr,
	}

	if schemaPath != "" {
		if err := ApplyClickHouseSchema(ctx, conn, schemaPath); err != nil {
			_ = harness.Teardown(ctx)
			return nil, fmt.Errorf("failed applying schema: %w", err)
		}
	}

	return harness, nil
}

// Teardown closes connections and terminates container instances.
func (h *Harness) Teardown(ctx context.Context) error {
	if h == nil {
		return nil
	}
	var errs []string

	if h.ClickHouseConn != nil {
		if err := h.ClickHouseConn.Close(); err != nil {
			errs = append(errs, fmt.Sprintf("failed to close clickhouse connection: %v", err))
		}
	}

	if h.ClickHouseContainer != nil {
		if err := testcontainers.TerminateContainer(h.ClickHouseContainer); err != nil {
			errs = append(errs, fmt.Sprintf("failed to terminate clickhouse container: %v", err))
		}
	}

	if h.RedpandaContainer != nil {
		if err := testcontainers.TerminateContainer(h.RedpandaContainer); err != nil {
			errs = append(errs, fmt.Sprintf("failed to terminate redpanda container: %v", err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("teardown errors: %s", strings.Join(errs, "; "))
	}
	return nil
}
