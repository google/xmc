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
	"flag"
	"fmt"
	"strconv"
)

// DefaultPort is the HTTP port serving the health and metrics endpoints.
const DefaultPort = "8080"

// Config holds the Study Controller configuration.
type Config struct {
	// Port is the HTTP port serving /healthz, /readyz and /metrics.
	Port string
}

// RegisterFlags registers the Study Controller flags on fs and returns the Config they populate
// once fs is parsed.
func RegisterFlags(fs *flag.FlagSet) *Config {
	c := &Config{}
	fs.StringVar(&c.Port, "port", DefaultPort, "HTTP port serving the health and metrics endpoints.")
	return c
}

// Validate reports whether c is usable.
func (c Config) Validate() error {
	port, err := strconv.Atoi(c.Port)
	if err != nil {
		return fmt.Errorf("invalid port %q: must be a number", c.Port)
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("invalid port %d: must be between 1 and 65535", port)
	}
	return nil
}
