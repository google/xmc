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

// Configured reports whether the environment points at a ClickHouse instance.
//
// GetOptions falls back to a localhost address so that local development needs no configuration,
// which means a failed connection alone cannot tell "ClickHouse is down" apart from "this
// deployment does not run the metrics pipeline". The helm chart injects these variables only when
// metrics are enabled, so their presence is what marks ClickHouse as expected to be reachable.
func Configured() bool {
	return os.Getenv("CLICKHOUSE_DSN") != "" || os.Getenv("CLICKHOUSE_HOST") != ""
}

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

// PasswordFromFileEnv reads the ClickHouse password from CLICKHOUSE_PASSWORD_FILE when set.
// It returns ("", false, nil) when CLICKHOUSE_PASSWORD_FILE is empty.
func PasswordFromFileEnv() (string, bool, error) {
	path := os.Getenv("CLICKHOUSE_PASSWORD_FILE")
	if path == "" {
		return "", false, nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return "", true, fmt.Errorf("failed to read CLICKHOUSE_PASSWORD_FILE %q: %w", path, err)
	}

	// Secret Manager payloads routinely carry a trailing newline, which is not part of the password.
	// Only the line terminator is stripped: a space is a legal password character, so trimming all
	// surrounding whitespace would corrupt a valid credential into an authentication failure.
	return strings.TrimRight(string(content), "\r\n"), true, nil
}

// getPassword reads the ClickHouse password, preferring the file CLICKHOUSE_PASSWORD_FILE names.
//
// The file form mirrors JWT_SECRET_FILE and CLIENT_SECRET_FILE: it lets the deployment mount the
// credential from Secret Manager through the CSI driver instead of carrying it as a Helm value.
// An unreadable file is an error rather than a fall back to CLICKHOUSE_PASSWORD, because falling
// back would connect with the wrong credential and surface as an authentication failure instead.
func getPassword() (string, error) {
	if pw, ok, err := PasswordFromFileEnv(); ok || err != nil {
		return pw, err
	}
	return os.Getenv("CLICKHOUSE_PASSWORD"), nil
}

// Connect establishes a connection to ClickHouse using options loaded from environment variables.
func Connect(ctx context.Context) (driver.Conn, error) {
	opts, err := GetOptions()
	if err != nil {
		return nil, err
	}
	return ConnectWithOptions(ctx, opts)
}

// ConnectWithOptions establishes and pings a connection to ClickHouse using the provided options.
func ConnectWithOptions(ctx context.Context, opts *clickhouse.Options) (driver.Conn, error) {
	if opts == nil {
		return nil, fmt.Errorf("clickhouse options cannot be nil")
	}

	conn, err := clickhouse.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open clickhouse connection: %w", err)
	}

	if err := conn.Ping(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to ping clickhouse: %w", err)
	}

	return conn, nil
}
