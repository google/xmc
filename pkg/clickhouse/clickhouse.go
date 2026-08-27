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

// Package clickhouse provides utilities to configure and connect to ClickHouse.
package clickhouse

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// GetOptions loads configuration from environment variables and returns clickhouse.Options.
func GetOptions() (*clickhouse.Options, error) {
	if dsn := os.Getenv("CLICKHOUSE_DSN"); dsn != "" {
		opts, err := clickhouse.ParseDSN(dsn)
		if err != nil {
			return nil, fmt.Errorf("failed to parse CLICKHOUSE_DSN: %w", err)
		}
		return opts, nil
	}

	host := os.Getenv("CLICKHOUSE_HOST")
	if host == "" {
		host = "127.0.0.1"
	}

	portStr := os.Getenv("CLICKHOUSE_PORT")
	port := 9000
	if portStr != "" {
		var err error
		port, err = strconv.Atoi(portStr)
		if err != nil {
			return nil, fmt.Errorf("invalid CLICKHOUSE_PORT %q: %w", portStr, err)
		}
	}

	database := os.Getenv("CLICKHOUSE_DATABASE")
	if database == "" {
		database = "default"
	}

	username := os.Getenv("CLICKHOUSE_USER")
	if username == "" {
		username = "default"
	}

	password, err := getPassword()
	if err != nil {
		return nil, err
	}

	addr := net.JoinHostPort(host, strconv.Itoa(port))

	opts := &clickhouse.Options{
		Addr: []string{addr},
		Auth: clickhouse.Auth{
			Database: database,
			Username: username,
			Password: password,
		},
		DialTimeout: 5 * time.Second,
	}

	return opts, nil
}

// getPassword reads the ClickHouse password, preferring the file CLICKHOUSE_PASSWORD_FILE names.
//
// The file form mirrors JWT_SECRET_FILE and CLIENT_SECRET_FILE: it lets the deployment mount the
// credential from Secret Manager through the CSI driver instead of carrying it as a Helm value.
// An unreadable file is an error rather than a fall back to CLICKHOUSE_PASSWORD, because falling
// back would connect with the wrong credential and surface as an authentication failure instead.
func getPassword() (string, error) {
	path := os.Getenv("CLICKHOUSE_PASSWORD_FILE")
	if path == "" {
		return os.Getenv("CLICKHOUSE_PASSWORD"), nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read CLICKHOUSE_PASSWORD_FILE %q: %w", path, err)
	}

	// Secret Manager payloads routinely carry a trailing newline, which is not part of the password.
	return strings.TrimSpace(string(content)), nil
}

// Connect establishes a connection to ClickHouse using options loaded from environment variables.
func Connect(ctx context.Context) (driver.Conn, error) {
	opts, err := GetOptions()
	if err != nil {
		return nil, err
	}

	conn, err := clickhouse.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open clickhouse connection: %w", err)
	}

	if err := conn.Ping(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping clickhouse: %w", err)
	}

	return conn, nil
}
