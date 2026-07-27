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

package clickhouse

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// strategyFunc builds clickhouse.Options for an authentication attempt strategy.
type strategyFunc func() (*clickhouse.Options, error)

// ConnectWithFallback attempts multi-strategy authentication fallback before raising an error.
func ConnectWithFallback(ctx context.Context) (driver.Conn, error) {
	strategies := []strategyFunc{
		GetOptions,
		getOptionsUserNoPassword,
		getOptionsDefaultUser,
	}

	var errs []error
	for _, strategy := range strategies {
		conn, err := tryConnectStrategy(ctx, strategy)
		if err == nil {
			return conn, nil
		}
		errs = append(errs, err)
	}

	return nil, fmt.Errorf("all clickhouse auth strategies failed: %v", errs)
}

func tryConnectStrategy(ctx context.Context, strategy strategyFunc) (driver.Conn, error) {
	opts, err := strategy()
	if err != nil {
		return nil, fmt.Errorf("failed strategy config: %w", err)
	}

	conn, err := clickhouse.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("failed strategy open: %w", err)
	}

	if err := conn.Ping(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed strategy ping: %w", err)
	}

	return conn, nil
}

func buildOptionsWithAuth(username, password string) (*clickhouse.Options, error) {
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

	addr := net.JoinHostPort(host, strconv.Itoa(port))
	return &clickhouse.Options{
		Addr: []string{addr},
		Auth: clickhouse.Auth{
			Database: database,
			Username: username,
			Password: password,
		},
		DialTimeout: 5 * time.Second,
	}, nil
}

func getOptionsUserNoPassword() (*clickhouse.Options, error) {
	user := os.Getenv("CLICKHOUSE_USER")
	if user == "" {
		user = "default"
	}
	return buildOptionsWithAuth(user, "")
}

func getOptionsDefaultUser() (*clickhouse.Options, error) {
	return buildOptionsWithAuth("default", "")
}
