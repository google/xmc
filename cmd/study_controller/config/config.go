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
	"unicode"
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
	// ESSAddr is the host:port of the Experiment State Server gRPC endpoint.
	ESSAddr string
	// ESSAudience is the target audience used when minting Google ID tokens for ESS gRPC calls.
	ESSAudience string
	// ComponentsNamespace is the Kubernetes namespace containing the vizier Component CR.
	ComponentsNamespace string
}

// RegisterFlags registers the Study Controller flags on fs and returns the Config they populate
// once fs is parsed.
func RegisterFlags(fs *flag.FlagSet) *Config {
	c := &Config{}
	fs.StringVar(&c.Port, "port", DefaultPort, "HTTP port serving the health and metrics endpoints.")
	fs.StringVar(&c.RedisAddr, "redis_addr", "", "Required. Redis server address as host:port (no scheme or credentials).")
	fs.StringVar(&c.ESSAddr, "ess_addr", "", "Required. Experiment State Server gRPC address as host:port (no scheme or credentials).")
	fs.StringVar(&c.ESSAudience, "ess_audience", "", "Required. Audience for Google ID tokens sent to the Experiment State Server.")
	fs.StringVar(&c.ComponentsNamespace, "components_namespace", "", "Kubernetes namespace containing the vizier Component CR. Defaults to xmc when empty.")
	return c
}

// Validate reports whether c is usable.
func (c Config) Validate() error {
	if err := validatePort(c.Port); err != nil {
		return err
	}
	if err := validateHostPort("redis_addr", c.RedisAddr); err != nil {
		return err
	}
	if err := validateHostPort("ess_addr", c.ESSAddr); err != nil {
		return err
	}
	if err := validateESSAudience(c.ESSAudience); err != nil {
		return err
	}
	return validateOptionalNamespace(c.ComponentsNamespace)
}

// validateHostPort reports whether addr is a host:port with a non-empty host and a valid port.
// Its errors never echo addr, which a misconfiguration could turn into a URL with credentials.
func validateHostPort(flagName, addr string) error {
	if addr == "" {
		return fmt.Errorf("%s is required", flagName)
	}
	if strings.ContainsAny(addr, "@/") {
		return fmt.Errorf("invalid %s: must be host:port without a scheme, credentials or path", flagName)
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid %s: must be host:port, with IPv6 hosts in brackets", flagName)
	}
	if host == "" {
		return fmt.Errorf("invalid %s: host must not be empty", flagName)
	}
	for _, r := range host {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("invalid %s: host must not contain whitespace or control characters", flagName)
		}
	}
	if validatePort(port) != nil {
		return fmt.Errorf("invalid %s: port must be a number between 1 and 65535", flagName)
	}
	return nil
}

// validateESSAudience checks that aud is non-empty and free of whitespace or control characters.
// Its errors never echo aud.
func validateESSAudience(aud string) error {
	if strings.TrimSpace(aud) == "" {
		return errors.New("ess_audience is required")
	}
	for _, r := range aud {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return errors.New("invalid ess_audience: must not contain whitespace or control characters")
		}
	}
	return nil
}

// validateOptionalNamespace checks that ns, when non-empty, is a valid Kubernetes DNS-1123 label.
func validateOptionalNamespace(ns string) error {
	if ns == "" {
		return nil
	}
	isAlnum := func(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') }
	if len(ns) > 63 || !isAlnum(ns[0]) || !isAlnum(ns[len(ns)-1]) {
		return errors.New("invalid components_namespace: must be a 1-63 char DNS-1123 label")
	}
	for i := 0; i < len(ns); i++ {
		if !isAlnum(ns[i]) && ns[i] != '-' {
			return errors.New("invalid components_namespace: must be a 1-63 char DNS-1123 label")
		}
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
