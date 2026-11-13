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

// Package config defines the command-line configuration of the Study Controller.
package config

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// DefaultPort is the HTTP port serving the health and metrics endpoints.
const DefaultPort = "8080"

// Config holds the Study Controller configuration.
type Config struct {
	// Port is the HTTP port serving /healthz, /readyz and /metrics.
	Port string
	// RedisAddr is the host:port of the Redis server. The password is read from the
	// REDIS_PASSWORD environment variable, never from a flag.
	RedisAddr string
}

// RegisterFlags registers the Study Controller flags on fs and returns the Config they populate
// once fs is parsed.
func RegisterFlags(fs *flag.FlagSet) *Config {
	c := &Config{}
	fs.StringVar(&c.Port, "port", DefaultPort, "HTTP port serving the health and metrics endpoints.")
	fs.StringVar(&c.RedisAddr, "redis_addr", "", "Required. Redis server address as host:port (no scheme or credentials).")
	return c
}

// Validate reports whether c is usable.
func (c Config) Validate() error {
	if err := validatePort(c.Port); err != nil {
		return err
	}
	return validateRedisAddr(c.RedisAddr)
}

// validateRedisAddr reports whether addr is a host:port with a non-empty host and a valid port.
// Its errors never echo addr, which a misconfiguration could turn into a URL with credentials.
func validateRedisAddr(addr string) error {
	if addr == "" {
		return errors.New("redis_addr is required")
	}
	if strings.ContainsAny(addr, "@/") {
		return errors.New("invalid redis_addr: must be host:port without a scheme, credentials or path")
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return errors.New("invalid redis_addr: must be host:port, with IPv6 hosts in brackets")
	}
	if host == "" {
		return errors.New("invalid redis_addr: host must not be empty")
	}
	if validatePort(port) != nil {
		return errors.New("invalid redis_addr: port must be a number between 1 and 65535")
	}
	return nil
}

// validatePort reports whether port is a decimal TCP port number between 1 and 65535. A sign is
// rejected: the dialer would read "+6379" as a service name rather than a port.
func validatePort(port string) error {
	n, err := strconv.ParseUint(port, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid port %q: must be a number", port)
	}
	if n < 1 || n > 65535 {
		return fmt.Errorf("invalid port %d: must be between 1 and 65535", n)
	}
	return nil
}
